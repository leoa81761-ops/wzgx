from datetime import datetime

from sqlalchemy import (
    JSON,
    Boolean,
    DateTime,
    ForeignKey,
    Index,
    Integer,
    String,
    Text,
    UniqueConstraint,
    text,
)
from sqlalchemy.orm import DeclarativeBase, Mapped, mapped_column


class Base(DeclarativeBase):
    pass


def utcnow() -> datetime:
    from datetime import timezone

    return datetime.now(timezone.utc)


class User(Base):
    __tablename__ = "users"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    email: Mapped[str | None] = mapped_column(String(255), unique=True, nullable=True)
    phone: Mapped[str | None] = mapped_column(String(32), unique=True, nullable=True)
    password_hash: Mapped[str] = mapped_column(String(255))
    tier: Mapped[str] = mapped_column(String(32), default="free")
    tier_expires_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    tier_started_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    is_admin: Mapped[bool] = mapped_column(Boolean, default=False)
    is_active: Mapped[bool] = mapped_column(Boolean, default=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)


class WxAccount(Base):
    __tablename__ = "wx_account"
    __table_args__ = (
        Index(
            "uq_wx_app_active",
            "app_id",
            unique=True,
            sqlite_where=text("bind_status = 'active'"),
            postgresql_where=text("bind_status = 'active'"),
        ),
    )

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id"), index=True)
    name: Mapped[str] = mapped_column(String(80), default="")
    app_id: Mapped[str] = mapped_column(String(64), index=True)
    secret_encrypted: Mapped[str] = mapped_column(Text, default="")
    subject_type: Mapped[str] = mapped_column(String(32), default="personal")
    service_type: Mapped[str] = mapped_column(String(32), default="subscription")
    verified: Mapped[bool] = mapped_column(Boolean, default=False)
    delivery_mode: Mapped[str] = mapped_column(String(32), default="draft")
    bind_status: Mapped[str] = mapped_column(String(32), default="active")
    permission_report: Mapped[dict | None] = mapped_column(JSON, nullable=True)
    bound_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    unbound_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)


class WritingPlan(Base):
    __tablename__ = "writing_plan"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id"), index=True)
    wx_account_id: Mapped[int] = mapped_column(ForeignKey("wx_account.id"), index=True)
    direction: Mapped[str] = mapped_column(String(80))
    mode: Mapped[str] = mapped_column(String(32), default="managed")
    style_key: Mapped[str] = mapped_column(String(32), default="warm")
    template_key: Mapped[str] = mapped_column(String(32), default="simple")
    word_target: Mapped[int] = mapped_column(Integer, default=800)
    schedule_type: Mapped[str] = mapped_column(String(16), default="daily")
    weekdays: Mapped[list | None] = mapped_column(JSON, nullable=True)
    time_window: Mapped[str] = mapped_column(String(32), default="night")
    status: Mapped[str] = mapped_column(String(32), default="active")
    pause_reason: Mapped[str] = mapped_column(String(80), default="")
    next_run_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)


class ArticleTask(Base):
    __tablename__ = "article_task"
    __table_args__ = (UniqueConstraint("idempotency_key", name="uq_task_idem"),)

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    plan_id: Mapped[int] = mapped_column(ForeignKey("writing_plan.id"), index=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id"), index=True)
    wx_account_id: Mapped[int] = mapped_column(ForeignKey("wx_account.id"), index=True)
    article_id: Mapped[int | None] = mapped_column(Integer, nullable=True)
    regen_of_article_id: Mapped[int | None] = mapped_column(Integer, nullable=True)
    idempotency_key: Mapped[str] = mapped_column(String(160))
    trigger_type: Mapped[str] = mapped_column(String(32), default="schedule")
    schedule_date: Mapped[str] = mapped_column(String(16), default="")
    run_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), index=True)
    status: Mapped[str] = mapped_column(String(32), default="pending", index=True)
    failure_reason: Mapped[str] = mapped_column(Text, default="")
    retry_count: Mapped[int] = mapped_column(Integer, default=0)
    priority: Mapped[int] = mapped_column(Integer, default=0)
    started_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    finished_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    duration_ms: Mapped[int] = mapped_column(Integer, default=0)
    tokens: Mapped[int] = mapped_column(Integer, default=0)
    model_trace: Mapped[list | None] = mapped_column(JSON, nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)


