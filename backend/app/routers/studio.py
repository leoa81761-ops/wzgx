from datetime import datetime, timedelta, timezone

from fastapi import APIRouter, Request
from fastapi.responses import HTMLResponse
from sqlalchemy import func, select

from app.catalog import (
    BLOCKED_DIRECTION_WORDS,
    COLUMNS,
    DIRECTIONS,
    LEGAL_VERSION,
    STYLE_MAP,
    STYLES,
    TEMPLATE_MAP,
    TEMPLATES,
    WINDOW_HOURS,
    WINDOW_LABELS,
    WINDOWS,
    WORD_CHOICES,
)
from app.db import session_scope
from app.deps import client_ip, current_user, user_agent
from app.errors import AppError
from app.models import Article, ConsentLog, LegalDoc, PlanConfig, User, WritingPlan, WxAccount
from app.schemas import AccountIn, PlanIn, SecretIn
from app.security import encrypt_secret
from app.services.jobs import create_generation_task
from app.services.pipeline import execute_task
from app.services.providers import load_wechat_runtime
from app.services.quota import QuotaError, enforce_free_limits, get_limits, membership_state, quota_snapshot
from app.services.renderer import render_within_limit
from app.services.schedule import as_utc, compute_next_run
from app.services.wechat import check_permissions, build_wechat_client

router = APIRouter(tags=["studio"])

STATUS_LABELS = {
    "generating": "生成中",
    "in_draft": "已入草稿箱",
    "exported": "已导出",
    "failed": "失败",
}


@router.get("/api/legal")
async def legal_list():
    async with session_scope() as session:
        rows = (await session.scalars(select(LegalDoc).where(LegalDoc.status == "active").order_by(LegalDoc.id.asc()))).all()
        return [{"doc_type": row.doc_type, "title": row.title, "version": row.version} for row in rows]


@router.get("/api/legal/{doc_type}")
async def legal_detail(doc_type: str):
    async with session_scope() as session:
        row = await session.scalar(
            select(LegalDoc).where(LegalDoc.doc_type == doc_type, LegalDoc.status == "active").order_by(LegalDoc.id.desc())
        )
        if row is None:
            raise AppError("没有这份文档", 404)
        return {
            "doc_type": row.doc_type,
            "title": row.title,
            "version": row.version,
            "content": row.content,
            "effective_date": row.effective_date,
        }


@router.get("/api/meta")
async def meta(request: Request):
    settings = request.app.state.settings
    async with session_scope() as session:
        runtime = await load_wechat_runtime(session, settings)
        plans = (await session.scalars(select(PlanConfig).order_by(PlanConfig.daily_quota.asc()))).all()
    return {
        "styles": STYLES,
        "templates": TEMPLATES,
        "directions": DIRECTIONS,
        "windows": WINDOWS,
        "word_choices": WORD_CHOICES,
        "columns": COLUMNS,
        "wechat_mode": runtime["mode"],
        "public_egress_ip": runtime["public_egress_ip"],
        "plans": [
            {
                "tier": item.tier,
                "name": item.name,
                "price_label": item.price_label,
                "daily_quota": item.daily_quota,
                "period_quota": item.period_quota,
                "period_unit": item.period_unit,
                "max_accounts": item.max_accounts,
                "max_plans": item.max_plans,
                "max_words": item.max_words,
                "summary": item.summary,
            }
            for item in plans
        ],
    }


@router.get("/api/me/quota")
async def my_quota(request: Request):
    user = await current_user(request)
    async with session_scope() as session:
        fresh = await session.get(User, user.id)
        return _jsonable(await quota_snapshot(session, fresh, datetime.now(timezone.utc)))


@router.get("/api/me/consents")
async def my_consents(request: Request):
    user = await current_user(request)
    async with session_scope() as session:
        rows = (
            await session.scalars(select(ConsentLog).where(ConsentLog.user_id == user.id).order_by(ConsentLog.id.desc()))
        ).all()
        docs = (await session.scalars(select(LegalDoc))).all()
        titles = {item.doc_type: item.title for item in docs}
        titles["wx_credentials"] = "公众号凭证授权"
        return [
            {
                "id": row.id,
                "doc_type": row.doc_type,
                "title": titles.get(row.doc_type, row.doc_type),
                "version": row.version,
                "consent_item": row.consent_item,
                "created_at": row.created_at,
            }
            for row in rows
        ]


