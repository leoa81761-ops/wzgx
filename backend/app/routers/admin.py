from datetime import datetime, timezone
from urllib.parse import urlparse

from fastapi import APIRouter, Request
from sqlalchemy import delete, select

from app.catalog import STAGES, TIER_LABELS
from app.db import session_scope
from app.deps import admin_user
from app.errors import AppError
from app.models import AuditLog, Entitlement, ServiceConfig, StageRoute, User
from app.schemas import ProviderIn, ProviderTestIn, RouteIn, SensitiveIn, TierIn, WeChatSettingsIn
from app.security import decrypt_secret, encrypt_secret, mask_secret
from app.services.audit import effective_words
from app.services.llm import AnthropicMessagesAdapter, OpenAIChatAdapter, ProviderConfig, ProviderError
from app.services.providers import load_sensitive_words, to_provider
from app.services.schedule import as_utc

router = APIRouter(prefix="/api/admin", tags=["admin"])


@router.get("/users")
async def users(request: Request):
    await admin_user(request)
    async with session_scope() as session:
        rows = (await session.scalars(select(User).order_by(User.id.desc()).limit(200))).all()
        return [
            {
                "id": row.id,
                "account": row.email or row.phone,
                "tier": row.tier,
                "tier_label": TIER_LABELS.get(row.tier, row.tier),
                "tier_expires_at": row.tier_expires_at,
                "is_admin": row.is_admin,
                "created_at": row.created_at,
            }
            for row in rows
        ]


@router.put("/users/{user_id}/tier")
async def set_tier(user_id: int, body: TierIn, request: Request):
    admin = await admin_user(request)
    if body.tier not in TIER_LABELS:
        raise AppError("未知会员档位")
    now = datetime.now(timezone.utc)
    expires = as_utc(body.expires_at)
    if body.tier in ("standard", "premium") and expires is None:
        expires = now.replace(microsecond=0)
        from datetime import timedelta

        expires = expires + timedelta(days=31)
    if body.tier in ("free", "lifetime"):
        expires = None
    async with session_scope() as session:
        user = await session.get(User, user_id)
        if user is None:
            raise AppError("找不到用户", 404)
        user.tier = body.tier
        user.tier_expires_at = expires
        if body.tier == "lifetime":
            user.tier_started_at = now
        session.add(
            Entitlement(
                user_id=user.id,
                source="admin",
                tier=body.tier,
                expires_at=expires,
                admin_id=admin.id,
                note=body.note.strip()[:500],
            )
        )
        session.add(
            AuditLog(
                actor_id=admin.id,
                action="set_tier",
                target=f"user:{user.id}",
                detail=f"档位改为{TIER_LABELS[body.tier]}。备注：{body.note.strip()[:120]}",
            )
        )
        return {"ok": True, "tier": user.tier, "tier_expires_at": user.tier_expires_at}


@router.get("/providers")
async def list_providers(request: Request):
    admin = await admin_user(request)
    settings = request.app.state.settings
    async with session_scope() as session:
        rows = (
            await session.scalars(
                select(ServiceConfig)
                .where(ServiceConfig.config_type == "text_model")
                .order_by(ServiceConfig.priority.asc(), ServiceConfig.id.asc())
            )
        ).all()
        return [_provider_dict(row, settings.master_key) for row in rows]


@router.post("/providers")
async def create_provider(body: ProviderIn, request: Request):
    admin = await admin_user(request)
    _validate_provider(body)
    settings = request.app.state.settings
    async with session_scope() as session:
        row = ServiceConfig(
            config_type="text_model",
            name=body.name.strip(),
            protocol=body.protocol,
            base_url=body.base_url.strip(),
            api_key_encrypted=encrypt_secret(settings.master_key, body.api_key.strip()),
            model_name=body.model.strip(),
            extra_headers=body.extra_headers,
            extra_params=body.extra_params,
            priority=body.priority,
            enabled=body.enabled,
            timeout_seconds=body.timeout_seconds,
            updated_at=datetime.now(timezone.utc),
        )
        session.add(row)
        await session.flush()
        session.add(
            AuditLog(
                actor_id=admin.id,
                action="provider_create",
                target=f"service_config:{row.id}",
                detail=f"新增模型服务 {row.name}，协议 {row.protocol}，模型 {row.model_name}",
            )
        )
        return _provider_dict(row, settings.master_key)