class Article(Base):
    __tablename__ = "article"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    task_id: Mapped[int] = mapped_column(ForeignKey("article_task.id"), index=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id"), index=True)
    plan_id: Mapped[int] = mapped_column(ForeignKey("writing_plan.id"), index=True)
    wx_account_id: Mapped[int] = mapped_column(ForeignKey("wx_account.id"), index=True)
    title: Mapped[str] = mapped_column(String(200), default="")
    alt_titles: Mapped[list | None] = mapped_column(JSON, nullable=True)
    summary: Mapped[str] = mapped_column(Text, default="")
    body_json: Mapped[dict | None] = mapped_column(JSON, nullable=True)
    body_html: Mapped[str] = mapped_column(Text, default="")
    sources: Mapped[list | None] = mapped_column(JSON, nullable=True)
    thumb_media_id: Mapped[str] = mapped_column(String(128), default="")
    draft_media_id: Mapped[str] = mapped_column(String(128), default="")
    template_key: Mapped[str] = mapped_column(String(32), default="simple")
    template_version: Mapped[str] = mapped_column(String(16), default="1")
    style_key: Mapped[str] = mapped_column(String(32), default="warm")
    prompt_version: Mapped[str] = mapped_column(String(32), default="p0.1")
    risk_level: Mapped[str] = mapped_column(String(16), default="low")
    status: Mapped[str] = mapped_column(String(32), default="generating", index=True)
    failure_reason: Mapped[str] = mapped_column(Text, default="")
    delivery_note: Mapped[str] = mapped_column(Text, default="")
    regen_count: Mapped[int] = mapped_column(Integer, default=0)
    subtopic: Mapped[str] = mapped_column(String(80), default="")
    topic_tag: Mapped[str] = mapped_column(String(200), default="")
    column_type: Mapped[str] = mapped_column(String(40), default="")
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)


class ArticleSource(Base):
    __tablename__ = "article_source"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    article_id: Mapped[int] = mapped_column(ForeignKey("article.id"), index=True)
    seq: Mapped[int] = mapped_column(Integer, default=1)
    source_type: Mapped[str] = mapped_column(String(32), default="other")
    name: Mapped[str] = mapped_column(String(200), default="")
    title: Mapped[str] = mapped_column(String(300), default="")
    publisher: Mapped[str] = mapped_column(String(200), default="")
    doc_no: Mapped[str] = mapped_column(String(120), default="")
    published_on: Mapped[str] = mapped_column(String(32), default="")
    raw_url: Mapped[str] = mapped_column(Text, default="")
    display_url: Mapped[str] = mapped_column(Text, default="")
    accessible: Mapped[str] = mapped_column(String(32), default="未校验")


class TopicCoverage(Base):
    __tablename__ = "topic_coverage"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    plan_id: Mapped[int] = mapped_column(ForeignKey("writing_plan.id"), index=True)
    subtopic: Mapped[str] = mapped_column(String(80))
    column_type: Mapped[str] = mapped_column(String(40), default="")
    angle: Mapped[str] = mapped_column(String(80), default="")
    topic_tag: Mapped[str] = mapped_column(String(200), index=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)


class QuotaHold(Base):
    __tablename__ = "quota_hold"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id"), index=True)
    task_id: Mapped[int] = mapped_column(ForeignKey("article_task.id"), unique=True)
    day_key: Mapped[str] = mapped_column(String(16), index=True)
    period_key: Mapped[str] = mapped_column(String(64), index=True)
    amount: Mapped[int] = mapped_column(Integer, default=1)
    state: Mapped[str] = mapped_column(String(16), default="held")
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)


class QuotaLedger(Base):
    __tablename__ = "quota_ledger"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id"), index=True)
    change_type: Mapped[str] = mapped_column(String(32))
    amount: Mapped[int] = mapped_column(Integer, default=0)
    task_id: Mapped[int | None] = mapped_column(Integer, nullable=True)
    note: Mapped[str] = mapped_column(String(200), default="")
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)


