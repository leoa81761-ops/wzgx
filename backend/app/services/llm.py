"""模型网关：OpenAI Chat Completions 与 Anthropic Messages 两种协议。"""

import json
import re
from dataclasses import dataclass, field
from typing import Any, Protocol

import httpx

from app.catalog import CLICHES, DEFAULT_SENSITIVE_WORDS


class ProviderError(Exception):
    pass


@dataclass
class ProviderConfig:
    id: int
    name: str
    protocol: str
    base_url: str
    api_key: str
    model: str
    extra_headers: dict
    extra_params: dict
    timeout: float = 60


@dataclass
class Completion:
    content: str
    prompt_tokens: int = 0
    completion_tokens: int = 0
    provider_id: int = 0
    provider_name: str = ""
    protocol: str = ""
    model: str = ""
    attempts: list[dict] = field(default_factory=list)


class ChatAdapter(Protocol):
    async def complete(self, provider: ProviderConfig, messages: list[dict]) -> Completion: ...


def public_params(params: dict | None) -> dict:
    return {key: value for key, value in (params or {}).items() if not str(key).startswith("_")}


def openai_url(base_url: str) -> str:
    base = base_url.rstrip("/")
    if base.endswith("/chat/completions"):
        return base
    return base + "/chat/completions"


def anthropic_url(base_url: str) -> str:
    base = base_url.rstrip("/")
    if base.endswith("/messages"):
        return base
    if base.endswith("/v1"):
        return base + "/messages"
    return base + "/v1/messages"


def _headers(provider: ProviderConfig, defaults: dict) -> dict:
    headers = {"Content-Type": "application/json"}
    headers.update(defaults)
    for key, value in (provider.extra_headers or {}).items():
        headers[str(key)] = str(value)
    return headers


def parse_json_content(text: str) -> dict:
    raw = (text or "").strip()
    if raw.startswith("```"):
        raw = re.sub(r"^```(?:json)?", "", raw).strip()
        raw = re.sub(r"```$", "", raw).strip()
    try:
        data = json.loads(raw)
    except json.JSONDecodeError:
        start = raw.find("{")
        end = raw.rfind("}")
        if start < 0 or end <= start:
            raise
        data = json.loads(raw[start : end + 1])
    if not isinstance(data, dict):
        raise ValueError("模型输出不是 JSON 对象")
    return data


class OpenAIChatAdapter:
    def __init__(self, transport: httpx.AsyncBaseTransport | None = None):
        self.transport = transport

    async def complete(self, provider: ProviderConfig, messages: list[dict]) -> Completion:
        body: dict[str, Any] = {"model": provider.model, "messages": messages, "stream": False}
        body.update(public_params(provider.extra_params))
        body["model"] = provider.model
        body["stream"] = False
        headers = _headers(provider, {"Authorization": f"Bearer {provider.api_key}"})
        data = await _post_json(openai_url(provider.base_url), headers, body, provider.timeout, self.transport)
        content = _openai_text(data)
        usage = data.get("usage") or {}
        return Completion(
            content=content,
            prompt_tokens=int(usage.get("prompt_tokens") or 0),
            completion_tokens=int(usage.get("completion_tokens") or 0),
            provider_id=provider.id,
            provider_name=provider.name,
            protocol="openai",
            model=provider.model,
        )


class AnthropicMessagesAdapter:
    def __init__(self, transport: httpx.AsyncBaseTransport | None = None):
        self.transport = transport

    async def complete(self, provider: ProviderConfig, messages: list[dict]) -> Completion:
        system_parts: list[str] = []
        chat: list[dict] = []
        for message in messages:
            role = message.get("role")
            content = message.get("content") or ""
            if role == "system":
                system_parts.append(str(content))
            else:
                chat.append({"role": "assistant" if role == "assistant" else "user", "content": str(content)})
        if not chat:
            chat = [{"role": "user", "content": "请继续"}]
        body: dict[str, Any] = {"model": provider.model, "max_tokens": 4096, "messages": chat}
        if system_parts:
            body["system"] = "\n".join(system_parts)
        extra = public_params(provider.extra_params)
        extra.pop("messages", None)
        body.update(extra)
        body["model"] = provider.model
        headers = _headers(
            provider,
            {"x-api-key": provider.api_key, "anthropic-version": "2023-06-01"},
        )
        data = await _post_json(anthropic_url(provider.base_url), headers, body, provider.timeout, self.transport)
        content = _anthropic_text(data)
        usage = data.get("usage") or {}
        return Completion(
            content=content,
            prompt_tokens=int(usage.get("input_tokens") or 0),
            completion_tokens=int(usage.get("output_tokens") or 0),
            provider_id=provider.id,
            provider_name=provider.name,
            protocol="anthropic",
            model=provider.model,
        )