@router.get("/api/accounts")
async def list_accounts(request: Request):
    user = await current_user(request)
    async with session_scope() as session:
        rows = (
            await session.scalars(
                select(WxAccount)
                .where(WxAccount.user_id == user.id, WxAccount.bind_status != "unbound")
                .order_by(WxAccount.id.desc())
            )
        ).all()
        return [_account_dict(row) for row in rows]


@router.post("/api/accounts")
async def bind_account(body: AccountIn, request: Request):
    if body.consent is not True:
        raise AppError("绑定前请勾选同意保存公众号凭证")
    if body.subject_type not in ("personal", "enterprise"):
        raise AppError("请选择主体类型")
    if body.service_type not in ("subscription", "service"):
        raise AppError("请选择订阅号或服务号")
    if body.subject_type == "personal" and body.verified:
        raise AppError("个人主体无法认证，请不要勾选已认证")
    user = await current_user(request)
    settings = request.app.state.settings
    now = datetime.now(timezone.utc)
    async with session_scope() as session:
        fresh = await session.get(User, user.id)
        await enforce_free_limits(session, fresh, now)
        tier, state = membership_state(fresh, now)
        if state == "grace":
            raise AppError("会员处于宽限期，暂不能新增公众号")
        limits = await get_limits(session, tier)
        active_count = await session.scalar(
            select(func.count()).select_from(WxAccount).where(WxAccount.user_id == user.id, WxAccount.bind_status == "active")
        )
        if int(active_count or 0) >= limits.max_accounts:
            raise AppError("已达到当前会员可绑定的公众号数量")
        await _assert_can_bind(session, user.id, body.app_id.strip(), now)
        runtime = await load_wechat_runtime(session, settings)
        mode = "mock" if runtime["mode"] == "mock" or body.app_id.strip().startswith("wx_mock") else "live"
        client = build_wechat_client(body.app_id.strip(), body.app_secret.strip(), mode=mode, base_url=runtime["api_base"])
        report = await check_permissions(client)
        if report.auth_failed:
            raise AppError("凭证无效，或微信接口拒绝了调用。请检查 AppID、AppSecret 和 IP 白名单。模拟模式可使用任意看起来合法的 AppID。")
        account = WxAccount(
            user_id=user.id,
            name=body.name.strip(),
            app_id=body.app_id.strip(),
            secret_encrypted=encrypt_secret(settings.master_key, body.app_secret.strip()),
            subject_type=body.subject_type,
            service_type=body.service_type,
            verified=body.verified,
            delivery_mode=report.delivery_mode,
            bind_status="active",
            permission_report={"checks": report.checks, "can_draft": report.can_draft},
            bound_at=now,
        )
        session.add(account)
        session.add(
            ConsentLog(
                user_id=user.id,
                doc_type="wx_credentials",
                version=LEGAL_VERSION,
                consent_item="wx_credentials",
                ip=client_ip(request),
                user_agent=user_agent(request),
            )
        )
        await session.flush()
        payload = _account_dict(account)
        payload["auto_publish_note"] = ""
        if body.subject_type == "enterprise" and body.verified:
            payload["auto_publish_note"] = "企业认证号的自动发布尚未开放，当前只会写入草稿箱。"
        return payload


@router.post("/api/accounts/{account_id}/recheck")
async def recheck_account(account_id: int, body: SecretIn, request: Request):
    user = await current_user(request)
    settings = request.app.state.settings
    async with session_scope() as session:
        account = await _own_account(session, user.id, account_id)
        from app.security import decrypt_secret

        secret = body.app_secret.strip()
        if secret:
            account.secret_encrypted = encrypt_secret(settings.master_key, secret)
        elif account.secret_encrypted:
            secret = decrypt_secret(settings.master_key, account.secret_encrypted)
        else:
            raise AppError("请重新填写 AppSecret")
        runtime = await load_wechat_runtime(session, settings)
        mode = "mock" if runtime["mode"] == "mock" or account.app_id.startswith("wx_mock") else "live"
        client = build_wechat_client(account.app_id, secret, mode=mode, base_url=runtime["api_base"])
        report = await check_permissions(client)
        account.permission_report = {"checks": report.checks, "can_draft": report.can_draft}
        if report.auth_failed:
            account.bind_status = "auth_invalid"
            failed = True
        else:
            account.bind_status = "active"
            account.delivery_mode = report.delivery_mode
            failed = False
            plans = (
                await session.scalars(
                    select(WritingPlan).where(
                        WritingPlan.wx_account_id == account.id, WritingPlan.pause_reason == "授权失效"
                    )
                )
            ).all()
            for plan in plans:
                plan.status = "active"
                plan.pause_reason = ""
        payload = _account_dict(account)
    if failed:
        raise AppError("凭证仍然无效，账号已标记为授权失效")
    return payload


