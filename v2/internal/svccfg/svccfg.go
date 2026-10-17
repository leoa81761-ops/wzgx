// Package svccfg is the admin config center: model and service configs, stage routing and
// connection tests. API keys are write-only: stored encrypted, shown only as a mask.
// Staff can read; only the super admin can change configs, edit routes or run tests.
package svccfg

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"gonghao/internal/audit"
	"gonghao/internal/auth"
	"gonghao/internal/crypto"
	"gonghao/internal/gateway"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrForbidden = errors.New("只有超级管理员可以修改配置")
	ErrInUse     = errors.New("该配置正在被环节路由使用，请先从路由中移除")
)

// ValidationError is shown to the admin as-is.
type ValidationError string

func (e ValidationError) Error() string { return string(e) }

type Box interface {
	Seal(string) (string, error)
	Open(string) (string, error)
}

type Service struct {
	DB    *pgxpool.Pool
	Box   Box
	Audit audit.Logger
	GW    *gateway.Gateway
}

var Kinds = []string{"text", "search", "image", "notify", "sms", "pay"}
var Stages = []string{"topic", "research", "write", "review"}

type ServiceConfig struct {
	ID              int64           `json:"id"`
	Kind            string          `json:"kind"`
	Name            string          `json:"name"`
	Protocol        string          `json:"protocol"`
	BaseURL         string          `json:"base_url"`
	KeyMask         string          `json:"key_mask"`
	HasKey          bool            `json:"has_key"`
	Model           string          `json:"model"`
	ReasoningEffort string          `json:"reasoning_effort"`
	Params          json.RawMessage `json:"params"`
	FirstByteS      int             `json:"first_byte_timeout_s"`
	IdleS           int             `json:"idle_timeout_s"`
	TotalS          int             `json:"total_timeout_s"`
	Priority        int             `json:"priority"`
	Enabled         bool            `json:"enabled"`
	UpdatedAt       time.Time       `json:"updated_at"`
	PriceIn         *float64        `json:"price_in_per_m"`  // yuan per million input tokens; nil = not set
	PriceOut        *float64        `json:"price_out_per_m"` // yuan per million output tokens
	DailyCap        *float64        `json:"daily_cap"`       // yuan per China day; nil = no cap
}

// Pricing: optional prices and daily spend cap of one config (super admin). nil clears a value.
type Pricing struct {
	PriceIn  *float64 `json:"price_in_per_m"`
	PriceOut *float64 `json:"price_out_per_m"`
	DailyCap *float64 `json:"daily_cap"`
}

func (s *Service) SavePricing(ctx context.Context, actor auth.User, id int64, p Pricing) (ServiceConfig, error) {
	if actor.Role != auth.RoleSuperAdmin {
		return ServiceConfig{}, ErrForbidden
	}
	for _, v := range []*float64{p.PriceIn, p.PriceOut} {
		if v != nil && (*v < 0 || *v > 100000) {
			return ServiceConfig{}, ValidationError("价格请填 0 到 100000 之间的数")
		}
	}
	if p.DailyCap != nil && *p.DailyCap <= 0 {
		p.DailyCap = nil // 0 or less = no cap
	}
	out, err := scan(s.DB.QueryRow(ctx, `UPDATE service_config SET price_in_per_m=$2, price_out_per_m=$3, daily_cap=$4, updated_at=now()
		WHERE id=$1 RETURNING `+cols, id, p.PriceIn, p.PriceOut, p.DailyCap))
	if err != nil {
		return out, err
	}
	s.Audit.Log(ctx, actor.ID, "config.pricing", out.Name, map[string]any{"id": id, "price_in_per_m": p.PriceIn, "price_out_per_m": p.PriceOut, "daily_cap": p.DailyCap})
	return out, nil
}

