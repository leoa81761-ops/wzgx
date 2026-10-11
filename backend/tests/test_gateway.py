import json

import httpx
import pytest

from app.services.llm import (
    AnthropicMessagesAdapter,
    ModelGateway,
    OpenAIChatAdapter,
    ProviderConfig,
    ProviderError,
    anthropic_url,
    openai_url,
)


def _provider(protocol, name="主模型", base="https://llm.example.com/v1", model="demo-model", **kwargs):
    data = dict(
        id=1,
        name=name,
        protocol=protocol,
        base_url=base,
        api_key="sk-test-key",
        model=model,
        extra_headers={"X-Custom": "abc"},
        extra_params={"temperature": 0.2, "model": "should-not-win"},
        timeout=15,
    )
    data.update(kwargs)
    return ProviderConfig(**data)


def test_url_normalization():
    assert openai_url("https://api.openai.com/v1") == "https://api.openai.com/v1/chat/completions"
    assert openai_url("https://llm.example.com/proxy/chat/completions") == "https://llm.example.com/proxy/chat/completions"
    assert anthropic_url("https://api.anthropic.com") == "https://api.anthropic.com/v1/messages"
    assert anthropic_url("https://claude.example.com/v1") == "https://claude.example.com/v1/messages"
    assert anthropic_url("https://claude.example.com/v1/messages") == "https://claude.example.com/v1/messages"


@pytest.mark.asyncio
async def test_openai_adapter_request_shape():
    seen = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["url"] = str(request.url)
        seen["headers"] = dict(request.headers)
        seen["body"] = json.loads(request.content.decode())
        return httpx.Response(
            200,
            json={"choices": [{"message": {"content": "{\"title\":\"好\"}"}}], "usage": {"prompt_tokens": 3, "completion_tokens": 4}},
        )

    adapter = OpenAIChatAdapter(transport=httpx.MockTransport(handler))
    result = await adapter.complete(_provider("openai"), [{"role": "user", "content": "hi"}])
    assert seen["url"] == "https://llm.example.com/v1/chat/completions"
    assert seen["headers"]["authorization"] == "Bearer sk-test-key"
    assert seen["headers"]["x-custom"] == "abc"
    assert seen["body"]["model"] == "demo-model"
    assert seen["body"]["temperature"] == 0.2
    assert seen["body"]["stream"] is False
    assert result.content == "{\"title\":\"好\"}"
    assert result.protocol == "openai"
    assert result.prompt_tokens == 3


@pytest.mark.asyncio
async def test_anthropic_adapter_request_shape():
    seen = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["url"] = str(request.url)
        seen["headers"] = dict(request.headers)
        seen["body"] = json.loads(request.content.decode())
        return httpx.Response(
            200,
            json={
                "content": [{"type": "text", "text": "{\"title\":\"好\"}"}],
                "usage": {"input_tokens": 5, "output_tokens": 6},
            },
        )

    adapter = AnthropicMessagesAdapter(transport=httpx.MockTransport(handler))
    result = await adapter.complete(
        _provider("anthropic", base="https://claude.example.com"),
        [{"role": "system", "content": "只输出 JSON"}, {"role": "user", "content": "写一段"}],
    )
    assert seen["url"] == "https://claude.example.com/v1/messages"
    assert seen["headers"]["x-api-key"] == "sk-test-key"
    assert seen["headers"]["anthropic-version"] == "2023-06-01"
    assert seen["headers"]["x-custom"] == "abc"
    assert seen["body"]["model"] == "demo-model"
    assert seen["body"]["temperature"] == 0.2
    assert seen["body"]["system"] == "只输出 JSON"
    assert seen["body"]["messages"] == [{"role": "user", "content": "写一段"}]
    assert "stream" not in seen["body"]
    assert result.completion_tokens == 6


@pytest.mark.asyncio
async def test_gateway_falls_back_from_anthropic_to_openai():
    def handler(request: httpx.Request) -> httpx.Response:
        if request.url.path.endswith("/messages"):
            return httpx.Response(500, json={"error": {"message": "overloaded"}})
        return httpx.Response(200, json={"choices": [{"message": {"content": "{\"title\":\"备用模型\"}"}}]})

    transport = httpx.MockTransport(handler)
    gateway = ModelGateway(
        {
            "anthropic": AnthropicMessagesAdapter(transport),
            "openai": OpenAIChatAdapter(transport),
        }
    )
    providers = [
        _provider("anthropic", name="Claude", base="https://claude.example.com"),
        _provider("openai", name="兼容接口", base="https://llm.example.com/v1"),
    ]
    data, completion = await gateway.complete_json(providers, [{"role": "user", "content": "{}"}], lambda item: item)
    assert data["title"] == "备用模型"
    assert [item["ok"] for item in completion.attempts] == [False, True]
    assert completion.attempts[1]["protocol"] == "openai"


@pytest.mark.asyncio
async def test_gateway_errors_when_every_provider_fails():
    def handler(_request: httpx.Request) -> httpx.Response:
        return httpx.Response(429, text="rate limit")

    gateway = ModelGateway({"openai": OpenAIChatAdapter(transport=httpx.MockTransport(handler))})
    with pytest.raises(ProviderError):
        await gateway.complete_json([_provider("openai")], [{"role": "user", "content": "{}"}], lambda item: item)