@router.delete("/api/accounts/{account_id}")
async def unbind_account(account_id: int, request: Request):
    user = await current_user(request)
    now = datetime.now(timezone.utc)
    async with session_scope() as session:
        account = await _own_account(session, user.id, account_id)
        account.bind_status = "unbound"
        account.secret_encrypted = ""
        account.unbound_at = now
        plans = (
            await session.scalars(select(WritingPlan).where(WritingPlan.wx_account_id == account.id, WritingPlan.status != "deleted"))
        ).all()
        for plan in plans:
            plan.status = "paused"
            plan.pause_reason = "已解绑"
        return {"ok": True}


@router.get("/api/plans")
async def list_plans(request: Request):
    user = await current_user(request)
    async with session_scope() as session:
        rows = (
            await session.scalars(
                select(WritingPlan).where(WritingPlan.user_id == user.id, WritingPlan.status != "deleted").order_by(WritingPlan.id.desc())
            )
        ).all()
        return [_plan_dict(row) for row in rows]


@router.post("/api/plans")
async def create_plan(body: PlanIn, request: Request):
    user = await current_user(request)
    settings = request.app.state.settings
    now = datetime.now(timezone.utc)
    direction = body.direction.strip()
    for word in BLOCKED_DIRECTION_WORDS:
        if word in direction:
            raise AppError(f"「{word}」一类方向风险较高，当前版本不开放自动写作")
    if body.style_key not in STYLE_MAP or body.template_key not in TEMPLATE_MAP:
        raise AppError("请选择风格和排版模板")
    if body.schedule_type not in ("daily", "weekly"):
        raise AppError("周期只支持每天或每周")
    weekdays = sorted(set(body.weekdays))
    if body.schedule_type == "weekly" and not weekdays:
        raise AppError("每周生成请至少选择一天")
    if any(day < 1 or day > 7 for day in weekdays):
        raise AppError("星期取值应为 1 到 7")
    if body.time_window not in WINDOW_HOURS:
        raise AppError("请选择生成时段")
    async with session_scope() as session:
        fresh = await session.get(User, user.id)
        await enforce_free_limits(session, fresh, now)
        tier, state = membership_state(fresh, now)
        if state == "grace":
            raise AppError("会员处于宽限期，暂不能新增写作方向")
        limits = await get_limits(session, tier)
        if body.word_target not in WORD_CHOICES or body.word_target > limits.max_words:
            raise AppError(f"当前会员单篇上限为 {limits.max_words} 字")
        plan_count = await session.scalar(
            select(func.count()).select_from(WritingPlan).where(WritingPlan.user_id == user.id, WritingPlan.status != "deleted")
        )
        if int(plan_count or 0) >= limits.max_plans:
            raise AppError("已达到当前会员可设置的写作方向数量")
        account = await _own_account(session, user.id, body.wx_account_id)
        if account.bind_status != "active":
            raise AppError("请先完成公众号绑定")
        window = "night" if tier == "free" else body.time_window
        plan = WritingPlan(
            user_id=user.id,
            wx_account_id=account.id,
            direction=direction,
            mode="managed",
            style_key=body.style_key,
            template_key=body.template_key,
            word_target=body.word_target,
            schedule_type=body.schedule_type,
            weekdays=weekdays,
            time_window=window,
            status="active",
            next_run_at=compute_next_run(
                now,
                schedule_type=body.schedule_type,
                weekdays=weekdays,
                window_key=window,
                rng=_rng(),
            ),
        )
        session.add(plan)
        await session.flush()
        return _plan_dict(plan)


@router.post("/api/plans/{plan_id}/pause")
async def pause_plan(plan_id: int, request: Request):
    user = await current_user(request)
    async with session_scope() as session:
        plan = await _own_plan(session, user.id, plan_id)
        plan.status = "paused"
        plan.pause_reason = "用户暂停"
        return _plan_dict(plan)


