// Package style learns a personal writing style from 3–5 sample articles the user pastes.
// One model call extracts style traits only (tone, sentence length, structure habits, vocabulary);
// only the traits are stored, never the samples, and the writer gets them as user settings.
// A profile covers a whole account or one column, and each has its own on/off switch.
package style

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"gonghao/internal/audit"
	"gonghao/internal/auth"
	"gonghao/internal/gateway"
	"gonghao/internal/server"
)

type Service struct {
	DB    *pgxpool.Pool
	GW    *gateway.Gateway
	Box   gateway.Opener
	Audit audit.Logger
}

// Traits are the only thing kept from the samples.
type Traits struct {
	Tone       string `json:"tone"`       // 语气
	Sentence   string `json:"sentence"`   // 句子
	Structure  string `json:"structure"`  // 结构
	Vocabulary string `json:"vocabulary"` // 用词
	Other      string `json:"other"`      // 其他
}

// Text is how the writer sees the traits (one line of the user settings).
func (t Traits) Text() string {
	var parts []string
	for _, p := range [][2]string{{"语气", t.Tone}, {"句子", t.Sentence}, {"结构", t.Structure}, {"用词", t.Vocabulary}, {"其他", t.Other}} {
		if v := strings.TrimSpace(p[1]); v != "" {
			parts = append(parts, p[0]+"："+v)
		}
	}
	return strings.Join(parts, "；")
}

