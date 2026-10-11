from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    database_url: str = "sqlite+aiosqlite:///./data/dev.db"
    master_key: str = "dev-master-key-change-me"
    jwt_secret: str = "dev-jwt-secret-change-me"
    admin_email: str = "admin@example.com"
    admin_password: str = "admin123456"
    wechat_mode: str = "mock"
    wechat_api_base: str = "https://api.weixin.qq.com"
    llm_mode: str = "mock"
    public_egress_ip: str = ""
    scheduler_enabled: bool = True
    scheduler_interval_seconds: int = 60
    max_concurrency: int = 20
    stale_task_seconds: int = 900
    static_dir: str = ""


def load_settings() -> Settings:
    return Settings()
