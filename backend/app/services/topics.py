from dataclasses import dataclass
from datetime import datetime, timedelta

from app.catalog import ANGLES, COLUMNS, DEFAULT_TREE, FREE_TREES, PAID_EXTRA
from app.services.schedule import as_utc


@dataclass
class TopicChoice:
    subtopic: str
    column: str
    angle: str
    topic_tag: str


@dataclass
class CoverageItem:
    subtopic: str
    column: str
    angle: str
    created_at: datetime


def subtopic_tree(direction: str, tier: str) -> list[str]:
    base = list(FREE_TREES.get(direction) or DEFAULT_TREE)
    if tier != "free":
        base.extend(PAID_EXTRA.get(direction) or [])
    # 去重并保持顺序
    seen = set()
    ordered = []
    for item in base:
        if item not in seen:
            seen.add(item)
            ordered.append(item)
    return ordered


def pick_topic(direction: str, tier: str, history: list[CoverageItem], now: datetime) -> TopicChoice:
    tree = subtopic_tree(direction, tier)
    combos: list[tuple[str, str, str]] = []
    for subtopic in tree:
        for column in COLUMNS:
            for angle in ANGLES[column]:
                combos.append((subtopic, column, angle))
    cutoff = as_utc(now) - timedelta(days=90)
    recent = {
        (item.subtopic, item.column, item.angle)
        for item in history
        if as_utc(item.created_at) >= cutoff
    }
    start = len(history)
    for offset in range(len(combos)):
        subtopic, column, angle = combos[(start + offset) % len(combos)]
        if (subtopic, column, angle) not in recent:
            return _choice(subtopic, column, angle)
    # 90 天内的组合都用过时，复用最早的一条，避免把用户卡住。
    if history:
        oldest = min(history, key=lambda item: as_utc(item.created_at))
        return _choice(oldest.subtopic, oldest.column, oldest.angle)
    subtopic, column, angle = combos[0]
    return _choice(subtopic, column, angle)


def _choice(subtopic: str, column: str, angle: str) -> TopicChoice:
    return TopicChoice(
        subtopic=subtopic,
        column=column,
        angle=angle,
        topic_tag=f"{subtopic}|{column}|{angle}",
    )