type Profile struct {
	ID        int64     `json:"id"`
	ColumnID  *int64    `json:"column_id"`
	Column    string    `json:"column_name"`
	Traits    Traits    `json:"traits"`
	Samples   int       `json:"samples"`
	Enabled   bool      `json:"enabled"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ValidationError string

func (e ValidationError) Error() string { return string(e) }

var ErrNotFound = errors.New("不存在")

const (
	minSamples, maxSamples = 3, 5
	minRunes, maxRunes     = 200, 20000
	maxTrait               = 120
	copyWindow             = 12 // a trait sharing this many consecutive characters with a sample is dropped
)

const extractSystem = `你分析几篇文章共同的写作风格。只描述风格特征，不复述内容：不要引用或改写原文句子，不要写出文章里的具体人名、地名、机构、数字、事件或观点。
只输出 JSON：{"tone":"语气和口吻","sentence":"句子长短和节奏","structure":"开头、分段、小标题、结尾的习惯","vocabulary":"用词习惯，比如口语化程度、称呼读者的方式","other":"其他明显的风格习惯，没有就留空"}
每一项不超过 60 字。`

// Extract runs the one model call and returns the traits. The samples stay in memory only.
func (s *Service) Extract(ctx context.Context, tier string, samples []string) (Traits, error) {
	if len(samples) < minSamples || len(samples) > maxSamples {
		return Traits{}, ValidationError(fmt.Sprintf("请贴 %d 到 %d 篇范文", minSamples, maxSamples))
	}
	for i, x := range samples {
		samples[i] = strings.TrimSpace(x)
		if n := utf8.RuneCountInString(samples[i]); n < minRunes || n > maxRunes {
			return Traits{}, ValidationError(fmt.Sprintf("第 %d 篇长度不合适：每篇 %d 到 %d 字（现在 %d 字）", i+1, minRunes, maxRunes, n))
		}
	}
	cfgs, err := gateway.LoadStage(ctx, s.DB, s.Box, "topic", tier)
	if err != nil {
		return Traits{}, err
	}
	var b strings.Builder
	for i, x := range samples {
		fmt.Fprintf(&b, "【范文 %d】\n%s\n\n", i+1, x)
	}
	res, err := s.GW.Chat(ctx, cfgs, gateway.Request{Stage: "style", JSON: true,
		Messages: []gateway.Message{{Role: "system", Content: extractSystem}, {Role: "user", Content: b.String()}}}, 3*time.Minute)
	if err != nil {
		return Traits{}, fmt.Errorf("提炼风格失败，请稍后再试：%w", err)
	}
	var t Traits
	text := res.Text
	if i, j := strings.Index(text, "{"), strings.LastIndex(text, "}"); i >= 0 && j > i {
		text = text[i : j+1]
	}
	if json.Unmarshal([]byte(text), &t) != nil {
		return Traits{}, errors.New("提炼风格失败：模型回复不是约定的格式，请再试一次")
	}
	all := strings.Join(samples, "\n")
	for _, f := range []*string{&t.Tone, &t.Sentence, &t.Structure, &t.Vocabulary, &t.Other} {
		*f = clip(strings.TrimSpace(*f), maxTrait)
		if copied(*f, all) {
			*f = "" // looks like source text, not a trait
		}
	}
	if t.Text() == "" {
		return Traits{}, errors.New("没有提炼出风格特征，换几篇更有代表性的范文试试")
	}
	return t, nil
}

// copied reports whether s shares copyWindow consecutive characters (ignoring spaces) with src.
func copied(s, src string) bool {
	r := []rune(strings.Join(strings.Fields(s), ""))
	src = strings.Join(strings.Fields(src), "")
	for i := 0; i+copyWindow <= len(r); i++ {
		if strings.Contains(src, string(r[i:i+copyWindow])) {
			return true
		}
	}
	return false
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// owned checks the account belongs to the user and returns the user's tier.
func (s *Service) owned(ctx context.Context, actor auth.User, accID int64) (string, error) {
	var tier string
	err := s.DB.QueryRow(ctx, `SELECT u.tier FROM wx_account a JOIN users u ON u.id=a.user_id WHERE a.id=$1 AND a.user_id=$2`, accID, actor.ID).Scan(&tier)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return tier, err
}

// Learn extracts traits from the samples and saves them for the account or one of its columns
// (replacing that scope's previous profile). The samples are not stored or logged.
func (s *Service) Learn(ctx context.Context, actor auth.User, accID int64, columnID *int64, samples []string) (Profile, error) {
	tier, err := s.owned(ctx, actor, accID)
	if err != nil {
		return Profile{}, err
	}
	if columnID != nil {
		var ok bool
		s.DB.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM account_column WHERE id=$1 AND account_id=$2)`, *columnID, accID).Scan(&ok)
		if !ok {
			return Profile{}, ValidationError("栏目不存在")
		}
	}
	t, err := s.Extract(ctx, tier, samples)
	if err != nil {
		return Profile{}, err
	}
	tj, _ := json.Marshal(t)
	var id int64
	err = s.DB.QueryRow(ctx, `INSERT INTO style_profile (account_id, column_id, traits, samples) VALUES ($1,$2,$3,$4)
		ON CONFLICT (account_id, COALESCE(column_id, 0)) DO UPDATE SET traits=$3, samples=$4, enabled=TRUE, updated_at=now() RETURNING id`,
		accID, columnID, tj, len(samples)).Scan(&id)
	if err != nil {
		return Profile{}, err
	}
	s.Audit.Log(ctx, actor.ID, "style.learn", fmt.Sprint(accID), map[string]any{"account_id": accID, "column_id": columnID, "samples": len(samples)})
	return s.get(ctx, accID, id)
}

const profCols = `p.id, p.column_id, COALESCE(c.name,''), p.traits, p.samples, p.enabled, p.updated_at`

