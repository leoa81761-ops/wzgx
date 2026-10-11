import logging
from datetime import datetime, timedelta

from sqlalchemy import select
from sqlalchemy.exc import IntegrityError

from app.config import Settings
from app.db import dialect_name, session_scope
from app.models import ArticleTask, User, WritingPlan, WxAccount
from app.services.jobs import batch_taken, create_generation_task
from app.services.pipeline import execute_task
from app.services.quota import QuotaError, enforce_free_limits, quota_block_reason, release_hold
from app.services.schedule import UTC, as_utc, compute_next_run, shanghai_day, should_retry, tomorrow_start, window_end

logger = logging.getLogger(__name__)


async def scan_once(settings: Settings, now: datetime | None = None) -> list[int]:
    current = as_utc(now) or datetime.now(UTC)
    async with session_scope() as session:
        await _requeue_stale(session, current, settings.stale_task_seconds)
        await _resume_quota(session, current)
        users = (await session.scalars(select(User).where(User.is_active.is_(True)))).all()
        for user in users:
            await enforce_free_limits(session, user, current)
        plans = (
            await session.scalars(
                select(WritingPlan).where(WritingPlan.status == "active", WritingPlan.next_run_at.is_not(None), WritingPlan.next_run_at <= current)
            )
        ).all()
        grouped: dict[int, list[WritingPlan]] = {}
        for plan in plans:
            grouped.setdefault(plan.wx_account_id, []).append(plan)
        for group in grouped.values():
            ordered = sorted(group, key=lambda item: item.id)
            day_key = shanghai_day(current)
            if await batch_taken(session, ordered[0].wx_account_id, day_key):
                for item in ordered:
                    item.next_run_at = _next_from_tomorrow(item, current)
                continue
            # 同一公众号每天只跑 1 批；多个方向按日期轮换。
            chosen = ordered[int(day_key.replace("-", "")) % len(ordered)]
            await _handle_plan(session, chosen, current)
            for item in ordered:
                if item.id != chosen.id:
                    item.next_run_at = _next_from_tomorrow(item, current)
        claimed = await _claim_pending(session, current, settings.max_concurrency)
    for task_id in claimed:
        await execute_task(task_id, settings, claim=False, now=current)
    return claimed


async def _handle_plan(session, plan: WritingPlan, now: datetime) -> None:
    user = await session.get(User, plan.user_id)
    account = await session.get(WxAccount, plan.wx_account_id)
    if not user or not account:
        plan.status = "paused"
        plan.pause_reason = "授权失效"
        return
    if account.bind_status != "active":
        plan.status = "paused"
        plan.pause_reason = "授权失效" if account.bind_status == "auth_invalid" else "已解绑"
        return
    day_key = shanghai_day(now)
    existing = await session.scalar(
        select(ArticleTask).where(
            ArticleTask.plan_id == plan.id,
            ArticleTask.schedule_date == day_key,
            ArticleTask.trigger_type == "schedule",
        )
    )
    if existing:
        await _advance_existing(session, plan, existing, now)
        return
    if await batch_taken(session, account.id, day_key):
        plan.next_run_at = _next_from_tomorrow(plan, now)
        return
    try:
        async with session.begin_nested():
            await create_generation_task(
                session,
                user=user,
                plan=plan,
                account=account,
                trigger_type="schedule",
                now=now,
            )
    except IntegrityError:
        logger.info("plan %s already has a task for %s", plan.id, day_key)
        return
    except QuotaError as exc:
        if "额度" in exc.message:
            plan.status = "paused"
            plan.pause_reason = "额度已用完"
        elif "已生成一批" in exc.message:
            plan.next_run_at = _next_from_tomorrow(plan, now)
        else:
            logger.info("skip plan %s: %s", plan.id, exc.message)


async def _advance_existing(session, plan: WritingPlan, task: ArticleTask, now: datetime) -> None:
    if task.status == "success":
        plan.next_run_at = _next_from_tomorrow(plan, now)
        return
    if task.status in ("pending", "running"):
        return
    if task.status == "failed":
        end = window_end(task.schedule_date, plan.time_window)
        if should_retry(task.trigger_type, task.retry_count, now, end):
            task.status = "pending"
            task.retry_count += 1
            task.run_at = now
            return
        task.status = "abandoned"
        await release_hold(session, task.id, "超出时段放弃")
        plan.next_run_at = _next_from_tomorrow(plan, now)
        return
    if task.status == "abandoned":
        plan.next_run_at = _next_from_tomorrow(plan, now)


async def _resume_quota(session, now: datetime) -> None:
    plans = (
        await session.scalars(select(WritingPlan).where(WritingPlan.status == "paused", WritingPlan.pause_reason == "额度已用完"))
    ).all()
    for plan in plans:
        user = await session.get(User, plan.user_id)
        if user and await quota_block_reason(session, user, now) is None:
            plan.status = "active"
            plan.pause_reason = ""
            if plan.next_run_at is None or as_utc(plan.next_run_at) > now:
                plan.next_run_at = now


async def _requeue_stale(session, now: datetime, stale_seconds: int) -> None:
    cutoff = now - timedelta(seconds=stale_seconds)
    rows = (
        await session.scalars(select(ArticleTask).where(ArticleTask.status == "running", ArticleTask.started_at.is_not(None), ArticleTask.started_at < cutoff))
    ).all()
    for task in rows:
        task.status = "pending"
        task.run_at = now
        task.failure_reason = "运行超时，已重新入队"


async def _claim_pending(session, now: datetime, limit: int) -> list[int]:
    stmt = (
        select(ArticleTask)
        .where(ArticleTask.status == "pending", ArticleTask.run_at <= now)
        .order_by(ArticleTask.priority.desc(), ArticleTask.id.asc())
        .limit(limit)
    )
    if dialect_name == "postgresql":
        stmt = stmt.with_for_update(skip_locked=True)
    rows = (await session.scalars(stmt)).all()
    ids = []
    for task in rows:
        task.status = "running"
        task.started_at = now
        ids.append(task.id)
    await session.flush()
    return ids


def _next_from_tomorrow(plan: WritingPlan, now: datetime) -> datetime:
    import random

    return compute_next_run(
        tomorrow_start(now),
        schedule_type=plan.schedule_type,
        weekdays=list(plan.weekdays or []),
        window_key=plan.time_window,
        rng=random.SystemRandom(),
    )
