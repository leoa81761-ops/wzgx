package model

import "time"

type User struct {
	ID            int64
	Email         string
	Phone         string
	PasswordHash  string
	Tier          string
	TierExpiresAt *time.Time
	TierStartedAt *time.Time
	IsAdmin       bool
	IsActive      bool
	QuotaExempt   bool
	CreatedAt     time.Time
}

type WxAccount struct {
	ID               int64
	UserID           int64
	Name             string
	AppID            string
	SecretEncrypted  string
	SubjectType      string
	ServiceType      string
	Verified         bool
	DeliveryMode     string
	BindStatus       string
	PermissionReport map[string]any
	BoundAt          *time.Time
	UnboundAt        *time.Time
	Audience         string
	Tone             string
	PersonaColumns   string
	Signatures       string
	Taboos           string
}

type WritingPlan struct {
	ID           int64
	UserID       int64
	WxAccountID  int64
	Direction    string
	Mode         string
	StyleKey     string
	TemplateKey  string
	WordTarget   int
	ScheduleType string
	Weekdays     []int
	TimeWindow   string
	Status       string
	PauseReason  string
	NextRunAt    *time.Time
	DailyCount   int
	ConfirmMode  bool
	CreatedAt    time.Time
}

// ArticlesPerDay 是这个方向每天计划生成的篇数。旧数据没有这个字段时按 1 篇。
func (p *WritingPlan) ArticlesPerDay() int {
	if p == nil || p.DailyCount < 1 {
		return 1
	}
	return p.DailyCount
}

type ArticleTask struct {
	ID               int64
	PlanID           int64
	UserID           int64
	WxAccountID      int64
	ArticleID        *int64
	RegenOfArticleID *int64
	IdempotencyKey   string
	TriggerType      string
	ScheduleDate     string
	RunAt            time.Time
	Status           string
	FailureReason    string
	RetryCount       int
	Priority         int
	StartedAt        *time.Time
	ProgressAt       *time.Time
	FinishedAt       *time.Time
	DurationMs       int
	Tokens           int
	ModelTrace       []map[string]any
	CreatedAt        time.Time
	SkipDraft        int
}

type Article struct {
	ID              int64
	TaskID          int64
	UserID          int64
	PlanID          int64
	WxAccountID     int64
	Title           string
	AltTitles       []any
	Summary         string
	BodyJSON        map[string]any
	BodyHTML        string
	Sources         []any
	ThumbMediaID    string
	DraftMediaID    string
	TemplateKey     string
	TemplateVersion string
	StyleKey        string
	PromptVersion   string
	RiskLevel       string
	Status          string
	FailureReason   string
	DeliveryNote    string
	RegenCount      int
	Subtopic        string
	TopicTag        string
	ColumnType      string
	CreatedAt       time.Time
}

type TopicCoverage struct {
	ID         int64
	PlanID     int64
	Subtopic   string
	ColumnType string
	Angle      string
	TopicTag   string
	CreatedAt  time.Time
}

type QuotaHold struct {
	ID        int64
	UserID    int64
	TaskID    int64
	DayKey    string
	PeriodKey string
	Amount    int
	State     string
	CreatedAt time.Time
}

type PlanConfig struct {
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

type ServiceConfig struct {
	ID              int64
	ConfigType      string
	Name            string
	Protocol        string
	BaseURL         string
	APIKeyEncrypted string
	ModelName       string
	ExtraHeaders    map[string]string
	ExtraParams     map[string]any
	Priority        int
	Enabled         bool
	TimeoutSeconds  int
	UpdatedAt       time.Time
}

type StageRoute struct {
	ID              int64
	Stage           string
	Tier            string
	ServiceConfigID int64
	SortOrder       int
}

type LegalDoc struct {
	ID            int64
	DocType       string
	Version       string
	Title         string
	Content       string
	Status        string
	EffectiveDate string
}

type Consent struct {
	ID          int64
	UserID      int64
	DocType     string
	Version     string
	ConsentItem string
	IP          string
	UserAgent   string
	CreatedAt   time.Time
	WithdrawnAt *time.Time
}

type AuditLog struct {
	ID        int64
	ActorID   *int64
	Action    string
	Target    string
	Detail    string
	CreatedAt time.Time
}

type ArticleSource struct {
	ArticleID   int64
	Seq         int
	SourceType  string
	Name        string
	Title       string
	PublishedOn string
	RawURL      string
	DisplayURL  string
	Accessible  string
}
