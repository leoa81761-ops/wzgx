from datetime import datetime, timedelta

from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.catalog import PLAN_DEFAULTS, TIER_LABELS
from app.models import PlanConfig, QuotaHold, QuotaLedger, User, WritingPlan, WxAccount
from app.services.schedule import as_utc, shanghai_day, shanghai_now

GRACE_DAYS = 7


class QuotaError(Exception):
    def __init__(self, message: str):
        super().__init__(message)
        self.message = message


def lifetime_period_key(start, today) -> str:
    years = today.year - start.year
    try:
        anniversary = start.replace(year=start.year + years)
    except ValueError:
        anniversary = start.replace(year=start.year + years, day=28)
    if today < anniversary:
        years -= 1
    return f"life:{start.isoformat()}:{max(years, 0)}"


def membership_state(user: User, now: datetime) -> tuple[str, str]:
    now = as_utc(now)
    tier = user.tier or "free"
    if tier == "free":
        return "free", "active"
    if tier == "lifetime":
        return "lifetime", "active"
    expires = as_utc(user.tier_expires_at)
    if expires is None or expires >= now:
        return tier, "active"
    if now <= expires + timedelta(days=GRACE_DAYS):
        return tier, "grace"
    return "free", "expired"


async def get_limits(session: AsyncSession, tier: str) -> PlanConfig:
    row = await session.get(PlanConfig, tier)
    if row:
        return row
    fallback = next(item for item in PLAN_DEFAULTS if item["tier"] == tier)
    return PlanConfig(**fallback)


def period_key_for(user: User, tier: str, now: datetime, unit: str) -> str:
    local = shanghai_now(now)
    if tier == "lifetime" or unit == "year":
        started = as_utc(user.tier_started_at) or as_utc(user.created_at) or now
        return lifetime_period_key(started.astimezone(shanghai_now(now).tzinfo).date(), local.date())
    return local.strftime("%Y-%m")


async def usage_counts(session: AsyncSession, user_id: int, day_key: str, period_key: str) -> tuple[int, int]:
    daily = await session.scalar(
        select(func.coalesce(func.sum(QuotaHold.amount), 0)).where(
            QuotaHold.user_id == user_id,
            QuotaHold.day_key == day_key,
            QuotaHold.state.in_(("held", "confirmed")),
            QuotaHold.amount > 0,
        )
    )
    period = await session.scalar(
        select(func.coalesce(func.sum(QuotaHold.amount), 0)).where(
            QuotaHold.user_id == user_id,
            QuotaHold.period_key == period_key,
            QuotaHold.state.in_(("held", "confirmed")),
            QuotaHold.amount > 0,
        )
    )
    return int(daily or 0), int(period or 0)


async def quota_block_reason(session: AsyncSession, user: User, now: datetime) -> str | None:
    tier, _state = membership_state(user, now)
    limits = await get_limits(session, tier)
    day_key = shanghai_day(now)
    period = period_key_for(user, tier, now, limits.period_unit)
    daily_used, period_used = await usage_counts(session, user.id, day_key, period)
    if daily_used >= limits.daily_quota:
        return "今日额度已用完"
    if period_used >= limits.period_quota:
        unit = "本周年" if limits.period_unit == "year" else "本月"
        return f"{unit}额度已用完"
    return None


async def reserve(session: AsyncSession, user: User, task_id: int, now: datetime, *, free: bool = False, note: str = "") -> None:
    tier, _state = membership_state(user, now)
    limits = await get_limits(session, tier)
    day_key = shanghai_day(now)
    period = period_key_for(user, tier, now, limits.period_unit)
    if free:
        session.add(
            QuotaHold(
                user_id=user.id,
                task_id=task_id,
                day_key=day_key,
                period_key=period,
                amount=0,
                state="confirmed",
            )
        )
        session.add(
            QuotaLedger(
                user_id=user.id,
                change_type="free_regen",
                amount=0,
                task_id=task_id,
                note=note or "首次重写不占额度",
            )
        )
        return
    reason = await quota_block_reason(session, user, now)
    if reason:
        raise QuotaError(reason)
    session.add(
        QuotaHold(
            user_id=user.id,
            task_id=task_id,
            day_key=day_key,
            period_key=period,
            amount=1,
            state="held",
        )
    )
    session.add(QuotaLedger(user_id=user.id, change_type="reserve", amount=1, task_id=task_id, note=note or "预占额度"))


