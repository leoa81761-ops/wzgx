package catalog

const (
	PromptVersion   = "p0.1"
	TemplateVersion = "1"
	LegalVersion    = "0.1-draft"
)

type KV struct {
	Key   string `json:"key"`
	Label string `json:"label,omitempty"`
	Name  string `json:"name,omitempty"`
}

type Style struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type Template struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type Window struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

type PlanDefault struct {
	Tier        string
	Name        string
	PriceLabel  string
	DailyQuota  int
	PeriodQuota int
	PeriodUnit  string
	MaxAccounts int
	MaxPlans    int
	MaxWords    int
	Summary     string
}

var Stages = [][2]string{
	{"topic", "选题"},
	{"outline", "大纲"},
	{"draft", "正文"},
	{"review", "审核"},
}

var TierLabels = map[string]string{
	"free":     "免费版",
	"standard": "普通版",
	"premium":  "高级版",
	"lifetime": "买断版",
}

var Styles = []Style{
	{Key: "formal", Name: "专业严谨", Description: "书面、克制，少用感叹号和口语词，先给结论再解释。"},
	{Key: "casual", Name: "轻松口语", Description: "像跟朋友说话，句子短，可以用「你」「咱们」，不要说教。"},
	{Key: "warm", Name: "温暖亲和", Description: "语气柔和，先肯定读者，再给一个小步骤，避免制造焦虑。"},
}

var Templates = []Template{
	{Key: "simple", Name: "简约通用", Description: "白底、清晰标题，适合大多数账号。"},
	{Key: "fresh", Name: "清新", Description: "绿色点缀，留白多，适合生活与亲子内容。"},
	{Key: "business", Name: "商务", Description: "深蓝强调，适合职场与方法类内容。"},
}

var Windows = []Window{
	{Key: "night", Label: "夜间低峰 0:00–6:00", Start: 0, End: 6},
	{Key: "dawn", Label: "清晨 6:00–9:00", Start: 6, End: 9},
	{Key: "morning", Label: "上午 9:00–12:00", Start: 9, End: 12},
	{Key: "afternoon", Label: "下午 12:00–18:00", Start: 12, End: 18},
	{Key: "evening", Label: "晚间 18:00–22:00", Start: 18, End: 22},
}

var Directions = []map[string]string{
	{"key": "母婴", "name": "母婴"},
	{"key": "职场成长", "name": "职场成长"},
	{"key": "个人成长", "name": "个人成长"},
	{"key": "美食日常", "name": "美食日常"},
	{"key": "数码工具", "name": "数码工具"},
}

var Columns = []string{"知识科普", "经验清单", "避坑辟谣"}

var Angles = map[string][]string{
	"知识科普": {"先讲结论", "从一个日常场景说起", "对比两种常见做法"},
	"经验清单": {"三件今天就能做的小事", "按顺序的一周小计划", "新手最容易漏的步骤"},
	"避坑辟谣": {"一个听起来很对的说法", "为什么会有这种误解", "更稳妥的做法"},
}

var FreeTrees = map[string][]string{
	"母婴":   {"亲子阅读", "亲子游戏", "家庭日常", "外出准备"},
	"职场成长": {"沟通协作", "时间管理", "工作方法", "会议记录"},
	"个人成长": {"习惯养成", "情绪记录", "阅读方法", "休息节奏"},
	"美食日常": {"家常备菜", "厨房收纳", "一周菜单", "剩菜利用"},
	"数码工具": {"手机相册整理", "笔记软件入门", "密码管理", "文件归档"},
}

var PaidExtra = map[string][]string{
	"母婴":   {"喂养与作息", "睡眠习惯", "早教启蒙", "产后恢复"},
	"职场成长": {"职业规划", "反馈沟通", "远程协作"},
	"个人成长": {"目标拆解", "复盘方法"},
	"美食日常": {"食材挑选", "待客菜单"},
	"数码工具": {"自动化入门", "备份策略"},
}

var DefaultTree = []string{"入门概念", "实用方法", "常见误区", "案例拆解", "清单总结"}

var BlockedDirectionWords = []string{"投资理财", "股票", "虚拟币", "时政", "法律咨询", "诊断", "处方", "荐股"}

var DefaultSensitiveWords = []string{"震惊", "不转不是", "稳赚不赔", "百分百治愈", "包治百病", "必涨停", "内部消息"}

var Cliches = []string{"综上所述", "在当今社会", "不言而喻", "值得一提的是"}

var WordChoices = []int{800, 1000, 1500, 3000}

var PlanDefaults = []PlanDefault{
	{Tier: "free", Name: "免费版", PriceLabel: "0 元", DailyQuota: 1, PeriodQuota: 10, PeriodUnit: "month", MaxAccounts: 1, MaxPlans: 1, MaxWords: 1000, Summary: "每天 1 篇，每月 10 篇，可绑定 1 个公众号、1 个写作方向。夜间低峰生成。"},
	{Tier: "standard", Name: "普通版", PriceLabel: "月付 30 元 / 年付 299 元", DailyQuota: 1, PeriodQuota: 30, PeriodUnit: "month", MaxAccounts: 1, MaxPlans: 3, MaxWords: 2000, Summary: "每天 1 篇，每月 30 篇，可绑定 1 个公众号。由管理员手工开通。"},
	{Tier: "premium", Name: "高级版", PriceLabel: "月付 99 元 / 年付 990 元", DailyQuota: 5, PeriodQuota: 150, PeriodUnit: "month", MaxAccounts: 5, MaxPlans: 99, MaxWords: 4000, Summary: "每天最多 5 篇（按用户合计），每月 150 篇，最多 5 个公众号。P0 每个公众号每天仍只生成 1 批。"},
	{Tier: "lifetime", Name: "买断版", PriceLabel: "一次性 3000 元（管理员授权）", DailyQuota: 5, PeriodQuota: 365, PeriodUnit: "year", MaxAccounts: 5, MaxPlans: 99, MaxWords: 4000, Summary: "终身指服务存续期间。每天最多 5 篇（按用户合计），每周年最多 365 篇。"},
}

func StyleByKey(key string) Style {
	for _, item := range Styles {
		if item.Key == key {
			return item
		}
	}
	return Style{}
}

func TemplateByKey(key string) Template {
	for _, item := range Templates {
		if item.Key == key {
			return item
		}
	}
	return Template{}
}

func WindowHours(key string) (int, int, bool) {
	for _, item := range Windows {
		if item.Key == key {
			return item.Start, item.End, true
		}
	}
	return 0, 0, false
}

func WindowLabel(key string) string {
	for _, item := range Windows {
		if item.Key == key {
			return item.Label
		}
	}
	return key
}

func WordAllowed(n int) bool {
	for _, item := range WordChoices {
		if item == n {
			return true
		}
	}
	return false
}

func PlanFallback(tier string) PlanDefault {
	for _, item := range PlanDefaults {
		if item.Tier == tier {
			return item
		}
	}
	return PlanDefaults[0]
}
