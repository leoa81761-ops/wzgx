import html

THEMES = {
    "simple": {
        "wrap": "font-size:16px;line-height:1.75;color:#1f2933;",
        "h2": "font-size:18px;line-height:1.45;font-weight:700;margin:28px 0 12px;color:#1f2933;",
        "p": "margin:0 0 14px;font-size:16px;line-height:1.75;color:#1f2933;",
        "lead": "margin:0 0 18px;font-size:15px;line-height:1.7;color:#5c5148;",
        "ul": "margin:0 0 16px;padding-left:1.2em;",
        "li": "margin:0 0 8px;line-height:1.7;color:#1f2933;",
        "hr": "border:none;border-top:1px solid #e6dccf;margin:22px 0;",
        "src_h": "font-size:15px;font-weight:700;margin:8px 0;color:#1f2933;",
        "src": "font-size:13px;line-height:1.65;color:#6b6258;margin:0 0 8px;",
        "foot": "font-size:13px;line-height:1.65;color:#8a8178;margin:8px 0 0;",
    },
    "fresh": {
        "wrap": "font-size:16px;line-height:1.75;color:#24312c;",
        "h2": "font-size:18px;line-height:1.45;font-weight:700;margin:28px 0 12px;color:#1f6a4d;",
        "p": "margin:0 0 14px;font-size:16px;line-height:1.75;color:#24312c;",
        "lead": "margin:0 0 18px;font-size:15px;line-height:1.7;color:#3e5c50;",
        "ul": "margin:0 0 16px;padding-left:1.2em;",
        "li": "margin:0 0 8px;line-height:1.7;color:#24312c;",
        "hr": "border:none;border-top:1px solid #d5e6dc;margin:22px 0;",
        "src_h": "font-size:15px;font-weight:700;margin:8px 0;color:#1f6a4d;",
        "src": "font-size:13px;line-height:1.65;color:#4d675c;margin:0 0 8px;",
        "foot": "font-size:13px;line-height:1.65;color:#6d7f76;margin:8px 0 0;",
    },
    "business": {
        "wrap": "font-size:16px;line-height:1.75;color:#1c2834;",
        "h2": "font-size:18px;line-height:1.45;font-weight:700;margin:28px 0 12px;color:#1e3a5f;",
        "p": "margin:0 0 14px;font-size:16px;line-height:1.75;color:#1c2834;",
        "lead": "margin:0 0 18px;font-size:15px;line-height:1.7;color:#3d4d60;",
        "ul": "margin:0 0 16px;padding-left:1.2em;",
        "li": "margin:0 0 8px;line-height:1.7;color:#1c2834;",
        "hr": "border:none;border-top:1px solid #d5deea;margin:22px 0;",
        "src_h": "font-size:15px;font-weight:700;margin:8px 0;color:#1e3a5f;",
        "src": "font-size:13px;line-height:1.65;color:#546274;margin:0 0 8px;",
        "foot": "font-size:13px;line-height:1.65;color:#6a7684;margin:8px 0 0;",
    },
}

DARK = {
    "wrap": "background:#1c1917;padding:16px;color:#f5f0e8;",
    "text": "#f5f0e8",
    "muted": "#d9cfc3",
    "line": "#3a332c",
    "accent": "#e7b8a8",
}


def display_url(url: str) -> str:
    return (url or "").replace(".", "点")


def render_html(article: dict, template_key: str, *, dark: bool = False, compact: bool = False) -> str:
    theme = dict(THEMES.get(template_key) or THEMES["simple"])
    if compact:
        theme["p"] = "margin:0 0 10px;font-size:15px;line-height:1.65;color:#1f2933;"
        theme["h2"] = "font-size:17px;font-weight:700;margin:16px 0 8px;color:#1f2933;"
    if dark:
        theme["wrap"] = f"font-size:16px;line-height:1.75;color:{DARK['text']};background:#1c1917;padding:12px;"
        theme["h2"] = theme["h2"].replace("#1f2933", DARK["accent"]).replace("#1f6a4d", DARK["accent"]).replace("#1e3a5f", DARK["accent"])
        theme["p"] = f"margin:0 0 14px;font-size:16px;line-height:1.75;color:{DARK['text']};"
        theme["lead"] = f"margin:0 0 18px;font-size:15px;line-height:1.7;color:{DARK['muted']};"
        theme["li"] = f"margin:0 0 8px;line-height:1.7;color:{DARK['text']};"
        theme["hr"] = f"border:none;border-top:1px solid {DARK['line']};margin:22px 0;"
        theme["src_h"] = f"font-size:15px;font-weight:700;margin:8px 0;color:{DARK['accent']};"
        theme["src"] = f"font-size:13px;line-height:1.65;color:{DARK['muted']};margin:0 0 8px;"
        theme["foot"] = f"font-size:13px;line-height:1.65;color:{DARK['muted']};margin:8px 0 0;"

    parts = [f'<section style="{theme["wrap"]}">']
    summary = (article.get("summary") or "").strip()
    if summary:
        parts.append(f'<p style="{theme["lead"]}">{html.escape(summary)}</p>')
    for section in article.get("sections") or []:
        heading = (section.get("heading") or "").strip()
        if heading:
            parts.append(f'<h2 style="{theme["h2"]}">{html.escape(heading)}</h2>')
        for paragraph in section.get("paragraphs") or []:
            text = str(paragraph).strip()
            if text:
                parts.append(f'<p style="{theme["p"]}">{html.escape(text)}</p>')
        items = [str(item).strip() for item in (section.get("list") or []) if str(item).strip()]
        if items and not compact:
            lis = "".join(f'<li style="{theme["li"]}">{html.escape(item)}</li>' for item in items)
            parts.append(f'<ul style="{theme["ul"]}">{lis}</ul>')
    closing = (article.get("closing") or "").strip()
    if closing:
        parts.append(f'<p style="{theme["p"]}">{html.escape(closing)}</p>')
    sources = article.get("sources") or []
    if sources:
        parts.append(f'<hr style="{theme["hr"]}" />')
        parts.append(f'<p style="{theme["src_h"]}">参考来源</p>')
        for index, source in enumerate(sources, start=1):
            parts.append(f'<p style="{theme["src"]}">{html.escape(_source_line(index, source))}</p>')
    footnotes = article.get("footnotes") or []
    if footnotes:
        parts.append(f'<hr style="{theme["hr"]}" />')
        for note in footnotes:
            parts.append(f'<p style="{theme["foot"]}">{html.escape(str(note))}</p>')
    parts.append("</section>")
    return "".join(parts)


def render_within_limit(article: dict, template_key: str, *, dark: bool = False) -> str:
    html_text = render_html(article, template_key, dark=dark)
    if len(html_text) <= 20000:
        return html_text
    compact = render_html(article, "simple", dark=dark, compact=True)
    if len(compact) <= 20000:
        return compact
    raise ValueError("正文超过微信草稿长度上限（2 万字符），请缩短篇幅后重试")


def _source_line(index: int, source: dict) -> str:
    name = source.get("name") or ""
    title = source.get("title") or ""
    date = source.get("published_on") or ""
    url = display_url(source.get("url") or "")
    bits = [f"[{index}]"]
    if name:
        bits.append(name)
    if title:
        bits.append(f"《{title}》")
    if date:
        bits.append(date)
    if url:
        bits.append(url)
    return " ".join(bits)
