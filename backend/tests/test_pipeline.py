import json
from datetime import datetime, timedelta, timezone

import pytest

from app.db import session_scope
from app.models import Article, ArticleTask, TopicCoverage, User, WritingPlan
from app.services.jobs import create_generation_task
from app.services.llm import Completion, ModelGateway
from app.services.pipeline import execute_task
from app.services.quota import usage_counts
from app.services.renderer import display_url, render_html
from app.services.schedule import shanghai_day
from tests.conftest import make_account_plan

UTC = timezone.utc


def test_renderer_inlines_styles_and_rewrites_urls():
    html = render_html(
        {
            "summary": "导语",
            "sections": [{"heading": "小节 <b>", "paragraphs": ["正文"], "list": ["一条"]}],
            "closing": "结尾",
            "sources": [{"name": "示例媒体", "title": "一条报道", "published_on": "2026-01-01", "url": "https://www.example.com/a.b"}],
            "footnotes": ["请发布前审阅。"],
        },
        "fresh",
    )
    assert "style=" in html
    assert "class=" not in html
    assert "<b>" not in html
    assert "参考来源" in html
    assert "www.example.com" not in html
    assert "www点example点com/a点b" in html
    assert display_url("a.b.c") == "a点b点c"


@pytest.mark.asyncio
async def test_pipeline_reaches_draft_and_rotates_topics(settings):
    yesterday = datetime.now(UTC) - timedelta(days=1)
    user_id, account_id, plan_id = await make_account_plan(settings, direction="职场成长")
    first = await _run(settings, user_id, account_id, plan_id, yesterday)
    second = await _run(settings, user_id, account_id, plan_id, datetime.now(UTC))
    async with session_scope() as session:
        articles = (await session.scalars(__import__("sqlalchemy").select(Article).order_by(Article.id.asc()))).all()
        assert len(articles) == 2
        assert articles[0].status == "in_draft"
        assert articles[0].draft_media_id.startswith("mock_draft_")
        assert "style=" in articles[0].body_html
        assert "温暖亲和" in articles[0].body_html
        assert "#1f6a4d" in articles[0].body_html
        assert articles[0].topic_tag != articles[1].topic_tag
        coverage = (await session.scalars(__import__("sqlalchemy").select(TopicCoverage))).all()
        assert len(coverage) == 2
        user = await session.get(User, user_id)
        _daily, period = await usage_counts(session, user.id, shanghai_day(datetime.now(UTC)), shanghai_day(datetime.now(UTC))[:7])
        assert period == 2
    assert first != second


@pytest.mark.asyncio
async def test_sensitive_failure_releases_quota(settings, monkeypatch):
    user_id, account_id, plan_id = await make_account_plan(settings, email="bad@example.com")

    class BadAdapter:
        async def complete(self, provider, messages):
            payload = json.loads(messages[-1]["content"])
            stage = payload["环节"]
            if stage == "选题":
                data = {"title": "一个正常选题"}
            elif stage == "大纲":
                data = {"points": [{"heading": "甲", "intent": "说明"}, {"heading": "乙", "intent": "说明"}], "closing": "结束"}
            else:
                data = {
                    "title": "震惊内幕",
                    "summary": "摘要",
                    "sections": [{"heading": "甲", "paragraphs": ["稳赚不赔"], "list": []}],
                    "sources": [],
                }
            return Completion(content=json.dumps(data, ensure_ascii=False), attempts=[{"ok": True, "protocol": "openai", "provider_id": 0}])

    monkeypatch.setattr("app.services.pipeline.build_gateway", lambda *_args, **_kwargs: ModelGateway({"openai": BadAdapter(), "anthropic": BadAdapter()}))
    now = datetime.now(UTC)
    async with session_scope() as session:
        user = await session.get(User, user_id)
        plan = await session.get(WritingPlan, plan_id)
        from app.models import WxAccount

        account = await session.get(WxAccount, account_id)
        task = await create_generation_task(session, user=user, plan=plan, account=account, trigger_type="manual", now=now)
        task_id = task.id
    await execute_task(task_id, settings, claim=True, now=now)
    async with session_scope() as session:
        task = await session.get(ArticleTask, task_id)
        article = await session.get(Article, task.article_id)
        assert task.status == "failed"
        assert "敏感词" in article.failure_reason
        user = await session.get(User, user_id)
        daily, _period = await usage_counts(session, user.id, shanghai_day(now), shanghai_day(now)[:7])
        assert daily == 0


async def _run(settings, user_id, account_id, plan_id, now):
    async with session_scope() as session:
        user = await session.get(User, user_id)
        plan = await session.get(WritingPlan, plan_id)
        from app.models import WxAccount

        account = await session.get(WxAccount, account_id)
        task = await create_generation_task(session, user=user, plan=plan, account=account, trigger_type="manual", now=now)
        task_id = task.id
    await execute_task(task_id, settings, claim=True, now=now)
    async with session_scope() as session:
        task = await session.get(ArticleTask, task_id)
        article = await session.get(Article, task.article_id)
        assert article.status == "in_draft"
        return article.topic_tag
