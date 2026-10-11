import logging
from dataclasses import dataclass
from datetime import datetime

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.catalog import PROMPT_VERSION, STYLE_MAP, TEMPLATE_VERSION
from app.config import Settings
from app.db import session_scope
from app.models import (
    Article,
    ArticleSource,
    ArticleTask,
    TopicCoverage,
    User,
    WritingPlan,
    WxAccount,
)
from app.security import decrypt_secret
from app.services.audit import find_sensitive, strip_cliches
from app.services.images import cover_png
from app.services.llm import Completion, ModelGateway, ProviderError, build_gateway, stage_messages
from app.services.providers import load_sensitive_words, load_wechat_runtime, resolve_providers
from app.services.quota import confirm_hold, membership_state, release_hold
from app.services.renderer import display_url, render_within_limit
from app.services.schedule import UTC, as_utc, compute_next_run, should_retry, tomorrow_start, window_end
from app.services.topics import CoverageItem, TopicChoice, pick_topic
from app.services.wechat import WeChatError, build_wechat_client

logger = logging.getLogger(__name__)


@dataclass
class WorkContext:
    task_id: int
    user_id: int
    tier: str
    plan_id: int
    direction: str
    style_key: str
    template_key: str
    word_target: int
    schedule_type: str
    weekdays: list
    time_window: str
    account_id: int
    app_id: str
    app_secret: str
    delivery_mode: str
    trigger_type: str
    schedule_date: str
    retry_count: int
    regen_article_id: int | None
    locked_topic: TopicChoice | None
    existing_media_id: str
    existing_thumb_id: str
    history: list[CoverageItem]
    recent_titles: list[str]


async def execute_task(task_id: int, settings: Settings, *, claim: bool = True, now: datetime | None = None) -> None:
    current = as_utc(now) or datetime.now(UTC)
    ctx = await _prepare(task_id, settings, claim, current)
    if ctx is None:
        return
    gateway = build_gateway(settings.llm_mode)
    try:
        result = await _generate(ctx, settings, gateway, current)
    except Exception as exc:
        logger.exception("task %s failed", task_id)
        await _finish_failure(task_id, settings, str(exc)[:500], current, auth_failed=_auth_failed(exc))
        return
    await _finish_success(task_id, settings, result, current)


async def _prepare(task_id: int, settings: Settings, claim: bool, now: datetime) -> WorkContext | None:
    async with session_scope() as session:
        if claim:
            task = await session.get(ArticleTask, task_id)
            if not task or task.status != "pending":
                return None
            task.status = "running"
            task.started_at = now
        else:
            task = await session.get(ArticleTask, task_id)
            if not task or task.status != "running":
                return None
            if task.started_at is None:
                task.started_at = now
        user = await session.get(User, task.user_id)
        plan = await session.get(WritingPlan, task.plan_id)
        account = await session.get(WxAccount, task.wx_account_id)
        if not user or not plan or not account:
            task.status = "failed"
            task.failure_reason = "任务关联的数据不存在"
            await release_hold(session, task.id, "数据缺失，退回额度")
            return None
        history_rows = (
            await session.scalars(select(TopicCoverage).where(TopicCoverage.plan_id == plan.id).order_by(TopicCoverage.id.asc()))
        ).all()
        titles = (
            await session.scalars(
                select(Article.title)
                .where(Article.plan_id == plan.id, Article.status.in_(("in_draft", "exported")))
                .order_by(Article.id.desc())
                .limit(20)
            )
        ).all()
        locked = None
        existing_media = ""
        existing_thumb = ""
        article = None
        if task.regen_of_article_id:
            article = await session.get(Article, task.regen_of_article_id)
            if article is None:
                task.status = "failed"
                task.failure_reason = "找不到要重写的文章"
                await release_hold(session, task.id, "文章不存在，退回额度")
                return None
            if article.topic_tag and "|" in article.topic_tag:
                parts = article.topic_tag.split("|")
                if len(parts) == 3:
                    locked = TopicChoice(parts[0], parts[1], parts[2], article.topic_tag)
            existing_media = article.draft_media_id or ""
            existing_thumb = article.thumb_media_id or ""
            article.status = "generating"
            article.failure_reason = ""
            task.article_id = article.id
        secret = ""
        if account.secret_encrypted:
            secret = decrypt_secret(settings.master_key, account.secret_encrypted)
        tier, _state = membership_state(user, now)
        if article is None:
            article = await session.scalar(select(Article).where(Article.task_id == task.id))
        if article is None:
            article = Article(
                task_id=task.id,
                user_id=user.id,
                plan_id=plan.id,
                wx_account_id=account.id,
                title="正在生成",
                status="generating",
                template_key=plan.template_key,
                style_key=plan.style_key,
                prompt_version=PROMPT_VERSION,
            )
            session.add(article)
            await session.flush()
            task.article_id = article.id
        return WorkContext(
            task_id=task.id,
            user_id=user.id,
            tier=tier,
            plan_id=plan.id,
            direction=plan.direction,
            style_key=plan.style_key,
            template_key=plan.template_key,
            word_target=plan.word_target,
            schedule_type=plan.schedule_type,
            weekdays=list(plan.weekdays or []),
            time_window=plan.time_window,
            account_id=account.id,
            app_id=account.app_id,
            app_secret=secret,
            delivery_mode=account.delivery_mode,
            trigger_type=task.trigger_type,
            schedule_date=task.schedule_date,
            retry_count=task.retry_count,
            regen_article_id=task.regen_of_article_id,
            locked_topic=locked,
            existing_media_id=existing_media,
            existing_thumb_id=existing_thumb,
            history=[
                CoverageItem(row.subtopic, row.column_type, row.angle, row.created_at) for row in history_rows
            ],
            recent_titles=[title for title in titles if title],
        )


