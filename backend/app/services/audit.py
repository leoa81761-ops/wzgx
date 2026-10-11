from app.catalog import CLICHES, DEFAULT_SENSITIVE_WORDS


def effective_words(extra: list[str] | None) -> list[str]:
    words = list(DEFAULT_SENSITIVE_WORDS)
    for word in extra or []:
        cleaned = str(word).strip()
        if cleaned and cleaned not in words:
            words.append(cleaned)
    return words


def find_sensitive(payload: dict, words: list[str]) -> list[str]:
    text = _flatten(payload)
    return [word for word in words if word and word in text]


def strip_cliches(value):
    if isinstance(value, str):
        for phrase in CLICHES:
            value = value.replace(phrase, "")
        return value
    if isinstance(value, list):
        return [strip_cliches(item) for item in value]
    if isinstance(value, dict):
        return {key: strip_cliches(item) for key, item in value.items()}
    return value


def _flatten(value) -> str:
    if isinstance(value, str):
        return value
    if isinstance(value, dict):
        return "\n".join(_flatten(item) for item in value.values())
    if isinstance(value, list):
        return "\n".join(_flatten(item) for item in value)
    return ""
