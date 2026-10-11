"""微信草稿箱客户端。官方接口：stable_token、永久素材、草稿新增。"""

import json
import uuid
from dataclasses import dataclass, field

import httpx

from app.services.images import cover_png


class WeChatError(Exception):
    pass


@dataclass
class PermissionReport:
    ok: bool
    auth_failed: bool
    can_draft: bool
    delivery_mode: str
    checks: list[dict] = field(default_factory=list)


class MockWeChatClient:
    """进程内模拟草稿箱，供本地与测试使用。"""

    store: dict[str, dict] = {}

    def __init__(self, app_id: str, app_secret: str, base_url: str = "https://api.weixin.qq.com"):
        self.app_id = app_id
        self.app_secret = app_secret
        self.base_url = base_url
        self.store.setdefault(app_id, {"materials": {}, "drafts": {}, "token": f"mock-token-{app_id}"})

    @classmethod
    def reset(cls) -> None:
        cls.store = {}

    async def get_token(self) -> str:
        if not self.app_id or not self.app_secret:
            raise WeChatError("缺少 AppID 或 AppSecret")
        return self.store[self.app_id]["token"]

    async def upload_thumb(self, image: bytes, filename: str = "cover.png") -> str:
        await self.get_token()
        media_id = f"mock_thumb_{uuid.uuid4().hex[:12]}"
        self.store[self.app_id]["materials"][media_id] = {"filename": filename, "size": len(image)}
        return media_id

    async def add_draft(self, article: dict) -> str:
        await self.get_token()
        if not article.get("thumb_media_id"):
            raise WeChatError("草稿缺少封面素材")
        media_id = f"mock_draft_{uuid.uuid4().hex[:12]}"
        self.store[self.app_id]["drafts"][media_id] = dict(article)
        return media_id

    async def update_draft(self, media_id: str, article: dict) -> str:
        drafts = self.store[self.app_id]["drafts"]
        if media_id not in drafts:
            raise WeChatError("找不到要更新的草稿")
        drafts[media_id] = dict(article)
        return media_id

    async def get_draft(self, media_id: str) -> dict:
        draft = self.store[self.app_id]["drafts"].get(media_id)
        if not draft:
            raise WeChatError("找不到草稿")
        return draft

    async def delete_draft(self, media_id: str) -> None:
        self.store[self.app_id]["drafts"].pop(media_id, None)

    async def draft_count(self) -> int:
        await self.get_token()
        return len(self.store[self.app_id]["drafts"])


class LiveWeChatClient:
    def __init__(
        self,
        app_id: str,
        app_secret: str,
        base_url: str = "https://api.weixin.qq.com",
        transport: httpx.AsyncBaseTransport | None = None,
        timeout: float = 30,
    ):
        self.app_id = app_id
        self.app_secret = app_secret
        self.base_url = base_url.rstrip("/")
        self.transport = transport
        self.timeout = timeout
        self._token = ""
        self._token_until = 0.0

    async def get_token(self, force: bool = False) -> str:
        import time

        if self._token and not force and time.time() < self._token_until:
            return self._token
        data = await self._request(
            "POST",
            "/cgi-bin/stable_token",
            json_body={
                "grant_type": "client_credential",
                "appid": self.app_id,
                "secret": self.app_secret,
                "force_refresh": False,
            },
            auth=False,
        )
        token = data.get("access_token")
        if not token:
            raise WeChatError("未返回 access_token")
        self._token = token
        self._token_until = time.time() + max(60, int(data.get("expires_in") or 7200) - 300)
        return token

    async def upload_thumb(self, image: bytes, filename: str = "cover.png") -> str:
        token = await self.get_token()
        url = f"{self.base_url}/cgi-bin/material/add_material"
        files = {"media": (filename, image, "image/png")}
        try:
            async with httpx.AsyncClient(timeout=self.timeout, transport=self.transport) as client:
                response = await client.post(url, params={"access_token": token, "type": "image"}, files=files)
        except httpx.HTTPError as exc:
            raise WeChatError(f"上传封面失败：{exc}") from exc
        data = _parse_wechat(response)
        media_id = data.get("media_id")
        if not media_id:
            raise WeChatError("上传封面未返回 media_id")
        return media_id

    async def add_draft(self, article: dict) -> str:
        data = await self._request(
            "POST",
            "/cgi-bin/draft/add",
            json_body={"articles": [_draft_fields(article)]},
        )
        media_id = data.get("media_id")
        if not media_id:
            raise WeChatError("新增草稿未返回 media_id")
        return media_id

    async def update_draft(self, media_id: str, article: dict) -> str:
        await self._request(
            "POST",
            "/cgi-bin/draft/update",
            json_body={"media_id": media_id, "index": 0, "articles": _draft_fields(article)},
        )
        return media_id

    async def get_draft(self, media_id: str) -> dict:
        return await self._request("POST", "/cgi-bin/draft/get", json_body={"media_id": media_id})

    async def delete_draft(self, media_id: str) -> None:
        await self._request("POST", "/cgi-bin/draft/delete", json_body={"media_id": media_id})

    async def draft_count(self) -> int:
        data = await self._request("GET", "/cgi-bin/draft/count")
        return int(data.get("total_count") or data.get("item_count") or 0)

    async def _request(self, method: str, path: str, json_body: dict | None = None, auth: bool = True) -> dict:
        params = {}
        if auth:
            params["access_token"] = await self.get_token()
        url = self.base_url + path
        try:
            async with httpx.AsyncClient(timeout=self.timeout, transport=self.transport) as client:
                response = await client.request(method, url, params=params, json=json_body)
        except httpx.HTTPError as exc:
            raise WeChatError(f"微信接口网络错误：{exc}") from exc
        return _parse_wechat(response)