async def _generate(ctx: WorkContext, settings: Settings, gateway: ModelGateway, now: datetime) -> dict:
    async with session_scope() as session:
        sensitive = await load_sensitive_words(session)
        wechat_cfg = await load_wechat_runtime(session, settings)
        stage_providers = {}
        for stage, _label in (("topic", "选题"), ("outline", "大纲"), ("draft", "正文"), ("review", "审核")):
            stage_providers[stage] = await resolve_providers(session, settings, stage, ctx.tier)

    topic = ctx.locked_topic or pick_topic(ctx.direction, ctx.tier, ctx.history, now)
    style = STYLE_MAP.get(ctx.style_key, STYLE_MAP["warm"])
    base_payload = {
        "direction": ctx.direction,
        "subtopic": topic.subtopic,
        "column": topic.column,
        "angle": topic.angle,
        "style_key": ctx.style_key,
        "style_label": style["name"],
        "style_hint": style["description"],
        "word_target": ctx.word_target,
        "recent_titles": ctx.recent_titles,
        "tier": ctx.tier,
    }
    trace: list[dict] = []
    tokens = 0

    topic_data, completion = await gateway.complete_json(
        stage_providers["topic"], stage_messages("选题", base_payload), _validate_topic
    )
    tokens += completion.prompt_tokens + completion.completion_tokens
    trace.extend(_trace("topic", completion))
    topic_title = str(topic_data.get("title") or f"{topic.subtopic}：{topic.angle}")[:64]

    outline_payload = {**base_payload, "topic_title": topic_title}
    outline, completion = await gateway.complete_json(
        stage_providers["outline"], stage_messages("大纲", outline_payload), _validate_outline
    )
    tokens += completion.prompt_tokens + completion.completion_tokens
    trace.extend(_trace("outline", completion))

    draft_payload = {**outline_payload, "outline": outline}
    draft, completion = await gateway.complete_json(
        stage_providers["draft"], stage_messages("正文", draft_payload), _validate_draft
    )
    tokens += completion.prompt_tokens + completion.completion_tokens
    trace.extend(_trace("draft", completion))

    review_payload = {**draft_payload, "draft": draft, "issues": find_sensitive(draft, sensitive)}
    reviewed, completion = await gateway.complete_json(
        stage_providers["review"], stage_messages("审核", review_payload), _validate_draft
    )
    tokens += completion.prompt_tokens + completion.completion_tokens
    trace.extend(_trace("review", completion))
    draft = strip_cliches(reviewed)
    hits = find_sensitive(draft, sensitive)
    if hits:
        review_payload["draft"] = draft
        review_payload["issues"] = hits
        reviewed, completion = await gateway.complete_json(
            stage_providers["review"], stage_messages("审核", review_payload), _validate_draft
        )
        tokens += completion.prompt_tokens + completion.completion_tokens
        trace.extend(_trace("review", completion))
        draft = strip_cliches(reviewed)
        hits = find_sensitive(draft, sensitive)
        if hits:
            raise ProviderError("审核未通过，命中敏感词：" + "、".join(hits))

    if ctx.tier == "free":
        draft["sources"] = []
    draft["title"] = str(draft.get("title") or topic_title)[:64]
    footnotes = ["请发布前审阅。"]
    if "母婴" in ctx.direction:
        footnotes.append("本文仅供科普，不能替代医生诊疗意见。")
    draft["footnotes"] = footnotes
    html = render_within_limit(draft, ctx.template_key)

    pushed = await _push(ctx, draft, html, wechat_cfg)
    return {
        "draft": draft,
        "html": html,
        "topic": topic,
        "tokens": tokens,
        "trace": trace,
        **pushed,
    }