@router.put("/providers/{provider_id}")
async def update_provider(provider_id: int, body: ProviderIn, request: Request):
    admin = await admin_user(request)
    _validate_provider(body)
    settings = request.app.state.settings
    async with session_scope() as session:
        row = await _model_row(session, provider_id)
        row.name = body.name.strip()
        row.protocol = body.protocol
        row.base_url = body.base_url.strip()
        if body.api_key.strip():
            row.api_key_encrypted = encrypt_secret(settings.master_key, body.api_key.strip())
        row.model_name = body.model.strip()
        row.extra_headers = body.extra_headers
        row.extra_params = body.extra_params
        row.priority = body.priority
        row.enabled = body.enabled
        row.timeout_seconds = body.timeout_seconds
        row.updated_at = datetime.now(timezone.utc)
        session.add(
            AuditLog(
                actor_id=admin.id,
                action="provider_update",
                target=f"service_config:{row.id}",
                detail=f"更新模型服务 {row.name}，协议 {row.protocol}" + ("，已更新密钥" if body.api_key.strip() else ""),
            )
        )
        return _provider_dict(row, settings.master_key)


@router.delete("/providers/{provider_id}")
async def delete_provider(provider_id: int, request: Request):
    admin = await admin_user(request)
    async with session_scope() as session:
        row = await _model_row(session, provider_id)
        await session.execute(delete(StageRoute).where(StageRoute.service_config_id == row.id))
        await session.delete(row)
        session.add(AuditLog(actor_id=admin.id, action="provider_delete", target=f"service_config:{provider_id}", detail=row.name))
        return {"ok": True}


@router.post("/providers/{provider_id}/test")
async def test_provider(provider_id: int, body: ProviderTestIn, request: Request):
    await admin_user(request)
    settings = request.app.state.settings
    async with session_scope() as session:
        row = await _model_row(session, provider_id)
        provider = to_provider(row, settings.master_key)
    if settings.llm_mode == "mock" and not body.live:
        return {"ok": True, "mock": True, "protocol": provider.protocol, "model": provider.model, "text": "OK"}
    adapter = OpenAIChatAdapter() if provider.protocol == "openai" else AnthropicMessagesAdapter()
    trial = ProviderConfig(
        id=provider.id,
        name=provider.name,
        protocol=provider.protocol,
        base_url=provider.base_url,
        api_key=provider.api_key,
        model=provider.model,
        extra_headers=provider.extra_headers,
        extra_params={**provider.extra_params, "max_tokens": 32},
        timeout=min(provider.timeout, 30),
    )
    try:
        result = await adapter.complete(trial, [{"role": "user", "content": "只回复 OK"}])
    except (ProviderError, Exception) as exc:
        raise AppError(f"连通失败：{exc}") from exc
    return {"ok": True, "mock": False, "protocol": provider.protocol, "model": provider.model, "text": result.content[:200]}


@router.get("/routes")
async def list_routes(request: Request):
    await admin_user(request)
    async with session_scope() as session:
        rows = (await session.scalars(select(StageRoute).order_by(StageRoute.stage.asc(), StageRoute.sort_order.asc()))).all()
        grouped: dict[tuple[str, str], list[int]] = {}
        for row in rows:
            grouped.setdefault((row.stage, row.tier), []).append(row.service_config_id)
        return {
            "stages": [{"key": key, "label": label} for key, label in STAGES],
            "tiers": [{"key": "*", "label": "全部档位"}] + [{"key": key, "label": label} for key, label in TIER_LABELS.items()],
            "routes": [{"stage": stage, "tier": tier, "provider_ids": ids} for (stage, tier), ids in grouped.items()],
        }


@router.put("/routes")
async def save_routes(body: RouteIn, request: Request):
    admin = await admin_user(request)
    stages = {key for key, _label in STAGES}
    if body.stage not in stages:
        raise AppError("未知环节")
    if body.tier != "*" and body.tier not in TIER_LABELS:
        raise AppError("未知档位")
    async with session_scope() as session:
        for provider_id in body.provider_ids:
            row = await session.get(ServiceConfig, provider_id)
            if row is None or row.config_type != "text_model":
                raise AppError(f"模型服务 {provider_id} 不存在")
        await session.execute(delete(StageRoute).where(StageRoute.stage == body.stage, StageRoute.tier == body.tier))
        for index, provider_id in enumerate(body.provider_ids):
            session.add(StageRoute(stage=body.stage, tier=body.tier, service_config_id=provider_id, sort_order=index))
        session.add(
            AuditLog(
                actor_id=admin.id,
                action="stage_route",
                target=f"{body.stage}:{body.tier}",
                detail="回退顺序：" + ",".join(str(item) for item in body.provider_ids),
            )
        )
        return {"ok": True}


@router.get("/wechat")
async def get_wechat(request: Request):
    await admin_user(request)
    async with session_scope() as session:
        row = await _wechat_row(session, request.app.state.settings)
        extra = row.extra_params or {}
        return {"mode": extra.get("mode") or "mock", "api_base": row.base_url, "public_egress_ip": extra.get("public_egress_ip") or ""}