@router.post("/api/plans/{plan_id}/resume")
async def resume_plan(plan_id: int, request: Request):
    user = await current_user(request)
    now = datetime.now(timezone.utc)
    async with session_scope() as session:
        plan = await _own_plan(session, user.id, plan_id)
        fresh = await session.get(User, user.id)
        if plan.pause_reason == "额度已用完":
            reason = await quota_snapshot(session, fresh, now)
            if reason["daily_used"] >= reason["daily_limit"] or reason["period_used"] >= reason["period_limit"]:
                raise AppError("额度仍不足，暂时不能恢复")
        plan.status = "active"
        plan.pause_reason = ""
        if plan.next_run_at is None or as_utc(plan.next_run_at) <= now:
            plan.next_run_at = compute_next_run(
                now,
                schedule_type=plan.schedule_type,
                weekdays=list(plan.weekdays or []),
                window_key=plan.time_window,
                rng=_rng(),
            )
        return _plan_dict(plan)


@router.delete("/api/plans/{plan_id}")
async def delete_plan(plan_id: int, request: Request):
    user = await current_user(request)
    async with session_scope() as session:
        plan = await _own_plan(session, user.id, plan_id)
        plan.status = "deleted"
        return {"ok": True}


@router.post("/api/plans/{plan_id}/generate")
async def generate_now(plan_id: int, request: Request):
    user = await current_user(request)
    settings = request.app.state.settings
    now = datetime.now(timezone.utc)
    async with session_scope() as session:
        fresh = await session.get(User, user.id)
        plan = await _own_plan(session, user.id, plan_id)
        account = await _own_account(session, user.id, plan.wx_account_id)
        await enforce_free_limits(session, fresh, now)
        await session.refresh(plan)
        try:
            task = await create_generation_task(
                session,
                user=fresh,
                plan=plan,
                account=account,
                trigger_type="manual",
                now=now,
            )
            task_id = task.id
        except QuotaError as exc:
            raise AppError(exc.message) from exc
    await execute_task(task_id, settings, claim=True, now=now)
    async with session_scope() as session:
        from app.models import ArticleTask

        task = await session.get(ArticleTask, task_id)
        article = await session.get(Article, task.article_id) if task and task.article_id else None
        if article is None:
            raise AppError(task.failure_reason if task else "生成失败")
        return _article_dict(article)


@router.get("/api/articles")
async def list_articles(request: Request):
    user = await current_user(request)
    async with session_scope() as session:
        rows = (
            await session.scalars(select(Article).where(Article.user_id == user.id).order_by(Article.id.desc()).limit(100))
        ).all()
        return [_article_dict(row, include_html=False) for row in rows]


@router.get("/api/articles/{article_id}")
async def article_detail(article_id: int, request: Request, theme: str = "light"):
    user = await current_user(request)
    async with session_scope() as session:
        article = await _own_article(session, user.id, article_id)
        payload = _article_dict(article)
        if theme == "dark" and article.body_json:
            payload["body_html"] = render_within_limit(article.body_json, article.template_key, dark=True)
            payload["theme_note"] = "暗色预览只在站内查看，写入草稿箱的仍是浅色模板。"
        return payload


@router.get("/api/articles/{article_id}/export")
async def export_article(article_id: int, request: Request):
    user = await current_user(request)
    async with session_scope() as session:
        article = await _own_article(session, user.id, article_id)
        html = article.body_html or ""
    return HTMLResponse(html, headers={"Content-Disposition": f'attachment; filename="article-{article_id}.html"'})


@router.post("/api/articles/{article_id}/regenerate")
async def regenerate(article_id: int, request: Request):
    user = await current_user(request)
    settings = request.app.state.settings
    now = datetime.now(timezone.utc)
    async with session_scope() as session:
        fresh = await session.get(User, user.id)
        article = await _own_article(session, user.id, article_id)
        plan = await _own_plan(session, user.id, article.plan_id)
        account = await _own_account(session, user.id, article.wx_account_id)
        try:
            task = await create_generation_task(
                session,
                user=fresh,
                plan=plan,
                account=account,
                trigger_type="regenerate",
                now=now,
                regen_article=article,
            )
            task_id = task.id
        except QuotaError as exc:
            raise AppError(exc.message) from exc
    await execute_task(task_id, settings, claim=True, now=now)
    async with session_scope() as session:
        article = await session.get(Article, article_id)
        return _article_dict(article)


