from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.catalog import DEFAULT_SENSITIVE_WORDS
from app.config import Settings
from app.models import ServiceConfig, StageRoute
from app.security import decrypt_secret
from app.services.audit import effective_words
from app.services.llm import ProviderConfig

BUILTIN_PROVIDER = ProviderConfig(
    id=0,
    name="内置模拟",
    protocol="openai",
    base_url="mock://local",
    api_key="",
    model="mock-writer",
    extra_headers={},
    extra_params={},
    timeout=30,
)


def to_provider(row: ServiceConfig, master_key: str) -> ProviderConfig:
    secret = ""
    if row.api_key_encrypted:
        secret = decrypt_secret(master_key, row.api_key_encrypted)
    return ProviderConfig(
        id=row.id,
        name=row.name,
        protocol=row.protocol,
        base_url=row.base_url,
        api_key=secret,
        model=row.model_name,
        extra_headers=row.extra_headers or {},
        extra_params=row.extra_params or {},
        timeout=float(row.timeout_seconds or 60),
    )


async def resolve_providers(session: AsyncSession, settings: Settings, stage: str, tier: str) -> list[ProviderConfig]:
    chosen = await _routes(session, stage, tier, settings.master_key)
    if not chosen:
        chosen = await _routes(session, stage, "*", settings.master_key)
    if chosen:
        return chosen
    rows = (
        await session.scalars(
            select(ServiceConfig)
            .where(ServiceConfig.config_type == "text_model", ServiceConfig.enabled.is_(True))
            .order_by(ServiceConfig.priority.asc(), ServiceConfig.id.asc())
        )
    ).all()
    providers = [to_provider(row, settings.master_key) for row in rows]
    if providers:
        return providers
    if settings.llm_mode == "mock":
        return [BUILTIN_PROVIDER]
    return []


async def _routes(session: AsyncSession, stage: str, tier: str, master_key: str) -> list[ProviderConfig]:
    routes = (
        await session.scalars(
            select(StageRoute)
            .where(StageRoute.stage == stage, StageRoute.tier == tier)
            .order_by(StageRoute.sort_order.asc(), StageRoute.id.asc())
        )
    ).all()
    if not routes:
        return []
    providers = []
    for route in routes:
        row = await session.get(ServiceConfig, route.service_config_id)
        if row and row.enabled and row.config_type == "text_model":
            providers.append(to_provider(row, master_key))
    return providers


async def load_sensitive_words(session: AsyncSession) -> list[str]:
    row = await session.scalar(
        select(ServiceConfig).where(ServiceConfig.config_type == "audit", ServiceConfig.name == "local_words")
    )
    extra = []
    if row and isinstance(row.extra_params, dict):
        extra = row.extra_params.get("words") or []
    return effective_words(extra if isinstance(extra, list) else DEFAULT_SENSITIVE_WORDS)


async def load_wechat_runtime(session: AsyncSession, settings: Settings) -> dict:
    row = await session.scalar(select(ServiceConfig).where(ServiceConfig.config_type == "wechat", ServiceConfig.name == "wechat"))
    if not row:
        return {
            "mode": settings.wechat_mode,
            "api_base": settings.wechat_api_base,
            "public_egress_ip": settings.public_egress_ip,
        }
    extra = row.extra_params or {}
    return {
        "mode": extra.get("mode") or settings.wechat_mode,
        "api_base": row.base_url or settings.wechat_api_base,
        "public_egress_ip": extra.get("public_egress_ip") or settings.public_egress_ip,
    }