async def confirm_hold(session: AsyncSession, task_id: int) -> None:
    hold = await session.scalar(select(QuotaHold).where(QuotaHold.task_id == task_id))
    if not hold or hold.state != "held":
        return
    hold.state = "confirmed"
    session.add(QuotaLedger(user_id=hold.user_id, change_type="confirm", amount=hold.amount, task_id=task_id, note="确认扣除"))


async def release_hold(session: AsyncSession, task_id: int, note: str = "失败退回") -> None:
    hold = await session.scalar(select(QuotaHold).where(QuotaHold.task_id == task_id))
    if not hold or hold.state != "held":
        return
    hold.state = "released"
    session.add(QuotaLedger(user_id=hold.user_id, change_type="release", amount=hold.amount, task_id=task_id, note=note))


async def quota_snapshot(session: AsyncSession, user: User, now: datetime) -> dict:
    tier, state = membership_state(user, now)
    limits = await get_limits(session, tier)
    day_key = shanghai_day(now)
    period = period_key_for(user, tier, now, limits.period_unit)
    daily_used, period_used = await usage_counts(session, user.id, day_key, period)
    state_label = {"active": "有效", "grace": "宽限期", "expired": "已降为免费"}.get(state, state)
    return {
        "tier": tier,
        "tier_label": TIER_LABELS.get(tier, tier),
        "stored_tier": user.tier,
        "state": state,
        "state_label": state_label,
        "expires_at": user.tier_expires_at,
        "daily_used": daily_used,
        "daily_limit": limits.daily_quota,
        "period_used": period_used,
        "period_limit": limits.period_quota,
        "period_unit": limits.period_unit,
        "period_label": "本周年" if limits.period_unit == "year" else "本月",
        "max_words": limits.max_words,
        "max_accounts": limits.max_accounts,
        "max_plans": limits.max_plans,
        "summary": limits.summary,
    }


async def enforce_free_limits(session: AsyncSession, user: User, now: datetime) -> None:
    tier, state = membership_state(user, now)
    if not (tier == "free" and state == "expired") and not (user.tier == "free"):
        # 宽限期内不暂停；当前仍是付费权益时不暂停。
        if tier != "free":
            return
    if tier != "free":
        return
    limits = await get_limits(session, "free")
    accounts = (
        await session.scalars(
            select(WxAccount)
            .where(WxAccount.user_id == user.id, WxAccount.bind_status == "active")
            .order_by(WxAccount.id.asc())
        )
    ).all()
    kept_accounts = {item.id for item in accounts[: limits.max_accounts]}
    plans = (
        await session.scalars(
            select(WritingPlan)
            .where(WritingPlan.user_id == user.id, WritingPlan.status != "deleted")
            .order_by(WritingPlan.id.asc())
        )
    ).all()
    active_kept = 0
    for plan in plans:
        if plan.wx_account_id not in kept_accounts or active_kept >= limits.max_plans:
            if plan.status == "active":
                plan.status = "paused"
                plan.pause_reason = "会员降级"
        elif plan.status == "paused" and plan.pause_reason == "会员降级":
            plan.status = "active"
            plan.pause_reason = ""
            active_kept += 1
        elif plan.status == "active":
            active_kept += 1


def priority_for(tier: str, trigger_type: str) -> int:
    base = {"premium": 30, "lifetime": 30, "standard": 20, "free": 10}.get(tier, 10)
    if trigger_type == "manual":
        base += 5
    if trigger_type == "regenerate":
        base += 6
    return base
