from datetime import datetime, timedelta, timezone

import pytest

from app.db import session_scope
from app.models import User
from app.services.quota import QuotaError, membership_state, quota_block_reason, release_hold, reserve

UTC = timezone.utc


@pytest.mark.asyncio
async def test_daily_and_monthly_quota(settings):
    now = datetime.now(UTC)
    async with session_scope() as session:
        user = User(email="quota@example.com", password_hash="x", tier="free")
        session.add(user)
        await session.flush()
        await reserve(session, user, await _task(session, user.id), now)
        reason = await quota_block_reason(session, user, now)
        assert reason == "今日额度已用完"
        with pytest.raises(QuotaError):
            await reserve(session, user, await _task(session, user.id), now)


@pytest.mark.asyncio
async def test_release_allows_another_reserve(settings):
    now = datetime.now(UTC)
    async with session_scope() as session:
        user = User(email="release@example.com", password_hash="x", tier="free")
        session.add(user)
        await session.flush()
        task_id = await _task(session, user.id)
        await reserve(session, user, task_id, now)
        await release_hold(session, task_id)
        await reserve(session, user, await _task(session, user.id), now)
        assert await quota_block_reason(session, user, now) == "今日额度已用完"


@pytest.mark.asyncio
async def test_first_regen_does_not_consume_quota(settings):
    now = datetime.now(UTC)
    async with session_scope() as session:
        user = User(email="regen@example.com", password_hash="x", tier="free")
        session.add(user)
        await session.flush()
        await reserve(session, user, await _task(session, user.id), now, free=True, note="首次重写")
        assert await quota_block_reason(session, user, now) is None


@pytest.mark.asyncio
async def test_monthly_cap(settings):
    start = datetime(2026, 10, 1, 1, 0, tzinfo=UTC)
    async with session_scope() as session:
        user = User(email="month@example.com", password_hash="x", tier="free")
        session.add(user)
        await session.flush()
        for offset in range(10):
            moment = start + timedelta(days=offset)
            await reserve(session, user, await _task(session, user.id), moment)
        blocked = await quota_block_reason(session, user, start + timedelta(days=20))
        assert blocked == "本月额度已用完"


def test_grace_then_free():
    now = datetime(2026, 10, 11, tzinfo=UTC)
    user = User(email="m@example.com", password_hash="x", tier="standard", tier_expires_at=now - timedelta(days=3))
    tier, state = membership_state(user, now)
    assert tier == "standard"
    assert state == "grace"
    tier, state = membership_state(user, user.tier_expires_at + timedelta(days=8))
    assert (tier, state) == ("free", "expired")


async def _task(session, user_id: int) -> int:
    import uuid

    from app.models import ArticleTask, WritingPlan, WxAccount

    account = WxAccount(
        user_id=user_id,
        name="号",
        app_id=f"wx_mock_{uuid.uuid4().hex[:12]}",
        secret_encrypted="x",
        bind_status="active",
    )
    session.add(account)
    await session.flush()
    plan = WritingPlan(
        user_id=user_id,
        wx_account_id=account.id,
        direction="职场成长",
        style_key="warm",
        template_key="simple",
        time_window="night",
        status="active",
    )
    session.add(plan)
    await session.flush()
    task = ArticleTask(
        plan_id=plan.id,
        user_id=user_id,
        wx_account_id=account.id,
        idempotency_key=f"q-{uuid.uuid4().hex}",
        trigger_type="manual",
        schedule_date="2026-10-01",
        run_at=datetime.now(UTC),
        status="success",
    )
    session.add(task)
    await session.flush()
    return task.id
