from datetime import datetime, timezone

from sqlalchemy import select

from app.catalog import LEGAL_VERSION, PLAN_DEFAULTS, STYLES, TEMPLATES
from app.config import Settings
from app.db import session_scope
from app.legal_texts import DOCS
from app.models import LayoutTemplate, LegalDoc, PlanConfig, ServiceConfig, StylePreset, User
from app.security import hash_password


async def seed(settings: Settings) -> None:
    async with session_scope() as session:
        for item in PLAN_DEFAULTS:
            if await session.get(PlanConfig, item["tier"]) is None:
                session.add(PlanConfig(**item))
        for item in STYLES:
            exists = await session.scalar(select(StylePreset).where(StylePreset.key == item["key"]))
            if exists is None:
                session.add(StylePreset(key=item["key"], name=item["name"], description=item["description"]))
        for item in TEMPLATES:
            exists = await session.scalar(select(LayoutTemplate).where(LayoutTemplate.key == item["key"]))
            if exists is None:
                session.add(LayoutTemplate(key=item["key"], name=item["name"], description=item["description"]))
        for doc_type, title, content in DOCS:
            exists = await session.scalar(
                select(LegalDoc).where(LegalDoc.doc_type == doc_type, LegalDoc.version == LEGAL_VERSION)
            )
            if exists is None:
                session.add(
                    LegalDoc(
                        doc_type=doc_type,
                        version=LEGAL_VERSION,
                        title=title,
                        content=content.strip(),
                        status="active",
                        effective_date="2026-10-11",
                    )
                )
        wechat = await session.scalar(
            select(ServiceConfig).where(ServiceConfig.config_type == "wechat", ServiceConfig.name == "wechat")
        )
        if wechat is None:
            session.add(
                ServiceConfig(
                    config_type="wechat",
                    name="wechat",
                    protocol="wechat",
                    base_url=settings.wechat_api_base,
                    extra_params={"mode": settings.wechat_mode, "public_egress_ip": settings.public_egress_ip},
                    enabled=True,
                    updated_at=datetime.now(timezone.utc),
                )
            )
        admin = await session.scalar(select(User).where(User.email == settings.admin_email.lower()))
        if admin is None:
            session.add(
                User(
                    email=settings.admin_email.lower(),
                    password_hash=hash_password(settings.admin_password),
                    tier="lifetime",
                    tier_started_at=datetime.now(timezone.utc),
                    is_admin=True,
                )
            )