func scanProfile(r pgx.Row) (Profile, error) {
	var p Profile
	var tj []byte
	err := r.Scan(&p.ID, &p.ColumnID, &p.Column, &tj, &p.Samples, &p.Enabled, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	if err == nil {
		json.Unmarshal(tj, &p.Traits)
	}
	return p, err
}

func (s *Service) get(ctx context.Context, accID, id int64) (Profile, error) {
	return scanProfile(s.DB.QueryRow(ctx, `SELECT `+profCols+` FROM style_profile p LEFT JOIN account_column c ON c.id=p.column_id
		WHERE p.id=$1 AND p.account_id=$2`, id, accID))
}

func (s *Service) List(ctx context.Context, actor auth.User, accID int64) ([]Profile, error) {
	if _, err := s.owned(ctx, actor, accID); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT `+profCols+` FROM style_profile p LEFT JOIN account_column c ON c.id=p.column_id
		WHERE p.account_id=$1 ORDER BY p.column_id NULLS FIRST, p.id`, accID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Profile{}
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Service) SetEnabled(ctx context.Context, actor auth.User, accID, id int64, on bool) (Profile, error) {
	if _, err := s.owned(ctx, actor, accID); err != nil {
		return Profile{}, err
	}
	t, err := s.DB.Exec(ctx, `UPDATE style_profile SET enabled=$3, updated_at=now() WHERE id=$1 AND account_id=$2`, id, accID, on)
	if err != nil {
		return Profile{}, err
	}
	if t.RowsAffected() == 0 {
		return Profile{}, ErrNotFound
	}
	s.Audit.Log(ctx, actor.ID, "style.toggle", fmt.Sprint(accID), map[string]any{"account_id": accID, "id": id, "enabled": on})
	return s.get(ctx, accID, id)
}

func (s *Service) Delete(ctx context.Context, actor auth.User, accID, id int64) error {
	if _, err := s.owned(ctx, actor, accID); err != nil {
		return err
	}
	t, err := s.DB.Exec(ctx, `DELETE FROM style_profile WHERE id=$1 AND account_id=$2`, id, accID)
	if err != nil {
		return err
	}
	if t.RowsAffected() == 0 {
		return ErrNotFound
	}
	s.Audit.Log(ctx, actor.ID, "style.delete", fmt.Sprint(accID), map[string]any{"account_id": accID, "id": id})
	return nil
}

// ForWriter returns the enabled traits for a job: the column's profile wins over the account's.
func ForWriter(ctx context.Context, db *pgxpool.Pool, accID, columnID int64) string {
	var tj []byte
	err := db.QueryRow(ctx, `SELECT traits FROM style_profile WHERE account_id=$1 AND enabled AND (column_id=$2 OR column_id IS NULL)
		ORDER BY column_id NULLS LAST LIMIT 1`, accID, columnID).Scan(&tj)
	if err != nil {
		return ""
	}
	var t Traits
	json.Unmarshal(tj, &t)
	return t.Text()
}

// ---------- HTTP ----------

func (s *Service) Register(srv *server.Server, h *auth.Handlers) {
	user := func(f http.HandlerFunc) http.Handler { return h.Require(auth.UserOnly, f) }
	srv.Handle("GET /api/accounts/{id}/style", user(s.hList))
	srv.Handle("POST /api/accounts/{id}/style", user(s.hLearn))
	srv.Handle("PUT /api/accounts/{id}/style/{pid}", user(s.hToggle))
	srv.Handle("DELETE /api/accounts/{id}/style/{pid}", user(s.hDelete))
}

func pathID(r *http.Request, k string) int64 {
	n, _ := strconv.ParseInt(r.PathValue(k), 10, 64)
	return n
}
func actor(r *http.Request) auth.User { u, _ := auth.FromContext(r.Context()); return u }

func fail(w http.ResponseWriter, err error) {
	var ve ValidationError
	switch {
	case errors.As(err, &ve):
		server.WriteError(w, 400, ve.Error())
	case errors.Is(err, ErrNotFound):
		server.WriteError(w, 404, "不存在")
	case errors.Is(err, gateway.ErrNoConfig):
		server.WriteError(w, 503, "还没有配置模型，暂时不能提炼风格")
	case errors.Is(err, gateway.ErrBudget):
		server.WriteError(w, 503, err.Error())
	default:
		server.WriteError(w, 502, err.Error())
	}
}

func (s *Service) hList(w http.ResponseWriter, r *http.Request) {
	ps, err := s.List(r.Context(), actor(r), pathID(r, "id"))
	if err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, ps)
}

func (s *Service) hLearn(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ColumnID *int64   `json:"column_id"`
		Samples  []string `json:"samples"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 512<<10)
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		server.WriteError(w, 400, "请求格式不正确或范文太长")
		return
	}
	p, err := s.Learn(r.Context(), actor(r), pathID(r, "id"), in.ColumnID, in.Samples)
	if err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, p)
}

func (s *Service) hToggle(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	p, err := s.SetEnabled(r.Context(), actor(r), pathID(r, "id"), pathID(r, "pid"), in.Enabled)
	if err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, p)
}

func (s *Service) hDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Delete(r.Context(), actor(r), pathID(r, "id"), pathID(r, "pid")); err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, map[string]bool{"ok": true})
}
