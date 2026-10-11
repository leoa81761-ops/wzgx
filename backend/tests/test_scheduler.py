from datetime import datetime, timedelta, timezone

import pytest

from app.db import session_scope
from app.models import ArticleTask, User, WritingPlan
from app.services.quota import reserve
from app.services.schedule import ZeroRng, compute_next_run, should_retry
from app.services.scheduler import scan_once
from tests.conftest import add_task, make_account_plan

UTC = timezone.utc


def test_next_run_spreads_inside_window():
    from zoneinfo import ZoneInfo

    sh = ZoneInfo("Asia/Shanghai")
    morning = datetime(2026, 10, 12, 8, 0, tzinfo=sh)
    slot = compute_next_run(morning, schedule_type="daily", weekdays=[], window_key="morning", rng=ZeroRng())
    assert slot.astimezone(sh) == datetime(2026, 10, 12, 9, 0, tzinfo=sh)

    inside = datetime(2026, 10, 12, 10, 30, tzinfo=sh)
    slot = compute_next_run(inside, schedule_type="daily", weekdays=[], window_key="morning", rng=ZeroRng())
    assert slot.astimezone(sh) == datetime(2026, 10, 12, 10, 31, tzinfo=sh)

    after = datetime(2026, 10, 12, 13, 0, tzinfo=sh)
    slot = compute_next_run(after, schedule_type="daily", weekdays=[], window_key="morning", rng=ZeroRng())
    assert slot.astimezone(sh) == datetime(2026, 10, 13, 9, 0, tzinfo=sh)

    sunday = datetime(2026, 10, 11, 10, 0, tzinfo=sh)
    slot = compute_next_run(sunday, schedule_type="weekly", weekdays=[1], window_key="morning", rng=ZeroRng())
    assert slot.astimezone(sh).isoweekday() == 1
    assert slot.astimezone(sh) == datetime(2026, 10, 12, 9, 0, tzinfo=sh)

    night = datetime(2026, 10, 12, 22, 0, tzinfo=sh)
    slot = compute_next_run(night, schedule_type="daily", weekdays=[], window_key="night", rng=ZeroRng())
    assert slot.astimezone(sh) == datetime(2026, 10, 13, 0, 0, tzinfo=sh)


def test_retry_only_inside_window():
    end = datetime(2026, 10, 13, 6, 0, tzinfo=UTC)
    assert should_retry("schedule", 0, datetime(2026, 10, 13, 5, 0, tzinfo=UTC), end) is True
    assert should_retry("schedule", 2, datetime(2026, 10, 13, 5, 0, tzinfo=UTC), end) is False
    assert should_retry("manual", 0, datetime(2026, 10, 13, 5, 0, tzinfo=UTC), end) is False
    assert should_retry("schedule", 0, datetime(2026, 10, 13, 7, 0, tzinfo=UTC), end) is False


@pytest.mark.asyncio
async def test_scan_creates_one_task_and_is_idempotent(settings):
    now = datetime.now(UTC)
    await make_account_plan(settings, next_run=now - timedelta(minutes=1))
    first = await scan_once(settings, now)
    assert len(first) == 1
    second = await scan_once(settings, now + timedelta(minutes=1))
    assert second == []
    async with session_scope() as session:
        tasks = (await session.scalars(__import__("sqlalchemy").select(ArticleTask))).all()
        assert len(tasks) == 1
        assert tasks[0].status == "success"
        plan = await session.get(WritingPlan, tasks[0].plan_id)
        from app.services.schedule import as_utc

        assert as_utc(plan.next_run_at) > now


@pytest.mark.asyncio
async def test_future_plan_is_not_triggered(settings):
    now = datetime.now(UTC)
    await make_account_plan(settings, email="future@example.com", next_run=now + timedelta(days=2))
    claimed = await scan_once(settings, now)
    assert claimed == []


@pytest.mark.asyncio
async def test_quota_exhaustion_pauses_plan(settings):
    from app.services.schedule import shanghai_day

    now = datetime.now(UTC)
    user_id, _account_id, plan_id = await make_account_plan(
        settings, email="paused@example.com", next_run=now - timedelta(minutes=1)
    )
    task_id = await add_task(user_id, plan_id, _account_id, trigger="regenerate", day=shanghai_day(now), status="success", when=now)
    async with session_scope() as session:
        user = await session.get(User, user_id)
        await reserve(session, user, task_id, now)
    claimed = await scan_once(settings, now)
    assert claimed == []
    async with session_scope() as session:
        plan = await session.get(WritingPlan, plan_id)
        assert plan.status == "paused"
        assert plan.pause_reason == "额度已用完"
