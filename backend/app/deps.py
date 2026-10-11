from fastapi import Request

from app.db import session_scope
from app.errors import AppError
from app.models import User
from app.security import read_token


def client_ip(request: Request) -> str:
    forwarded = request.headers.get("x-forwarded-for", "")
    if forwarded:
        return forwarded.split(",")[0].strip()[:64]
    if request.client:
        return request.client.host or ""
    return ""


def user_agent(request: Request) -> str:
    return (request.headers.get("user-agent") or "")[:256]


async def current_user(request: Request) -> User:
    header = request.headers.get("authorization") or ""
    if not header.lower().startswith("bearer "):
        raise AppError("请先登录", 401)
    token = header.split(" ", 1)[1].strip()
    try:
        user_id = read_token(token, request.app.state.settings.jwt_secret)
    except Exception as exc:
        raise AppError("登录已失效，请重新登录", 401) from exc
    async with session_scope() as session:
        user = await session.get(User, user_id)
        if user is None or not user.is_active:
            raise AppError("请先登录", 401)
        return user


async def admin_user(request: Request) -> User:
    user = await current_user(request)
    if not user.is_admin:
        raise AppError("需要管理员权限", 403)
    return user