class DeterministicAdapter:
    """不访问网络。按协议记录调用，并按环节返回稳定 JSON。"""

    def __init__(self, protocol: str):
        self.protocol = protocol
        self.calls: list[dict] = []

    async def complete(self, provider: ProviderConfig, messages: list[dict]) -> Completion:
        if (provider.extra_params or {}).get("_mock_fail"):
            raise ProviderError(f"{provider.name} 模拟失败")
        self.calls.append(
            {
                "protocol": self.protocol,
                "model": provider.model,
                "base_url": provider.base_url,
                "headers": provider.extra_headers,
                "params": provider.extra_params,
            }
        )
        payload = _user_payload(messages)
        stage = payload.get("环节") or "正文"
        content = json.dumps(mock_for_stage(stage, payload), ensure_ascii=False)
        return Completion(
            content=content,
            prompt_tokens=120,
            completion_tokens=180,
            provider_id=provider.id,
            provider_name=provider.name,
            protocol=self.protocol,
            model=provider.model,
        )


class ModelGateway:
    def __init__(self, adapters: dict[str, ChatAdapter]):
        self.adapters = adapters

    async def complete_json(self, providers: list[ProviderConfig], messages: list[dict], validate) -> tuple[dict, Completion]:
        if not providers:
            raise ProviderError("没有可用的模型服务。请管理员在配置中心添加供应商，或使用模拟模式。")
        errors: list[str] = []
        attempts: list[dict] = []
        for provider in providers:
            adapter = self.adapters.get(provider.protocol)
            if adapter is None:
                message = f"{provider.name}: 不支持的协议 {provider.protocol}"
                errors.append(message)
                attempts.append({"provider_id": provider.id, "protocol": provider.protocol, "ok": False, "error": message})
                continue
            try:
                completion = await adapter.complete(provider, messages)
                data = parse_json_content(completion.content)
                validated = validate(data)
                attempts.append(
                    {
                        "provider_id": provider.id,
                        "provider_name": provider.name,
                        "protocol": provider.protocol,
                        "model": provider.model,
                        "ok": True,
                    }
                )
                completion.attempts = attempts
                return validated, completion
            except (ProviderError, ValueError, KeyError, json.JSONDecodeError, httpx.HTTPError) as exc:
                message = f"{provider.name}: {exc}"
                errors.append(message)
                attempts.append(
                    {
                        "provider_id": provider.id,
                        "provider_name": provider.name,
                        "protocol": provider.protocol,
                        "model": provider.model,
                        "ok": False,
                        "error": str(exc)[:300],
                    }
                )
        raise ProviderError("全部模型服务失败：" + "；".join(errors))


def build_gateway(llm_mode: str, transport: httpx.AsyncBaseTransport | None = None) -> ModelGateway:
    if llm_mode == "mock":
        return ModelGateway(
            {
                "openai": DeterministicAdapter("openai"),
                "anthropic": DeterministicAdapter("anthropic"),
            }
        )
    return ModelGateway(
        {
            "openai": OpenAIChatAdapter(transport),
            "anthropic": AnthropicMessagesAdapter(transport),
        }
    )


def stage_messages(stage_label: str, payload: dict) -> list[dict]:
    system = (
        "你是公众号写作助手。只输出一个 JSON 对象，不要使用 Markdown 代码块，不要输出 HTML。"
        "不得编造具体政策、标准、新闻事件、统计数据、人名引语。"
        "不得使用「震惊」「不转不是」「稳赚不赔」等标题党或承诺性用语，也不得写药物剂量、诊断、偏方或收益承诺。"
        "免费或无来源时不要给出参考网址。"
    )
    body = {"环节": stage_label, **payload}
    return [
        {"role": "system", "content": system},
        {"role": "user", "content": json.dumps(body, ensure_ascii=False)},
    ]