def _draft_fields(article: dict) -> dict:
    return {
        "title": article.get("title") or "未命名",
        "author": article.get("author") or "公号智写",
        "digest": (article.get("digest") or "")[:120],
        "content": article.get("content") or "",
        "content_source_url": article.get("content_source_url") or "",
        "thumb_media_id": article.get("thumb_media_id") or "",
        "need_open_comment": 0,
        "only_fans_can_comment": 0,
    }


def _parse_wechat(response: httpx.Response) -> dict:
    try:
        data = response.json()
    except json.JSONDecodeError as exc:
        raise WeChatError(f"微信接口返回无法解析（HTTP {response.status_code}）") from exc
    if not isinstance(data, dict):
        raise WeChatError("微信接口返回格式无法识别")
    errcode = data.get("errcode")
    if errcode not in (None, 0):
        raise WeChatError(f"微信接口错误 {errcode}：{data.get('errmsg') or ''}")
    if response.status_code >= 400:
        raise WeChatError(f"微信接口 HTTP {response.status_code}")
    return data


async def check_permissions(client, template_key: str = "simple") -> PermissionReport:
    checks: list[dict] = []
    try:
        await client.get_token()
        checks.append({"name": "access_token", "ok": True, "detail": "已获取调用凭证"})
    except Exception as exc:
        checks.append({"name": "access_token", "ok": False, "detail": str(exc)})
        return PermissionReport(False, True, False, "export", checks)

    try:
        count = await client.draft_count()
        checks.append({"name": "draft_count", "ok": True, "detail": f"当前草稿数量 {count}"})
    except Exception as exc:
        checks.append({"name": "draft_count", "ok": False, "detail": str(exc)})
        return PermissionReport(True, False, False, "export", checks)

    media_id = ""
    try:
        media_id = await client.upload_thumb(cover_png(template_key))
        checks.append({"name": "material", "ok": True, "detail": "封面素材上传成功"})
    except Exception as exc:
        checks.append({"name": "material", "ok": False, "detail": str(exc)})
        return PermissionReport(True, False, False, "export", checks)

    probe_id = ""
    try:
        probe_id = await client.add_draft(
            {
                "title": "绑定检测可删除",
                "author": "公号智写",
                "digest": "权限检测草稿，可删除",
                "content": "<p>这是一条绑定检测草稿，系统会尝试删除。</p>",
                "thumb_media_id": media_id,
            }
        )
        await client.get_draft(probe_id)
        checks.append({"name": "draft_write", "ok": True, "detail": "草稿写入与读取成功"})
    except Exception as exc:
        checks.append({"name": "draft_write", "ok": False, "detail": str(exc)})
        if probe_id:
            try:
                await client.delete_draft(probe_id)
            except Exception:
                pass
        return PermissionReport(True, False, False, "export", checks)

    try:
        await client.delete_draft(probe_id)
        checks.append({"name": "draft_cleanup", "ok": True, "detail": "检测草稿已删除"})
    except Exception as exc:
        checks.append({"name": "draft_cleanup", "ok": False, "detail": f"检测草稿未能删除：{exc}"})
    return PermissionReport(True, False, True, "draft", checks)


def build_wechat_client(app_id: str, app_secret: str, *, mode: str, base_url: str, transport=None):
    if mode == "mock" or app_id.startswith("wx_mock"):
        return MockWeChatClient(app_id, app_secret, base_url)
    return LiveWeChatClient(app_id, app_secret, base_url, transport=transport)
