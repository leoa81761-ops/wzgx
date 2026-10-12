package catalog

const (
	PromptVersion   = "p0.5"
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
	Tone        string `json:"tone"`
	MinTier     string `json:"min_tier"`
}

type Template struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MinTier     string `json:"min_tier"`
}

type VerticalSeed struct {
	Key         string
	Name        string
	Keywords    string
	Subtopics   string
	RedLines    string
	SourceAllow string
	ImageRules  string
	Disclaimer  string
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
	{"research", "资料检索"},
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
	{Key: "formal", Name: "专业严谨", Description: "书面、克制，少用感叹号和口语词，先给结论再解释。", Tone: "书面、第三人称或「你」，少感叹号", MinTier: "free"},
	{Key: "casual", Name: "轻松口语", Description: "像跟朋友说话，句子短，可以用「你」「咱们」，不要说教。", Tone: "短句、口语、第二人称", MinTier: "free"},
	{Key: "warm", Name: "温暖亲和", Description: "语气柔和，先肯定读者，再给一个小步骤，避免制造焦虑。", Tone: "先肯定再建议，不制造焦虑", MinTier: "free"},
	{Key: "story", Name: "故事化叙述", Description: "从一个具体场景写起，再回到方法和结论。", Tone: "场景开头，叙述收束到做法", MinTier: "standard"},
	{Key: "list", Name: "干货清单", Description: "每节先给一句结论，再补一句怎么做，适合清单栏目。", Tone: "结论前置，条目清楚", MinTier: "standard"},
	{Key: "sharp", Name: "观点犀利", Description: "观点明确，但不用标题党和绝对化用语。", Tone: "判断清楚，留有余地", MinTier: "standard"},
	{Key: "plain", Name: "科普通俗", Description: "把术语翻成日常说法，先讲是什么，再讲怎么用。", Tone: "少术语，先定义再举例", MinTier: "standard"},
}

var Templates = []Template{
	{Key: "simple", Name: "简约通用", Description: "白底、清晰标题，适合大多数账号。", MinTier: "free"},
	{Key: "fresh", Name: "清新", Description: "绿色点缀，留白多，适合生活与亲子内容。", MinTier: "free"},
	{Key: "business", Name: "商务", Description: "深蓝强调，适合职场与方法类内容。", MinTier: "free"},
	{Key: "card", Name: "卡片风", Description: "小节像卡片，适合清单和步骤。", MinTier: "standard"},
	{Key: "magazine", Name: "杂志风", Description: "标题更突出，适合叙述和观点。", MinTier: "standard"},
}

var VerticalSeeds = []VerticalSeed{
	{Key: "general", Name: "通用", Keywords: "", Subtopics: "", RedLines: "不写诊断、剂量、偏方、处方、治疗方案和收益承诺。不确定的数字不要写死。", SourceAllow: "", ImageRules: "插画或示意图，不要文字、水印和品牌标识。", Disclaimer: ""},
	{Key: "elv", Name: "弱电智能化", Keywords: "弱电,智能化,楼宇", Subtopics: "综合布线\n视频监控\n门禁对讲\n楼宇自控\n背景音乐\n机房与防雷", RedLines: "弱电指通信、安防、楼宇自控、火灾报警、音视频等电子系统，常见是安全特低电压或信号电平，不是低压配电的 1000V 门槛。没有来源就不要写标准编号、电压阈值和器件参数。", SourceAllow: "先用中文搜索。优先 gov.cn、国家标准（例如 GB 50311）、mohurd.gov.cn、samr.gov.cn 和中国行业协会，其次才是英文资料。", ImageRules: "工程示意插画，机房、线槽、摄像机或控制室，不要真实人脸，不要文字。", Disclaimer: "本文是工程科普，不能代替设计图纸和验收规范。"},
	{Key: "family", Name: "家庭生活", Keywords: "家庭,母婴,生活,美食,亲子", Subtopics: "家庭日常\n亲子阅读\n家常备菜\n收纳整理\n作息节奏", RedLines: "不写药物剂量、疾病诊断、偏方和治疗方案。不声称替代母乳，不制造育儿焦虑。", SourceAllow: "健康、喂养、疫苗只引用卫生健康主管部门、疾控机构或专业学会。", ImageRules: "家庭场景插画或物品，不生成可识别的真实儿童人脸，不要文字。", Disclaimer: "本文是家庭生活分享，请结合自己的情况判断。"},
	{Key: "tech", Name: "科技", Keywords: "科技,数码,软件,工具", Subtopics: "手机使用\n笔记软件\n备份习惯\n密码管理\n文件整理", RedLines: "不编造跑分、价格和未发布的产品参数。不承诺收益。", SourceAllow: "优先厂商文档和主流科技媒体。", ImageRules: "干净的产品与界面插画，不要文字、水印和品牌标识。", Disclaimer: ""},
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

var Cliches = []string{"综上所述", "在当今社会", "不言而喻", "值得一提的是", "结论是：", "更稳妥的做法是", "一件小事，一份真心", "安心不是一下子"}

var WordChoices = []int{800, 1000, 1500, 3000}

var PlanDefaults = []PlanDefault{
	{Tier: "free", Name: "免费版", PriceLabel: "0 元", DailyQuota: 1, PeriodQuota: 10, PeriodUnit: "month", MaxAccounts: 1, MaxPlans: 1, MaxWords: 1000, Summary: "每天 1 篇，每月 10 篇，可绑定 1 个公众号、1 个写作方向。夜间低峰生成。"},
	{Tier: "standard", Name: "普通版", PriceLabel: "月付 30 元 / 年付 299 元", DailyQuota: 1, PeriodQuota: 30, PeriodUnit: "month", MaxAccounts: 1, MaxPlans: 3, MaxWords: 2000, Summary: "每天 1 篇，每月 30 篇，可绑定 1 个公众号。由管理员手工开通。"},
	{Tier: "premium", Name: "高级版", PriceLabel: "月付 99 元 / 年付 990 元", DailyQuota: 5, PeriodQuota: 150, PeriodUnit: "month", MaxAccounts: 5, MaxPlans: 99, MaxWords: 4000, Summary: "每天最多 5 篇（按用户合计，可分配到各写作方向），每月 150 篇，最多 5 个公众号。"},
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

func TierRank(tier string) int {
	switch tier {
	case "standard":
		return 1
	case "premium":
		return 2
	case "lifetime":
		return 3
	default:
		return 0
	}
}

func TierAllows(userTier, minTier string) bool {
	if minTier == "" {
		minTier = "free"
	}
	return TierRank(userTier) >= TierRank(minTier)
}

func ImageAllowance(tier string) (aiCover bool, inline int) {
	switch tier {
	case "premium", "lifetime":
		return true, 3
	case "standard":
		return true, 1
	default:
		return false, 0
	}
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