class LegalDoc(Base):
    __tablename__ = "legal_doc"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    doc_type: Mapped[str] = mapped_column(String(64), index=True)
    version: Mapped[str] = mapped_column(String(32))
    title: Mapped[str] = mapped_column(String(120))
    content: Mapped[str] = mapped_column(Text)
    status: Mapped[str] = mapped_column(String(16), default="active")
    effective_date: Mapped[str] = mapped_column(String(16), default="2026-10-11")


class ConsentLog(Base):
    __tablename__ = "consent_log"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id"), index=True)
    doc_type: Mapped[str] = mapped_column(String(64))
    version: Mapped[str] = mapped_column(String(32))
    consent_item: Mapped[str] = mapped_column(String(64))
    ip: Mapped[str] = mapped_column(String(64), default="")
    user_agent: Mapped[str] = mapped_column(String(256), default="")
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)
    withdrawn_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)


class ServiceConfig(Base):
    """文本模型供应商，以及微信 / 审核等服务配置。对应开发书 service_config。"""

    __tablename__ = "service_config"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    config_type: Mapped[str] = mapped_column(String(32), index=True)
    name: Mapped[str] = mapped_column(String(80), default="")
    protocol: Mapped[str] = mapped_column(String(32), default="")
    base_url: Mapped[str] = mapped_column(Text, default="")
    api_key_encrypted: Mapped[str] = mapped_column(Text, default="")
    model_name: Mapped[str] = mapped_column(String(120), default="")
    extra_headers: Mapped[dict | None] = mapped_column(JSON, nullable=True)
    extra_params: Mapped[dict | None] = mapped_column(JSON, nullable=True)
    priority: Mapped[int] = mapped_column(Integer, default=100)
    enabled: Mapped[bool] = mapped_column(Boolean, default=True)
    timeout_seconds: Mapped[int] = mapped_column(Integer, default=60)
    updated_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)


class StageRoute(Base):
    __tablename__ = "stage_route"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    stage: Mapped[str] = mapped_column(String(32), index=True)
    tier: Mapped[str] = mapped_column(String(32), default="*")
    service_config_id: Mapped[int] = mapped_column(ForeignKey("service_config.id"))
    sort_order: Mapped[int] = mapped_column(Integer, default=0)


class PlanConfig(Base):
    __tablename__ = "plan_config"

    tier: Mapped[str] = mapped_column(String(32), primary_key=True)
    name: Mapped[str] = mapped_column(String(40))
    price_label: Mapped[str] = mapped_column(String(120), default="")
    daily_quota: Mapped[int] = mapped_column(Integer, default=1)
    period_quota: Mapped[int] = mapped_column(Integer, default=10)
    period_unit: Mapped[str] = mapped_column(String(16), default="month")
    max_accounts: Mapped[int] = mapped_column(Integer, default=1)
    max_plans: Mapped[int] = mapped_column(Integer, default=1)
    max_words: Mapped[int] = mapped_column(Integer, default=1000)
    summary: Mapped[str] = mapped_column(Text, default="")


class StylePreset(Base):
    __tablename__ = "style_preset"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    key: Mapped[str] = mapped_column(String(32), unique=True)
    name: Mapped[str] = mapped_column(String(40))
    description: Mapped[str] = mapped_column(Text, default="")
    enabled: Mapped[bool] = mapped_column(Boolean, default=True)


class LayoutTemplate(Base):
    __tablename__ = "layout_template"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    key: Mapped[str] = mapped_column(String(32), unique=True)
    name: Mapped[str] = mapped_column(String(40))
    description: Mapped[str] = mapped_column(Text, default="")
    version: Mapped[str] = mapped_column(String(16), default="1")
    enabled: Mapped[bool] = mapped_column(Boolean, default=True)


class Entitlement(Base):
    __tablename__ = "entitlement"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id"), index=True)
    source: Mapped[str] = mapped_column(String(32), default="admin")
    tier: Mapped[str] = mapped_column(String(32))
    expires_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    admin_id: Mapped[int | None] = mapped_column(Integer, nullable=True)
    note: Mapped[str] = mapped_column(Text, default="")
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)


class AuditLog(Base):
    __tablename__ = "audit_log"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    actor_id: Mapped[int | None] = mapped_column(Integer, nullable=True)
    action: Mapped[str] = mapped_column(String(64))
    target: Mapped[str] = mapped_column(String(120), default="")
    detail: Mapped[str] = mapped_column(Text, default="")
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), default=utcnow)
