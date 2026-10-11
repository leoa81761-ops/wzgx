"""P0 内置目录。风格与模板的可视化编辑留到 P1。"""

STAGES = [
    ("topic", "选题"),
    ("outline", "大纲"),
    ("draft", "正文"),
    ("review", "审核"),
]

TIERS = [
    ("free", "免费版"),
    ("standard", "普通版"),
    ("premium", "高级版"),
    ("lifetime", "买断版"),
]

TIER_LABELS = dict(TIERS)

STYLES = [
    {
        "key": "formal",
        "name": "专业严谨",
        "description": "书面、克制，少用感叹号和口语词，先给结论再解释。",
    },
    {
        "key": "casual",
        "name": "轻松口语",
        "description": "像跟朋友说话，句子短，可以用「你」「咱们」，不要说教。",
    },
    {
        "key": "warm",
        "name": "温暖亲和",
        "description": "语气柔和，先肯定读者，再给一个小步骤，避免制造焦虑。",
    },
]

STYLE_MAP = {item["key"]: item for item in STYLES}

TEMPLATES = [
    {"key": "simple", "name": "简约通用", "description": "白底、清晰标题，适合大多数账号。"},
    {"key": "fresh", "name": "清新", "description": "绿色点缀，留白多，适合生活与亲子内容。"},
    {"key": "business", "name": "商务", "description": "深蓝强调，适合职场与方法类内容。"},
]

TEMPLATE_MAP = {item["key"]: item for item in TEMPLATES}

WINDOWS = [
    {"key": "night", "label": "夜间低峰 0:00–6:00", "start": 0, "end": 6},
    {"key": "dawn", "label": "清晨 6:00–9:00", "start": 6, "end": 9},
    {"key": "morning", "label": "上午 9:00–12:00", "start": 9, "end": 12},
    {"key": "afternoon", "label": "下午 12:00–18:00", "start": 12, "end": 18},
    {"key": "evening", "label": "晚间 18:00–22:00", "start": 18, "end": 22},
]

WINDOW_HOURS = {item["key"]: (item["start"], item["end"]) for item in WINDOWS}
WINDOW_LABELS = {item["key"]: item["label"] for item in WINDOWS}

DIRECTIONS = [
    {"key": "母婴", "name": "母婴"},
    {"key": "职场成长", "name": "职场成长"},
    {"key": "个人成长", "name": "个人成长"},
    {"key": "美食日常", "name": "美食日常"},
    {"key": "数码工具", "name": "数码工具"},
]

COLUMNS = ["知识科普", "经验清单", "避坑辟谣"]

ANGLES = {
    "知识科普": ["先讲结论", "从一个日常场景说起", "对比两种常见做法"],
    "经验清单": ["三件今天就能做的小事", "按顺序的一周小计划", "新手最容易漏的步骤"],
    "避坑辟谣": ["一个听起来很对的说法", "为什么会有这种误解", "更稳妥的做法"],
}

FREE_TREES = {
    "母婴": ["亲子阅读", "亲子游戏", "家庭日常", "外出准备"],
    "职场成长": ["沟通协作", "时间管理", "工作方法", "会议记录"],
    "个人成长": ["习惯养成", "情绪记录", "阅读方法", "休息节奏"],
    "美食日常": ["家常备菜", "厨房收纳", "一周菜单", "剩菜利用"],
    "数码工具": ["手机相册整理", "笔记软件入门", "密码管理", "文件归档"],
}

PAID_EXTRA = {
    "母婴": ["喂养与作息", "睡眠习惯", "早教启蒙", "产后恢复"],
    "职场成长": ["职业规划", "反馈沟通", "远程协作"],
    "个人成长": ["目标拆解", "复盘方法"],
    "美食日常": ["食材挑选", "待客菜单"],
    "数码工具": ["自动化入门", "备份策略"],
}

DEFAULT_TREE = ["入门概念", "实用方法", "常见误区", "案例拆解", "清单总结"]

BLOCKED_DIRECTION_WORDS = ["投资理财", "股票", "虚拟币", "时政", "法律咨询", "诊断", "处方", "荐股"]

DEFAULT_SENSITIVE_WORDS = [
    "震惊",
    "不转不是",
    "稳赚不赔",
    "百分百治愈",
    "包治百病",
    "必涨停",
    "内部消息",
]

CLICHES = ["综上所述", "在当今社会", "不言而喻", "值得一提的是"]

WORD_CHOICES = [800, 1000, 1500, 3000]

PROMPT_VERSION = "p0.1"
TEMPLATE_VERSION = "1"
LEGAL_VERSION = "0.1-draft"

PLAN_DEFAULTS = [
    {
        "tier": "free",
        "name": "免费版",
        "price_label": "0 元",
        "daily_quota": 1,
        "period_quota": 10,
        "period_unit": "month",
        "max_accounts": 1,
        "max_plans": 1,
        "max_words": 1000,
        "summary": "每天 1 篇，每月 10 篇，可绑定 1 个公众号、1 个写作方向。夜间低峰生成。",
    },
    {
        "tier": "standard",
        "name": "普通版",
        "price_label": "月付 30 元 / 年付 299 元",
        "daily_quota": 1,
        "period_quota": 30,
        "period_unit": "month",
        "max_accounts": 1,
        "max_plans": 3,
        "max_words": 2000,
        "summary": "每天 1 篇，每月 30 篇，可绑定 1 个公众号。由管理员手工开通。",
    },
    {
        "tier": "premium",
        "name": "高级版",
        "price_label": "月付 99 元 / 年付 990 元",
        "daily_quota": 5,
        "period_quota": 150,
        "period_unit": "month",
        "max_accounts": 5,
        "max_plans": 99,
        "max_words": 4000,
        "summary": "每天最多 5 篇（按用户合计），每月 150 篇，最多 5 个公众号。P0 每个公众号每天仍只生成 1 批。",
    },
    {
        "tier": "lifetime",
        "name": "买断版",
        "price_label": "一次性 3000 元（管理员授权）",
        "daily_quota": 5,
        "period_quota": 365,
        "period_unit": "year",
        "max_accounts": 5,
        "max_plans": 99,
        "max_words": 4000,
        "summary": "终身指服务存续期间。每天最多 5 篇（按用户合计），每周年最多 365 篇。",
    },
]
