import base64
import hashlib
import secrets
from datetime import datetime, timedelta, timezone

import jwt
from cryptography.fernet import Fernet, InvalidToken


def hash_password(password: str) -> str:
    salt = secrets.token_bytes(16)
    digest = hashlib.pbkdf2_hmac("sha256", password.encode(), salt, 100_000)
    return base64.b64encode(salt).decode() + "$" + base64.b64encode(digest).decode()


def verify_password(password: str, stored: str) -> bool:
    try:
        salt_b64, digest_b64 = stored.split("$", 1)
        salt = base64.b64decode(salt_b64)
        expected = base64.b64decode(digest_b64)
    except (ValueError, TypeError):
        return False
    digest = hashlib.pbkdf2_hmac("sha256", password.encode(), salt, 100_000)
    return secrets.compare_digest(digest, expected)


def _fernet(master_key: str) -> Fernet:
    raw = hashlib.sha256(master_key.encode()).digest()
    return Fernet(base64.urlsafe_b64encode(raw))


def encrypt_secret(master_key: str, plain: str) -> str:
    if not plain:
        return ""
    return _fernet(master_key).encrypt(plain.encode()).decode()


def decrypt_secret(master_key: str, token: str) -> str:
    if not token:
        return ""
    try:
        return _fernet(master_key).decrypt(token.encode()).decode()
    except InvalidToken as exc:
        raise ValueError("无法解密凭证，请确认主密钥未变更") from exc


def mask_secret(value: str) -> str:
    if not value:
        return ""
    if len(value) <= 4:
        return "****"
    return "****" + value[-4:]


def make_token(user_id: int, secret: str, days: int = 7) -> str:
    now = datetime.now(timezone.utc)
    payload = {"sub": str(user_id), "exp": now + timedelta(days=days)}
    return jwt.encode(payload, secret, algorithm="HS256")


def read_token(token: str, secret: str) -> int:
    payload = jwt.decode(token, secret, algorithms=["HS256"])
    return int(payload["sub"])