def mock_for_stage(stage: str, payload: dict) -> dict:
    if stage == "选题":
        title = f"{payload.get('subtopic') or '实用方法'}：{payload.get('angle') or '一个小做法'}"
        return {"title": title[:64], "angle": payload.get("angle") or ""}
    if stage == "大纲":
        return {
            "hook": f"先从「{payload.get('subtopic') or '这件事'}」里一个具体场景说起。",
            "points": [
                {"heading": "先看清眼前的问题", "intent": "用一个很小的场景说明读者为什么会卡住"},
                {"heading": "一个今天就能做的做法", "intent": "给出可以立刻执行的步骤"},
                {"heading": "少踩一个常见坑", "intent": "提醒一个容易做错的地方"},
            ],
            "closing": "选一个最小的步骤，做完再决定要不要继续。",
        }
    if stage == "审核":
        draft = payload.get("draft") or mock_for_stage("正文", payload)
        return _clean_draft(draft)
    return _draft_from_payload(payload)


def _draft_from_payload(payload: dict) -> dict:
    outline = payload.get("outline") or mock_for_stage("大纲", payload)
    style = payload.get("style_label") or "清楚"
    direction = payload.get("direction") or "这个方向"
    subtopic = payload.get("subtopic") or "这件事"
    angle = payload.get("angle") or "一个具体角度"
    title = (payload.get("topic_title") or f"{subtopic}：{angle}")[:64]
    sections = []
    for point in outline.get("points") or []:
        heading = point.get("heading") or "一个小节"
        sections.append(
            {
                "heading": heading,
                "paragraphs": [
                    f"这篇文章用「{style}」的语气来写{direction}里的{subtopic}。",
                    f"这一节顺着「{angle}」展开：把动作写小，读者才知道下一步做什么。",
                ],
                "list": ["先做一个最小步骤", "做完后用一句话记下结果", "不追求一次到位"],
            }
        )
    return {
        "title": title,
        "alt_titles": [f"{subtopic}可以先做的一件小事", f"关于{subtopic}，少做一件事"],
        "summary": f"一篇关于{direction}／{subtopic}的短文，供你阅读后自己判断是否采用。",
        "sections": sections,
        "closing": outline.get("closing") or "今天先完成最小的一步。",
        "sources": [],
    }


def _clean_draft(draft: dict) -> dict:
    cleaned = json.loads(json.dumps(draft, ensure_ascii=False))

    def walk(value):
        if isinstance(value, str):
            for word in DEFAULT_SENSITIVE_WORDS + CLICHES:
                value = value.replace(word, "")
            return value
        if isinstance(value, list):
            return [walk(item) for item in value]
        if isinstance(value, dict):
            return {key: walk(item) for key, item in value.items()}
        return value

    return walk(cleaned)


def _user_payload(messages: list[dict]) -> dict:
    for message in reversed(messages):
        if message.get("role") == "user":
            try:
                data = json.loads(message.get("content") or "{}")
            except json.JSONDecodeError:
                return {}
            return data if isinstance(data, dict) else {}
    return {}


async def _post_json(url: str, headers: dict, body: dict, timeout: float, transport) -> dict:
    try:
        async with httpx.AsyncClient(timeout=timeout, transport=transport) as client:
            response = await client.post(url, headers=headers, json=body)
    except httpx.HTTPError as exc:
        raise ProviderError(f"网络错误：{exc}") from exc
    if response.status_code >= 400:
        raise ProviderError(f"HTTP {response.status_code}：{response.text[:300]}")
    try:
        data = response.json()
    except json.JSONDecodeError as exc:
        raise ProviderError("模型接口没有返回 JSON") from exc
    if isinstance(data, dict) and data.get("error"):
        message = data["error"].get("message") if isinstance(data["error"], dict) else str(data["error"])
        raise ProviderError(message)
    if not isinstance(data, dict):
        raise ProviderError("模型接口返回格式无法识别")
    return data


def _openai_text(data: dict) -> str:
    choices = data.get("choices") or []
    if not choices:
        raise ProviderError("OpenAI 兼容接口没有返回 choices")
    message = choices[0].get("message") or {}
    content = message.get("content")
    if isinstance(content, str) and content.strip():
        return content
    if isinstance(content, list):
        texts = []
        for part in content:
            if isinstance(part, dict) and part.get("text"):
                texts.append(str(part["text"]))
            elif isinstance(part, str):
                texts.append(part)
        if texts:
            return "\n".join(texts)
    raise ProviderError("OpenAI 兼容接口没有返回文本")


def _anthropic_text(data: dict) -> str:
    blocks = data.get("content") or []
    texts = []
    for block in blocks:
        if isinstance(block, dict) and block.get("type") == "text":
            texts.append(str(block.get("text") or ""))
        elif isinstance(block, str):
            texts.append(block)
    if not texts:
        raise ProviderError("Anthropic 接口没有返回文本")
    return "\n".join(texts)
