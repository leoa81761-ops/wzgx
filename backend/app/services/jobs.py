import uuid
from datetime import datetime

from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.models import Article, ArticleTask, User, WritingPlan, WxAccount
from app.services.quota import QuotaError, membership_state, priority_for, reserve
from app.services.schedule import shanghai_day


async def batch_taken(session: AsyncSession, account_id: int, day_key: str) -> bool:
    count = await session.scalar(
        select(func.count())
        .select_from(ArticleTask)
        .where(
            ArticleTask.wx_account_id == account_id,
            ArticleTask.schedule_date == day_key,
            ArticleTask.trigger_type.in_(("schedule", "manual")),
            ArticleTask.status.in_(("pending", "running", "success")),
        )
    )
    return int(count or 0) > 0


async def create_generation_task(
    session: AsyncSession,
    *,
    user: User,
    plan: WritingPlan,
    account: WxAccount,
    trigger_type: str,
    now: datetime,
    regen_article: Article | None = None,
) -> ArticleTask:
    if account.bind_status != "active":
        raise QuotaError("公众号当前不可用，请重新检测或重新绑定")
    if plan.status != "active":
        raise QuotaError(plan.pause_reason or "写作计划已暂停")
    day_key = shanghai_day(now)
    free = False
    if trigger_type == "regenerate":
        if regen_article is None:
            raise QuotaError("找不到要重写的文章")
        free = regen_article.regen_count <= 0
        idem = f"regen:{regen_article.id}:{uuid.uuid4().hex}"
    else:
        if await batch_taken(session, account.id, day_key):
            raise QuotaError("该公众号今日已生成一批（每日最多 1 批）")
        if trigger_type == "schedule":
            idem = f"schedule:{plan.id}:{day_key}"
        else:
            idem = f"manual:{plan.id}:{uuid.uuid4().hex}"
    tier, _state = membership_state(user, now)
    task = ArticleTask(
        plan_id=plan.id,
        user_id=user.id,
        wx_account_id=account.id,
        regen_of_article_id=regen_article.id if regen_article else None,
        idempotency_key=idem,
        trigger_type=trigger_type,
        schedule_date=day_key,
        run_at=now,
        status="pending",
        priority=priority_for(tier, trigger_type),
    )
    session.add(task)
    await session.flush()
    note = "定时生成" if trigger_type == "schedule" else "立即生成" if trigger_type == "manual" else "重新生成"
    await reserve(session, user, task.id, now, free=free, note=note)
    return task
