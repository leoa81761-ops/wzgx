from datetime import datetime
from typing import Any

from pydantic import BaseModel, Field


class RegisterIn(BaseModel):
    account: str = Field(min_length=3, max_length=255)
    password: str = Field(min_length=8, max_length=72)
    agree: bool = False


class LoginIn(BaseModel):
    account: str
    password: str


class AccountIn(BaseModel):
    name: str = Field(min_length=1, max_length=40)
    app_id: str = Field(min_length=4, max_length=64)
    app_secret: str = Field(min_length=4, max_length=128)
    subject_type: str
    service_type: str
    verified: bool = False
    consent: bool = False


class SecretIn(BaseModel):
    app_secret: str = ""


class PlanIn(BaseModel):
    wx_account_id: int
    direction: str = Field(min_length=1, max_length=40)
    style_key: str
    template_key: str
    word_target: int
    schedule_type: str
    weekdays: list[int] = []
    time_window: str


class TierIn(BaseModel):
    tier: str
    expires_at: datetime | None = None
    note: str = ""


class ProviderIn(BaseModel):
    name: str = Field(min_length=1, max_length=40)
    protocol: str
    base_url: str
    api_key: str = ""
    model: str = Field(min_length=1, max_length=120)
    extra_headers: dict[str, str] = {}
    extra_params: dict[str, Any] = {}
    priority: int = 100
    enabled: bool = True
    timeout_seconds: int = 60


class ProviderTestIn(BaseModel):
    live: bool = False


class RouteIn(BaseModel):
    stage: str
    tier: str = "*"
    provider_ids: list[int]


class WeChatSettingsIn(BaseModel):
    mode: str
    api_base: str
    public_egress_ip: str = ""


class SensitiveIn(BaseModel):
    words: list[str]