async def _push(ctx: WorkContext, draft: dict, html: str, wechat_cfg: dict) -> dict:
    article_payload = {
        "title": draft["title"],
        "digest": draft.get("summary") or "",
        "content": html,
        "author": "公号智写",
        "thumb_media_id": ctx.existing_thumb_id,
    }
    if ctx.delivery_mode != "draft":
        return {
            "status": "exported",
            "draft_media_id": "",
            "thumb_media_id": "",
            "delivery_note": "当前公众号为手动导出模式，请复制排版 HTML 后粘贴到公众号编辑器。",
            "auth_failed": False,
        }
    mode = "mock" if wechat_cfg.get("mode") == "mock" or ctx.app_id.startswith("wx_mock") else "live"
    client = build_wechat_client(ctx.app_id, ctx.app_secret, mode=mode, base_url=wechat_cfg.get("api_base") or "https://api.weixin.qq.com")
    try:
        if not article_payload["thumb_media_id"]:
            article_payload["thumb_media_id"] = await client.upload_thumb(cover_png(ctx.template_key))
        if ctx.existing_media_id:
            media_id = await client.update_draft(ctx.existing_media_id, article_payload)
        else:
            media_id = await client.add_draft(article_payload)
        return {
            "status": "in_draft",
            "draft_media_id": media_id,
            "thumb_media_id": article_payload["thumb_media_id"],
            "delivery_note": "",
            "auth_failed": False,
        }
    except WeChatError as exc:
        if _auth_failed(exc):
            raise
        return {
            "status": "exported",
            "draft_media_id": "",
            "thumb_media_id": article_payload.get("thumb_media_id") or "",
            "delivery_note": f"写入草稿失败，已改为手动导出：{exc}",
            "auth_failed": False,
        }


async def _finish_success(task_id: int, settings: Settings, result: dict, now: datetime) -> None:
    async with session_scope() as session:
        task = await session.get(ArticleTask, task_id)
        article = await session.get(Article, task.article_id)
        plan = await session.get(WritingPlan, task.plan_id)
        draft = result["draft"]
        topic: TopicChoice = result["topic"]
        article.title = draft["title"]
        article.alt_titles = draft.get("alt_titles") or []
        article.summary = draft.get("summary") or ""
        article.body_json = draft
        article.body_html = result["html"]
        article.sources = draft.get("sources") or []
        article.thumb_media_id = result.get("thumb_media_id") or ""
        article.draft_media_id = result.get("draft_media_id") or ""
        article.template_key = plan.template_key
        article.template_version = TEMPLATE_VERSION
        article.style_key = plan.style_key
        article.prompt_version = PROMPT_VERSION
        article.status = result["status"]
        article.failure_reason = ""
        article.delivery_note = result.get("delivery_note") or ""
        article.subtopic = topic.subtopic
        article.column_type = topic.column
        article.topic_tag = topic.topic_tag
        article.risk_level = "low"
        if task.trigger_type == "regenerate":
            article.regen_count = int(article.regen_count or 0) + 1
        if task.trigger_type != "regenerate":
            session.add(
                TopicCoverage(
                    plan_id=plan.id,
                    subtopic=topic.subtopic,
                    column_type=topic.column,
                    angle=topic.angle,
                    topic_tag=topic.topic_tag,
                )
            )
        old_sources = (
            await session.scalars(select(ArticleSource).where(ArticleSource.article_id == article.id))
        ).all()
        for row in old_sources:
            await session.delete(row)
        for index, source in enumerate(draft.get("sources") or [], start=1):
            session.add(
                ArticleSource(
                    article_id=article.id,
                    seq=index,
                    source_type=source.get("source_type") or "other",
                    name=source.get("name") or "",
                    title=source.get("title") or "",
                    published_on=source.get("published_on") or "",
                    raw_url=source.get("url") or "",
                    display_url=display_url(source.get("url") or ""),
                    accessible="未校验",
                )
            )
        await confirm_hold(session, task.id)
        task.status = "success"
        task.failure_reason = ""
        task.finished_at = now
        task.tokens = int(result.get("tokens") or 0)
        task.model_trace = result.get("trace") or []
        if task.started_at:
            task.duration_ms = int((as_utc(now) - as_utc(task.started_at)).total_seconds() * 1000)
        if task.trigger_type != "regenerate":
            plan.next_run_at = compute_next_run(
                tomorrow_start(now),
                schedule_type=plan.schedule_type,
                weekdays=list(plan.weekdays or []),
                window_key=plan.time_window,
                rng=_rng(),
            )
        if plan.status == "paused" and plan.pause_reason == "额度已用完":
            plan.status = "active"
            plan.pause_reason = ""


