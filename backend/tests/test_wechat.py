import json

import httpx
import pytest

from app.services.wechat import LiveWeChatClient, MockWeChatClient, WeChatError, check_permissions


@pytest.mark.asyncio
async def test_mock_client_add_and_update_draft():
    MockWeChatClient.reset()
    client = MockWeChatClient("wx_mock_1", "secret")
    report = await check_permissions(client)
    assert report.can_draft is True
    assert report.delivery_mode == "draft"
    assert MockWeChatClient.store["wx_mock_1"]["drafts"] == {}
    thumb = await client.upload_thumb(b"png")
    media_id = await client.add_draft({"title": "标题", "content": "<p>正文</p>", "thumb_media_id": thumb, "digest": "摘要"})
    assert media_id.startswith("mock_draft_")
    await client.update_draft(media_id, {"title": "新标题", "content": "<p>新</p>", "thumb_media_id": thumb, "digest": "摘要"})
    saved = await client.get_draft(media_id)
    assert saved["title"] == "新标题"


@pytest.mark.asyncio
async def test_live_client_token_material_and_draft():
    calls = []

    def handler(request: httpx.Request) -> httpx.Response:
        calls.append((request.method, request.url.path))
        path = request.url.path
        if path.endswith("/stable_token"):
            body = json.loads(request.content.decode())
            assert body["grant_type"] == "client_credential"
            assert body["appid"] == "wx123"
            assert body["secret"] == "sec"
            return httpx.Response(200, json={"access_token": "token-1", "expires_in": 7200})
        if path.endswith("/material/add_material"):
            assert request.url.params["access_token"] == "token-1"
            assert request.url.params["type"] == "image"
            return httpx.Response(200, json={"media_id": "thumb-1"})
        if path.endswith("/draft/add"):
            body = json.loads(request.content.decode())
            assert body["articles"][0]["title"] == "你好"
            assert body["articles"][0]["thumb_media_id"] == "thumb-1"
            assert isinstance(body["articles"], list)
            return httpx.Response(200, json={"media_id": "draft-1"})
        if path.endswith("/draft/count"):
            return httpx.Response(200, json={"total_count": 2})
        if path.endswith("/draft/get"):
            return httpx.Response(200, json={"news_item": []})
        if path.endswith("/draft/delete"):
            return httpx.Response(200, json={"errcode": 0, "errmsg": "ok"})
        return httpx.Response(404, json={"errcode": 404, "errmsg": "missing"})

    client = LiveWeChatClient("wx123", "sec", transport=httpx.MockTransport(handler))
    assert await client.get_token() == "token-1"
    assert await client.upload_thumb(b"png") == "thumb-1"
    assert await client.add_draft({"title": "你好", "content": "<p>a</p>", "thumb_media_id": "thumb-1", "digest": "d"}) == "draft-1"
    assert await client.draft_count() == 2
    assert any(path.endswith("/stable_token") for _method, path in calls)
    assert any(path.endswith("/draft/add") for _method, path in calls)


@pytest.mark.asyncio
async def test_live_permission_failure_falls_back_to_export():
    def handler(request: httpx.Request) -> httpx.Response:
        if request.url.path.endswith("/stable_token"):
            return httpx.Response(200, json={"access_token": "token-1", "expires_in": 7200})
        if request.url.path.endswith("/draft/count"):
            return httpx.Response(200, json={"errcode": 48001, "errmsg": "api unauthorized"})
        return httpx.Response(200, json={"errcode": 0})

    client = LiveWeChatClient("wx123", "sec", transport=httpx.MockTransport(handler))
    report = await check_permissions(client)
    assert report.auth_failed is False
    assert report.can_draft is False
    assert report.delivery_mode == "export"


@pytest.mark.asyncio
async def test_live_invalid_credential():
    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json={"errcode": 40001, "errmsg": "invalid credential"})

    client = LiveWeChatClient("wx123", "bad", transport=httpx.MockTransport(handler))
    with pytest.raises(WeChatError):
        await client.get_token()
    report = await check_permissions(client)
    assert report.auth_failed is True