// Input for create/update. APIKey: nil keeps the stored key, a value replaces it; ClearKey removes it.
type Input struct {
	Kind            string          `json:"kind"`
	Name            string          `json:"name"`
	Protocol        string          `json:"protocol"`
	BaseURL         string          `json:"base_url"`
	APIKey          *string         `json:"api_key"`
	ClearKey        bool            `json:"clear_key"`
	Model           string          `json:"model"`
	ReasoningEffort string          `json:"reasoning_effort"`
	Params          json.RawMessage `json:"params"`
	FirstByteS      int             `json:"first_byte_timeout_s"`
	IdleS           int             `json:"idle_timeout_s"`
	TotalS          int             `json:"total_timeout_s"`
	Priority        int             `json:"priority"`
	Enabled         bool            `json:"enabled"`
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func isModelKind(k string) bool { return k == "text" || k == "search" }

func (in *Input) normalize() error {
	in.Name, in.BaseURL, in.Model = strings.TrimSpace(in.Name), strings.TrimSpace(in.BaseURL), strings.TrimSpace(in.Model)
	in.ReasoningEffort = strings.TrimSpace(in.ReasoningEffort)
	in.Protocol = strings.TrimSpace(in.Protocol)
	if !contains(Kinds, in.Kind) {
		return ValidationError("类型不正确")
	}
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 64 {
		return ValidationError("请填写名称（64 字以内）")
	}
	if in.Protocol == "" {
		in.Protocol = "openai"
	}
	if isModelKind(in.Kind) {
		if in.Protocol != "openai" && in.Protocol != "anthropic" {
			return ValidationError("模型配置只支持 openai 或 anthropic 接口")
		}
		u, err := url.Parse(in.BaseURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return ValidationError("接口地址需以 http:// 或 https:// 开头")
		}
		if in.Model == "" {
			return ValidationError("请填写模型")
		}
	}
	if utf8.RuneCountInString(in.ReasoningEffort) > 32 {
		return ValidationError("推理强度过长")
	}
	if len(in.Params) == 0 || string(in.Params) == "null" {
		in.Params = json.RawMessage(`{}`)
	}
	var p map[string]any
	if err := json.Unmarshal(in.Params, &p); err != nil {
		return ValidationError("附加参数必须是 JSON 对象")
	}
	for _, k := range []string{"model", "messages", "stream", "reasoning_effort"} {
		if _, ok := p[k]; ok {
			return ValidationError("附加参数里不能设置 " + k)
		}
	}
	if in.FirstByteS == 0 {
		in.FirstByteS = 30
	}
	if in.IdleS == 0 {
		in.IdleS = 180
	}
	if in.TotalS == 0 {
		in.TotalS = 900
	}
	if in.FirstByteS < 0 || in.IdleS < 0 || in.TotalS < 0 || in.TotalS < in.FirstByteS || in.TotalS > 3600 {
		return ValidationError("超时设置不正确（秒，总时长不小于首字节时长，最多 3600）")
	}
	if in.APIKey != nil {
		k := strings.TrimSpace(*in.APIKey)
		in.APIKey = &k
		if k == "" {
			in.APIKey = nil
		}
	}
	return nil
}

const cols = `id, kind, name, protocol, base_url, key_mask, key_cipher<>'', model, reasoning_effort, params,
	first_byte_timeout_s, idle_timeout_s, total_timeout_s, priority, enabled, updated_at,
	price_in_per_m::float8, price_out_per_m::float8, daily_cap::float8`

func scan(r pgx.Row) (ServiceConfig, error) {
	var c ServiceConfig
	err := r.Scan(&c.ID, &c.Kind, &c.Name, &c.Protocol, &c.BaseURL, &c.KeyMask, &c.HasKey, &c.Model, &c.ReasoningEffort, &c.Params,
		&c.FirstByteS, &c.IdleS, &c.TotalS, &c.Priority, &c.Enabled, &c.UpdatedAt, &c.PriceIn, &c.PriceOut, &c.DailyCap)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

func (s *Service) List(ctx context.Context, kind string) ([]ServiceConfig, error) {
	q, args := `SELECT `+cols+` FROM service_config`, []any{}
	if kind != "" {
		q += ` WHERE kind=$1`
		args = append(args, kind)
	}
	rows, err := s.DB.Query(ctx, q+` ORDER BY kind, priority, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ServiceConfig{}
	for rows.Next() {
		c, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Service) Get(ctx context.Context, id int64) (ServiceConfig, error) {
	return scan(s.DB.QueryRow(ctx, `SELECT `+cols+` FROM service_config WHERE id=$1`, id))
}

// Save creates (id=0) or updates a config. The key is sealed before it reaches the DB.
func (s *Service) Save(ctx context.Context, actor auth.User, id int64, in Input) (ServiceConfig, error) {
	if actor.Role != auth.RoleSuperAdmin {
		return ServiceConfig{}, ErrForbidden
	}
	if err := in.normalize(); err != nil {
		return ServiceConfig{}, err
	}
	var cipher, mask *string
	keyChange := "kept"
	switch {
	case in.ClearKey:
		e := ""
		cipher, mask, keyChange = &e, &e, "cleared"
	case in.APIKey != nil:
		sealed, err := s.Box.Seal(*in.APIKey)
		if err != nil {
			return ServiceConfig{}, err
		}
		m := crypto.Mask(*in.APIKey)
		cipher, mask, keyChange = &sealed, &m, "replaced"
	}
	var out ServiceConfig
	var err error
	if id == 0 {
		if cipher == nil {
			e := ""
			cipher, mask = &e, &e
		}
		out, err = scan(s.DB.QueryRow(ctx, `INSERT INTO service_config (kind, name, protocol, base_url, key_cipher, key_mask, model,
			reasoning_effort, params, first_byte_timeout_s, idle_timeout_s, total_timeout_s, priority, enabled)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING `+cols,
			in.Kind, in.Name, in.Protocol, in.BaseURL, *cipher, *mask, in.Model, in.ReasoningEffort, []byte(in.Params),
			in.FirstByteS, in.IdleS, in.TotalS, in.Priority, in.Enabled))
	} else {
		out, err = scan(s.DB.QueryRow(ctx, `UPDATE service_config SET kind=$2, name=$3, protocol=$4, base_url=$5,
			key_cipher=COALESCE($6, key_cipher), key_mask=COALESCE($7, key_mask), model=$8, reasoning_effort=$9, params=$10,
			first_byte_timeout_s=$11, idle_timeout_s=$12, total_timeout_s=$13, priority=$14, enabled=$15, updated_at=now()
			WHERE id=$1 RETURNING `+cols,
			id, in.Kind, in.Name, in.Protocol, in.BaseURL, cipher, mask, in.Model, in.ReasoningEffort, []byte(in.Params),
			in.FirstByteS, in.IdleS, in.TotalS, in.Priority, in.Enabled))
	}
	if err != nil {
		return out, err
	}
	s.Audit.Log(ctx, actor.ID, "config.save", out.Name, map[string]any{"id": out.ID, "kind": out.Kind, "model": out.Model,
		"reasoning_effort": out.ReasoningEffort, "enabled": out.Enabled, "key": keyChange})
	return out, nil
}

func (s *Service) Delete(ctx context.Context, actor auth.User, id int64) error {
	if actor.Role != auth.RoleSuperAdmin {
		return ErrForbidden
	}
	var used int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM stage_route WHERE $1 = ANY(config_ids)`, id).Scan(&used); err != nil {
		return err
	}
	if used > 0 {
		return ErrInUse
	}
	t, err := s.DB.Exec(ctx, `DELETE FROM service_config WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if t.RowsAffected() == 0 {
		return ErrNotFound
	}
	s.Audit.Log(ctx, actor.ID, "config.delete", "", map[string]any{"id": id})
	return nil
}

// ---------- stage routing ----------

type Route struct {
	Stage     string  `json:"stage"`
	Tier      string  `json:"tier"`
	ConfigIDs []int64 `json:"config_ids"` // primary first, then backups
}

func (s *Service) Routes(ctx context.Context) ([]Route, error) {
	rows, err := s.DB.Query(ctx, `SELECT stage, tier, config_ids FROM stage_route ORDER BY stage, tier`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Route{}
	for rows.Next() {
		var r Route
		if err := rows.Scan(&r.Stage, &r.Tier, &r.ConfigIDs); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Service) SaveRoute(ctx context.Context, actor auth.User, r Route) (Route, error) {
	if actor.Role != auth.RoleSuperAdmin {
		return r, ErrForbidden
	}
	r.Tier = strings.TrimSpace(r.Tier)
	if r.Tier == "" {
		r.Tier = "*"
	}
	if !contains(Stages, r.Stage) {
		return r, ValidationError("环节不正确")
	}
	if len(r.ConfigIDs) == 0 || len(r.ConfigIDs) > 5 {
		return r, ValidationError("每个环节需要 1 到 5 个配置")
	}
	seen := map[int64]bool{}
	for _, id := range r.ConfigIDs {
		if seen[id] {
			return r, ValidationError("同一配置不能重复")
		}
		seen[id] = true
		var kind string
		err := s.DB.QueryRow(ctx, `SELECT kind FROM service_config WHERE id=$1`, id).Scan(&kind)
		if errors.Is(err, pgx.ErrNoRows) {
			return r, ValidationError("配置不存在")
		}
		if err != nil {
			return r, err
		}
		if !isModelKind(kind) {
			return r, ValidationError("环节只能使用文本或搜索模型配置")
		}
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO stage_route (stage, tier, config_ids) VALUES ($1,$2,$3)
		ON CONFLICT (stage, tier) DO UPDATE SET config_ids=$3`, r.Stage, r.Tier, r.ConfigIDs); err != nil {
		return r, err
	}
	s.Audit.Log(ctx, actor.ID, "route.save", r.Stage, map[string]any{"tier": r.Tier, "config_ids": r.ConfigIDs})
	return r, nil
}

func (s *Service) DeleteRoute(ctx context.Context, actor auth.User, stage, tier string) error {
	if actor.Role != auth.RoleSuperAdmin {
		return ErrForbidden
	}
	if tier == "" {
		tier = "*"
	}
	t, err := s.DB.Exec(ctx, `DELETE FROM stage_route WHERE stage=$1 AND tier=$2`, stage, tier)
	if err != nil {
		return err
	}
	if t.RowsAffected() == 0 {
		return ErrNotFound
	}
	s.Audit.Log(ctx, actor.ID, "route.delete", stage, map[string]any{"tier": tier})
	return nil
}

// ---------- connection test ----------

type TestResult struct {
	OK       bool              `json:"ok"`
	Reply    string            `json:"reply"`
	Model    string            `json:"model"`
	Error    string            `json:"error,omitempty"`
	Attempts []gateway.Attempt `json:"attempts"`
}

// TestConnection sends one tiny streamed request through the gateway with the config's own
// timeouts and retry policy (no backup config), so the admin sees what generation would see.
func (s *Service) TestConnection(ctx context.Context, actor auth.User, id int64) (TestResult, error) {
	if actor.Role != auth.RoleSuperAdmin {
		return TestResult{}, ErrForbidden
	}
	var c gateway.Config
	var kind, keyCipher string
	var params []byte
	var fb, idle, total int
	err := s.DB.QueryRow(ctx, `SELECT id, kind, name, protocol, base_url, key_cipher, model, reasoning_effort, params,
		first_byte_timeout_s, idle_timeout_s, total_timeout_s FROM service_config WHERE id=$1`, id).
		Scan(&c.ID, &kind, &c.Name, &c.Protocol, &c.BaseURL, &keyCipher, &c.Model, &c.ReasoningEffort, &params, &fb, &idle, &total)
	if errors.Is(err, pgx.ErrNoRows) {
		return TestResult{}, ErrNotFound
	}
	if err != nil {
		return TestResult{}, err
	}
	if !isModelKind(kind) {
		return TestResult{}, ValidationError("目前只能测试文本和搜索模型配置")
	}
	if keyCipher != "" {
		if c.APIKey, err = s.Box.Open(keyCipher); err != nil {
			return TestResult{}, err
		}
	}
	_ = json.Unmarshal(params, &c.Params)
	c.FirstByte, c.Idle, c.Total = time.Duration(fb)*time.Second, time.Duration(idle)*time.Second, time.Duration(total)*time.Second

	budget := c.Total
	if budget > 5*time.Minute {
		budget = 5 * time.Minute
	}
	res, err := s.GW.Chat(ctx, []gateway.Config{c}, gateway.Request{Stage: "config_test",
		Messages: []gateway.Message{{Role: "user", Content: "连接测试，请只回复两个字母：OK"}}}, budget)
	out := TestResult{OK: err == nil, Model: res.Model, Attempts: res.Attempts}
	if err != nil {
		out.Error = err.Error()
		var fe *gateway.FailedError
		if errors.As(err, &fe) {
			out.Attempts = fe.Attempts
		}
	} else {
		r := []rune(strings.TrimSpace(res.Text))
		if len(r) > 200 {
			r = r[:200]
		}
		out.Reply = string(r)
	}
	s.Audit.Log(ctx, actor.ID, "config.test", c.Name, map[string]any{"id": id, "ok": out.OK, "attempts": len(out.Attempts)})
	return out, nil
}