async def _finish_failure(task_id: int, settings: Settings, message: str, now: datetime, *, auth_failed: bool) -> None:
    async with session_scope() as session:
        task = await session.get(ArticleTask, task_id)
        if not task:
            return
        plan = await session.get(WritingPlan, task.plan_id)
        article = await session.get(Article, task.article_id) if task.article_id else None
        retry = False
        if plan and task.schedule_date and not auth_failed:
            try:
                end = window_end(task.schedule_date, plan.time_window)
                retry = should_retry(task.trigger_type, task.retry_count, now, end)
            except Exception:
                retry = False
        if auth_failed:
            account = await session.get(WxAccount, task.wx_account_id)
            if account:
                account.bind_status = "auth_invalid"
            plans = (
                await session.scalars(
                    select(WritingPlan).where(
                        WritingPlan.wx_account_id == task.wx_account_id,
                        WritingPlan.status == "active",
                    )
                )
            ).all()
            for item in plans:
                item.status = "paused"
                item.pause_reason = "授权失效"
            message = "公众号授权失效，请重新绑定。未扣除额度。"
        if retry:
            task.status = "pending"
            task.retry_count += 1
            task.failure_reason = message
            task.run_at = now
            if article:
                article.status = "generating"
                article.failure_reason = "生成失败，将在时段内重试：" + message
            return
        task.status = "abandoned" if task.trigger_type == "schedule" else "failed"
        task.failure_reason = message
        task.finished_at = now
        if article:
            article.status = "failed"
            article.failure_reason = message
        await release_hold(session, task.id, "失败退回额度")


def _trace(stage: str, completion: Completion) -> list[dict]:
    rows = []
    for attempt in completion.attempts:
        rows.append({"stage": stage, **attempt})
    if not rows:
        rows.append({"stage": stage, "provider_id": completion.provider_id, "protocol": completion.protocol, "ok": True})
    return rows


def _validate_topic(data: dict) -> dict:
    title = str(data.get("title") or "").strip()
    if not title:
        raise ValueError("选题缺少标题")
    data["title"] = title[:64]
    return data


def _validate_outline(data: dict) -> dict:
    points = data.get("points")
    if not isinstance(points, list):
        raise ValueError("大纲格式不正确")
    cleaned = []
    for point in points[:5]:
        if isinstance(point, dict) and (point.get("heading") or point.get("intent")):
            cleaned.append(
                {
                    "heading": str(point.get("heading") or "小节")[:40],
                    "intent": str(point.get("intent") or "")[:120],
                }
            )
    if len(cleaned) < 2:
        raise ValueError("大纲至少需要两个小节")
    data["points"] = cleaned
    data["closing"] = str(data.get("closing") or "")[:200]
    data["hook"] = str(data.get("hook") or "")[:200]
    return data


def _validate_draft(data: dict) -> dict:
    title = str(data.get("title") or "").strip()
    sections = data.get("sections")
    if not title or not isinstance(sections, list):
        raise ValueError("正文缺少标题或小节")
    normalized = []
    for section in sections[:6]:
        if not isinstance(section, dict):
            continue
        paragraphs = [str(item).strip() for item in section.get("paragraphs") or [] if str(item).strip()]
        items = [str(item).strip() for item in section.get("list") or [] if str(item).strip()]
        heading = str(section.get("heading") or "").strip()
        if not heading and not paragraphs:
            continue
        normalized.append({"heading": heading[:40], "paragraphs": paragraphs[:4], "list": items[:6]})
    if not normalized:
        raise ValueError("正文没有可用小节")
    sources = []
    for source in data.get("sources") or []:
        if not isinstance(source, dict):
            continue
        url = str(source.get("url") or "")
        if url and not url.startswith(("http://", "https://")):
            url = ""
        name = str(source.get("name") or "").strip()
        source_title = str(source.get("title") or "").strip()
        if not name and not source_title:
            continue
        sources.append(
            {
                "name": name[:80],
                "title": source_title[:120],
                "published_on": str(source.get("published_on") or "")[:32],
                "url": url[:300],
                "source_type": str(source.get("source_type") or "other")[:32],
            }
        )
    return {
        "title": title[:64],
        "alt_titles": [str(item)[:64] for item in (data.get("alt_titles") or [])[:5] if str(item).strip()],
        "summary": str(data.get("summary") or "")[:120],
        "sections": normalized,
        "closing": str(data.get("closing") or "")[:300],
        "sources": sources[:8],
    }


def _auth_failed(exc: Exception) -> bool:
    text = str(exc).lower()
    return "40001" in text or "invalid credential" in text or "授权失效" in str(exc)


def _rng():
    import random

    return random.SystemRandom()
