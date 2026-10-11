def _auth(token: str) -> dict:
    return {"Authorization": f"Bearer {token}"}


def test_register_requires_consent(client):
    denied = client.post("/api/auth/register", json={"account": "13800138000", "password": "password12", "agree": False})
    assert denied.status_code == 400
    assert "同意" in denied.json()["detail"]


def test_full_flow_generate_now(client):
    registered = client.post(
        "/api/auth/register",
        json={"account": "reader@example.com", "password": "password12", "agree": True},
    )
    assert registered.status_code == 200
    token = registered.json()["token"]
    headers = _auth(token)

    consents = client.get("/api/me/consents", headers=headers)
    assert consents.status_code == 200
    assert {item["doc_type"] for item in consents.json()} >= {"user_agreement", "privacy"}

    legal = client.get("/api/legal/user_agreement")
    assert legal.status_code == 200
    assert "待法务审核" in legal.json()["content"]

    bound = client.post(
        "/api/accounts",
        headers=headers,
        json={
            "name": "我的订阅号",
            "app_id": "wx_mock_demo",
            "app_secret": "mock-secret",
            "subject_type": "personal",
            "service_type": "subscription",
            "verified": False,
            "consent": True,
        },
    )
    assert bound.status_code == 200, bound.text
    account = bound.json()
    assert account["delivery_mode"] == "draft"
    assert account["permission_report"]["can_draft"] is True

    created = client.post(
        "/api/plans",
        headers=headers,
        json={
            "wx_account_id": account["id"],
            "direction": "职场成长",
            "style_key": "casual",
            "template_key": "simple",
            "word_target": 800,
            "schedule_type": "weekly",
            "weekdays": [1, 3, 5],
            "time_window": "morning",
        },
    )
    assert created.status_code == 200, created.text
    plan = created.json()
    assert plan["time_window"] == "night"
    assert plan["schedule_type"] == "weekly"

    generated = client.post(f"/api/plans/{plan['id']}/generate", headers=headers)
    assert generated.status_code == 200, generated.text
    article = generated.json()
    assert article["status"] == "in_draft"
    assert article["draft_media_id"].startswith("mock_draft_")
    assert "style=" in article["body_html"]
    assert "轻松口语" in article["body_html"]

    again = client.post(f"/api/plans/{plan['id']}/generate", headers=headers)
    assert again.status_code == 400
    assert "1 批" in again.json()["detail"]

    regenerated = client.post(f"/api/articles/{article['id']}/regenerate", headers=headers)
    assert regenerated.status_code == 200, regenerated.text
    assert regenerated.json()["status"] == "in_draft"
    assert regenerated.json()["draft_media_id"] == article["draft_media_id"]
    assert regenerated.json()["regen_count"] == 1

    blocked = client.post(
        "/api/plans",
        headers=headers,
        json={
            "wx_account_id": account["id"],
            "direction": "投资理财",
            "style_key": "formal",
            "template_key": "business",
            "word_target": 800,
            "schedule_type": "daily",
            "weekdays": [],
            "time_window": "night",
        },
    )
    assert blocked.status_code == 400

    admin = client.post("/api/auth/login", json={"account": "admin@example.com", "password": "admin123456"})
    assert admin.status_code == 200
    admin_headers = _auth(admin.json()["token"])
    provider = client.post(
        "/api/admin/providers",
        headers=admin_headers,
        json={
            "name": "Claude",
            "protocol": "anthropic",
            "base_url": "https://api.anthropic.com",
            "api_key": "sk-ant-secret",
            "model": "claude-test",
            "extra_headers": {"X-Tenant": "demo"},
            "extra_params": {"temperature": 0.4},
            "priority": 1,
            "enabled": True,
            "timeout_seconds": 60,
        },
    )
    assert provider.status_code == 200, provider.text
    body = provider.json()
    assert body["key_hint"].endswith("cret")
    assert "sk-ant-secret" not in str(body)
    backup = client.post(
        "/api/admin/providers",
        headers=admin_headers,
        json={
            "name": "兼容接口",
            "protocol": "openai",
            "base_url": "https://llm.example.com/v1",
            "api_key": "sk-openai",
            "model": "demo-writer",
            "extra_headers": {},
            "extra_params": {},
            "priority": 2,
            "enabled": True,
            "timeout_seconds": 60,
        },
    )
    assert backup.status_code == 200
    saved = client.put(
        "/api/admin/routes",
        headers=admin_headers,
        json={"stage": "draft", "tier": "*", "provider_ids": [provider.json()["id"], backup.json()["id"]]},
    )
    assert saved.status_code == 200
    routes = client.get("/api/admin/routes", headers=admin_headers).json()
    draft_route = next(item for item in routes["routes"] if item["stage"] == "draft" and item["tier"] == "*")
    assert draft_route["provider_ids"] == [provider.json()["id"], backup.json()["id"]]

    tier = client.put(
        f"/api/admin/users/{registered.json()['user']['id']}/tier",
        headers=admin_headers,
        json={"tier": "standard", "expires_at": "2026-12-01T00:00:00+08:00", "note": "线下收款 30 元"},
    )
    assert tier.status_code == 200
    me = client.get("/api/auth/me", headers=headers).json()
    assert me["tier"] == "standard"