@router.put("/wechat")
async def save_wechat(body: WeChatSettingsIn, request: Request):
    admin = await admin_user(request)
    if body.mode not in ("mock", "live"):
        raise AppError("微信模式只能是 mock 或 live")
    _validate_http_url(body.api_base)
    async with session_scope() as session:
        row = await _wechat_row(session, request.app.state.settings)
        row.base_url = body.api_base.strip()
        row.extra_params = {"mode": body.mode, "public_egress_ip": body.public_egress_ip.strip()}
        row.updated_at = datetime.now(timezone.utc)
        session.add(AuditLog(actor_id=admin.id, action="wechat_settings", target="wechat", detail=f"模式 {body.mode}"))
        return {"ok": True}


@router.get("/sensitive-words")
async def get_words(request: Request):
    await admin_user(request)
    async with session_scope() as session:
        words = await load_sensitive_words(session)
        row = await session.scalar(select(ServiceConfig).where(ServiceConfig.config_type == "audit", ServiceConfig.name == "local_words"))
        custom = []
        if row and isinstance(row.extra_params, dict):
            custom = row.extra_params.get("words") or []
        return {"words": words, "custom": custom}


@router.put("/sensitive-words")
async def save_words(body: SensitiveIn, request: Request):
    admin = await admin_user(request)
    custom = []
    for word in body.words:
        cleaned = word.strip()
        if cleaned and cleaned not in custom:
            custom.append(cleaned[:40])
    async with session_scope() as session:
        row = await session.scalar(select(ServiceConfig).where(ServiceConfig.config_type == "audit", ServiceConfig.name == "local_words"))
        if row is None:
            row = ServiceConfig(config_type="audit", name="local_words", protocol="local", enabled=True)
            session.add(row)
        row.extra_params = {"words": custom}
        row.updated_at = datetime.now(timezone.utc)
        session.add(AuditLog(actor_id=admin.id, action="sensitive_words", target="audit", detail=f"{len(custom)} 个自定义词"))
        return {"words": effective_words(custom), "custom": custom}


@router.get("/audit-logs")
async def audit_logs(request: Request):
    await admin_user(request)
    async with session_scope() as session:
        rows = (await session.scalars(select(AuditLog).order_by(AuditLog.id.desc()).limit(100))).all()
        return [
            {"id": row.id, "actor_id": row.actor_id, "action": row.action, "target": row.target, "detail": row.detail, "created_at": row.created_at}
            for row in rows
        ]


def _validate_provider(body: ProviderIn) -> None:
    if body.protocol not in ("openai", "anthropic"):
        raise AppError("协议只支持 openai（Chat Completions）或 anthropic（Messages）")
    _validate_http_url(body.base_url)
    if body.timeout_seconds < 5 or body.timeout_seconds > 180:
        raise AppError("超时时间需在 5 到 180 秒之间")
    if not isinstance(body.extra_headers, dict) or not isinstance(body.extra_params, dict):
        raise AppError("额外请求头和参数必须是 JSON 对象")
    for key, value in body.extra_headers.items():
        if not str(key).strip():
            raise AppError("请求头名称不能为空")
        if not isinstance(value, str):
            raise AppError("请求头的值必须是字符串")


def _validate_http_url(value: str) -> None:
    parsed = urlparse(value.strip())
    if parsed.scheme not in ("http", "https") or not parsed.netloc:
        raise AppError("接口地址需要以 http:// 或 https:// 开头")
    host = (parsed.hostname or "").lower()
    if host in {"169.254.169.254", "metadata.google.internal"}:
        raise AppError("不能使用云主机元数据地址")


async def _model_row(session, provider_id: int) -> ServiceConfig:
    row = await session.get(ServiceConfig, provider_id)
    if row is None or row.config_type != "text_model":
        raise AppError("找不到这个模型服务", 404)
    return row


async def _wechat_row(session, settings) -> ServiceConfig:
    row = await session.scalar(select(ServiceConfig).where(ServiceConfig.config_type == "wechat", ServiceConfig.name == "wechat"))
    if row is None:
        row = ServiceConfig(
            config_type="wechat",
            name="wechat",
            protocol="wechat",
            base_url=settings.wechat_api_base,
            extra_params={"mode": settings.wechat_mode, "public_egress_ip": settings.public_egress_ip},
            enabled=True,
        )
        session.add(row)
        await session.flush()
    return row


def _provider_dict(row: ServiceConfig, master_key: str) -> dict:
    hint = ""
    if row.api_key_encrypted:
        try:
            hint = mask_secret(decrypt_secret(master_key, row.api_key_encrypted))
        except ValueError:
            hint = "无法解密"
    return {
        "id": row.id,
        "name": row.name,
        "protocol": row.protocol,
        "base_url": row.base_url,
        "model": row.model_name,
        "extra_headers": row.extra_headers or {},
        "extra_params": row.extra_params or {},
        "priority": row.priority,
        "enabled": row.enabled,
        "timeout_seconds": row.timeout_seconds,
        "has_key": bool(row.api_key_encrypted),
        "key_hint": hint,
    }