async def _assert_can_bind(session, user_id: int, app_id: str, now: datetime) -> None:
    rows = (await session.scalars(select(WxAccount).where(WxAccount.app_id == app_id))).all()
    for row in rows:
        if row.bind_status in ("active", "auth_invalid"):
            if row.user_id == user_id:
                raise AppError("你已经绑定了这个公众号")
            raise AppError("该公众号已绑定在其他账号上")
        unbound_at = as_utc(row.unbound_at)
        if row.bind_status == "unbound" and unbound_at and row.user_id != user_id:
            if now < unbound_at + timedelta(days=30):
                raise AppError("该公众号解绑后 30 天内不能被其他账号绑定")


async def _own_account(session, user_id: int, account_id: int) -> WxAccount:
    account = await session.get(WxAccount, account_id)
    if account is None or account.user_id != user_id or account.bind_status == "unbound":
        raise AppError("找不到这个公众号", 404)
    return account


async def _own_plan(session, user_id: int, plan_id: int) -> WritingPlan:
    plan = await session.get(WritingPlan, plan_id)
    if plan is None or plan.user_id != user_id or plan.status == "deleted":
        raise AppError("找不到这个写作计划", 404)
    return plan


async def _own_article(session, user_id: int, article_id: int) -> Article:
    article = await session.get(Article, article_id)
    if article is None or article.user_id != user_id:
        raise AppError("找不到这篇文章", 404)
    return article


def _account_dict(account: WxAccount) -> dict:
    subject = "个人" if account.subject_type == "personal" else "企业"
    service = "订阅号" if account.service_type == "subscription" else "服务号"
    verified = "已认证" if account.verified else "未认证"
    return {
        "id": account.id,
        "name": account.name,
        "app_id": account.app_id,
        "subject_type": account.subject_type,
        "service_type": account.service_type,
        "verified": account.verified,
        "type_label": f"{subject} · {service} · {verified}",
        "delivery_mode": account.delivery_mode,
        "delivery_label": "写入草稿箱" if account.delivery_mode == "draft" else "仅手动导出",
        "bind_status": account.bind_status,
        "permission_report": account.permission_report or {},
        "bound_at": account.bound_at,
    }


def _plan_dict(plan: WritingPlan) -> dict:
    style = STYLE_MAP.get(plan.style_key, {})
    template = TEMPLATE_MAP.get(plan.template_key, {})
    return {
        "id": plan.id,
        "wx_account_id": plan.wx_account_id,
        "direction": plan.direction,
        "mode": plan.mode,
        "style_key": plan.style_key,
        "style_name": style.get("name", plan.style_key),
        "template_key": plan.template_key,
        "template_name": template.get("name", plan.template_key),
        "word_target": plan.word_target,
        "schedule_type": plan.schedule_type,
        "schedule_label": "每天" if plan.schedule_type == "daily" else "每周",
        "weekdays": plan.weekdays or [],
        "time_window": plan.time_window,
        "window_label": WINDOW_LABELS.get(plan.time_window, plan.time_window),
        "status": plan.status,
        "pause_reason": plan.pause_reason,
        "next_run_at": plan.next_run_at,
    }


def _article_dict(article: Article, include_html: bool = True) -> dict:
    payload = {
        "id": article.id,
        "title": article.title,
        "summary": article.summary,
        "status": article.status,
        "status_label": STATUS_LABELS.get(article.status, article.status),
        "failure_reason": article.failure_reason,
        "delivery_note": article.delivery_note,
        "draft_media_id": article.draft_media_id,
        "template_key": article.template_key,
        "style_key": article.style_key,
        "subtopic": article.subtopic,
        "column_type": article.column_type,
        "topic_tag": article.topic_tag,
        "regen_count": article.regen_count,
        "alt_titles": article.alt_titles or [],
        "created_at": article.created_at,
        "plan_id": article.plan_id,
        "wx_account_id": article.wx_account_id,
    }
    if include_html:
        payload["body_html"] = article.body_html
        payload["sources"] = article.sources or []
    return payload


def _jsonable(data: dict) -> dict:
    converted = {}
    for key, value in data.items():
        if isinstance(value, datetime):
            converted[key] = value.isoformat()
        else:
            converted[key] = value
    return converted


def _rng():
    import random

    return random.SystemRandom()
