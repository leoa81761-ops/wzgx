from datetime import datetime, timedelta, timezone

import pytest
from fastapi.testclient import TestClient

from app.config import Settings
from app.db import dispose_db, init_db, session_scope
from app.main import create_app
from app.models import ArticleTask, User, WritingPlan, WxAccount
from app.security import encrypt_secret, hash_password
from app.seed import seed

UTC = timezone.utc


def _apply_env(monkeypatch, tmp_path):
    monkeypatch.setenv("DATABASE_URL", f"sqlite+aiosqlite:///{tmp_path}/app.db")
    monkeypatch.setenv("MASTER_KEY", "test-master-key")
    monkeypatch.setenv("JWT_SECRET", "test-jwt-secret")
    monkeypatch.setenv("SCHEDULER_ENABLED", "0")
    monkeypatch.setenv("LLM_MODE", "mock")
    monkeypatch.setenv("WECHAT_MODE", "mock")
    monkeypatch.setenv("ADMIN_EMAIL", "admin@example.com")
    monkeypatch.setenv("ADMIN_PASSWORD", "admin123456")


@pytest.fixture
def client(tmp_path, monkeypatch):
    _apply_env(monkeypatch, tmp_path)
    app = create_app(Settings())
    with TestClient(app) as test_client:
        yield test_client


@pytest.fixture
async def settings(tmp_path, monkeypatch):
    _apply_env(monkeypatch, tmp_path)
    cfg = Settings()
    await init_db(cfg.database_url)
    await seed(cfg)
    yield cfg
    await dispose_db()


async def make_account_plan(cfg: Settings, *, email="writer@example.com", tier="free", next_run=None, direction="职场成长"):
    now = datetime.now(UTC)
    async with session_scope() as session:
        user = User(email=email, password_hash=hash_password("password12"), tier=tier, tier_started_at=now)
        session.add(user)
        await session.flush()
        account = WxAccount(
            user_id=user.id,
            name="测试号",
            app_id=f"wx_mock_{user.id}",
            secret_encrypted=encrypt_secret(cfg.master_key, "mock-secret"),
            subject_type="personal",
            service_type="subscription",
            verified=False,
            delivery_mode="draft",
            bind_status="active",
            bound_at=now,
        )
        session.add(account)
        await session.flush()
        plan = WritingPlan(
            user_id=user.id,
            wx_account_id=account.id,
            direction=direction,
            mode="managed",
            style_key="warm",
            template_key="fresh",
            word_target=800,
            schedule_type="daily",
            weekdays=[],
            time_window="night",
            status="active",
            next_run_at=next_run if next_run is not None else now - timedelta(minutes=5),
        )
        session.add(plan)
        await session.flush()
        return user.id, account.id, plan.id


async def add_task(user_id, plan_id, account_id, *, trigger="manual", day="2026-10-11", status="success", when=None):
    when = when or datetime.now(UTC)
    async with session_scope() as session:
        task = ArticleTask(
            plan_id=plan_id,
            user_id=user_id,
            wx_account_id=account_id,
            idempotency_key=f"test-{trigger}-{day}-{status}-{when.timestamp()}",
            trigger_type=trigger,
            schedule_date=day,
            run_at=when,
            status=status,
            priority=10,
        )
        session.add(task)
        await session.flush()
        return task.id
