from datetime import datetime, time, timedelta, timezone
from zoneinfo import ZoneInfo

from app.catalog import WINDOW_HOURS

SH = ZoneInfo("Asia/Shanghai")
UTC = timezone.utc


def as_utc(value: datetime | None) -> datetime | None:
    if value is None:
        return None
    if value.tzinfo is None:
        return value.replace(tzinfo=UTC)
    return value.astimezone(UTC)


def shanghai_now(now: datetime | None = None) -> datetime:
    current = now or datetime.now(UTC)
    current = as_utc(current)
    return current.astimezone(SH)


def shanghai_day(now: datetime | None = None) -> str:
    return shanghai_now(now).strftime("%Y-%m-%d")


def tomorrow_start(now: datetime) -> datetime:
    local = shanghai_now(now) + timedelta(days=1)
    start = local.replace(hour=0, minute=0, second=0, microsecond=0)
    return start.astimezone(UTC)


def window_end(day_key: str, window_key: str) -> datetime:
    _start, end_h = WINDOW_HOURS[window_key]
    year, month, day = (int(part) for part in day_key.split("-"))
    local = datetime(year, month, day, end_h, 0, tzinfo=SH)
    return local.astimezone(UTC)


class ZeroRng:
    def randrange(self, _n: int) -> int:
        return 0


def compute_next_run(
    after: datetime,
    *,
    schedule_type: str,
    weekdays: list[int] | None,
    window_key: str,
    rng,
) -> datetime:
    if window_key not in WINDOW_HOURS:
        raise ValueError("未知时段")
    after_sh = shanghai_now(after)
    start_h, end_h = WINDOW_HOURS[window_key]
    days = weekdays or []
    for offset in range(0, 28):
        day = after_sh.date() + timedelta(days=offset)
        if schedule_type == "weekly" and day.isoweekday() not in days:
            continue
        window_start = datetime.combine(day, time(start_h, 0), tzinfo=SH)
        window_end_at = datetime.combine(day, time(end_h, 0), tzinfo=SH)
        if after_sh >= window_end_at and day == after_sh.date():
            continue
        earliest = window_start
        if day == after_sh.date() and after_sh > window_start:
            earliest = (after_sh + timedelta(minutes=1)).replace(second=0, microsecond=0)
        if earliest >= window_end_at:
            continue
        minutes = int((window_end_at - earliest).total_seconds() // 60)
        if minutes <= 0:
            continue
        pick = earliest + timedelta(minutes=rng.randrange(minutes))
        return pick.astimezone(UTC)
    raise RuntimeError("未来四周没有可执行时段")


def should_retry(trigger_type: str, retry_count: int, now: datetime, window_end_at: datetime) -> bool:
    return trigger_type == "schedule" and retry_count < 2 and as_utc(now) < as_utc(window_end_at)
