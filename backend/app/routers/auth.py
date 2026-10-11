import re

from fastapi import APIRouter, Request
from sqlalchemy import or_, select

from app.catalog import LEGAL_VERSION
from app.db import session_scope
from app.deps import client_ip, current_user, user_agent
from app.errors import AppError
from app.models import ConsentLog, User
from app.schemas import LoginIn, RegisterIn
from app.security import hash_password, make_token, verify_password

router = APIRouter(prefix="/api/auth", tags=["auth"])

PHONE_RE = re.compile(r"^1[3-9]\d{9}$")
EMAIL_RE = re.compile(r"^[^@\s]+@[^@\s]+\.[^@\s]+$")


def classify_account(value: str) -> tuple[str, str]:
    raw = value.strip()
    if PHONE_RE.fullmatch(raw):
        return "phone", raw
    if EMAIL_RE.fullmatch(raw):
        return "email", raw.lower()
    raise AppError("请输入有效的手机号或邮箱")


def user_dict(user: User) -> dict:
    return {
        "id": user.id,
        "email": user.email,
        "phone": user.phone,
        "account": user.email or user.phone,
        "tier": user.tier,
        "is_admin": user.is_admin,
        "tier_expires_at": user.tier_expires_at,
    }


@router.post("/register")
async def register(body: RegisterIn, request: Request):
    if body.agree is not True:
        raise AppError("请先阅读并勾选同意《用户协议》和《隐私政策》")
    kind, account = classify_account(body.account)
    async with session_scope() as session:
        existing = await session.scalar(select(User).where(or_(User.email == account, User.phone == account)))
        if existing:
            raise AppError("该手机号或邮箱已注册")
        user = User(
            email=account if kind == "email" else None,
            phone=account if kind == "phone" else None,
            password_hash=hash_password(body.password),
            tier="free",
        )
        session.add(user)
        await session.flush()
        for doc_type in ("user_agreement", "privacy"):
            session.add(
                ConsentLog(
                    user_id=user.id,
                    doc_type=doc_type,
                    version=LEGAL_VERSION,
                    consent_item="register",
                    ip=client_ip(request),
                    user_agent=user_agent(request),
                )
            )
        token = make_token(user.id, request.app.state.settings.jwt_secret)
        return {"token": token, "user": user_dict(user)}


@router.post("/login")
async def login(body: LoginIn, request: Request):
    kind, account = classify_account(body.account)
    async with session_scope() as session:
        column = User.email if kind == "email" else User.phone
        user = await session.scalar(select(User).where(column == account))
        if user is None or not verify_password(body.password, user.password_hash):
            raise AppError("账号或密码不正确", 401)
        if not user.is_active:
            raise AppError("账号已停用", 403)
        token = make_token(user.id, request.app.state.settings.jwt_secret)
        return {"token": token, "user": user_dict(user)}


@router.get("/me")
async def me(request: Request):
    user = await current_user(request)
    return user_dict(user)
