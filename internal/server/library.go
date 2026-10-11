package server

import (
	"context"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/catalog"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/store"
)

func clipRunes(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func validKey(key string) bool {
	if key == "" || len(key) > 32 {
		return false
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if i == 0 && (c < 'a' || c > 'z') {
			return false
		}
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' {
			continue
		}
		return false
	}
	return true
}

func knownTier(tier string) bool {
	_, ok := catalog.TierLabels[tier]
	return ok
}

func (a *App) styleDicts(ctx context.Context, enabledOnly bool) ([]map[string]any, error) {
	rows, err := a.Store.ListStyles(ctx, nil)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if enabledOnly && !row.Enabled {
			continue
		}
		minTier := row.MinTier
		if minTier == "" {
			minTier = "free"
		}
		out = append(out, map[string]any{
			"key": row.Key, "name": row.Name, "description": row.Description,
			"tone": row.Tone, "min_tier": minTier, "enabled": row.Enabled,
		})
	}
	return out, nil
}

func (a *App) templateDicts(ctx context.Context, enabledOnly bool) ([]map[string]any, error) {
	rows, err := a.Store.ListTemplates(ctx, nil)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if enabledOnly && !row.Enabled {
			continue
		}
		minTier := row.MinTier
		if minTier == "" {
			minTier = "free"
		}
		out = append(out, map[string]any{
			"key": row.Key, "name": row.Name, "description": row.Description,
			"min_tier": minTier, "enabled": row.Enabled,
		})
	}
	return out, nil
}

func (a *App) adminStyles(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	rows, err := a.styleDicts(r.Context(), false)
	if err != nil {
		return err
	}
	writeJSON(w, 200, rows)
	return nil
}

type styleBody struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Tone        string `json:"tone"`
	MinTier     string `json:"min_tier"`
	Enabled     *bool  `json:"enabled"`
}

func (a *App) adminSaveStyle(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	var body styleBody
	if err := readJSON(r, &body); err != nil {
		return err
	}
	body.Key = strings.TrimSpace(body.Key)
	if !validKey(body.Key) {
		return bad("风格标识只使用小写英文字母、数字和下划线")
	}
	name := clipRunes(body.Name, 40)
	if name == "" {
		return bad("请填写风格名称")
	}
	minTier := body.MinTier
	if minTier == "" {
		minTier = "free"
	}
	if !knownTier(minTier) {
		return bad("未知会员档位")
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	if err := a.Store.SaveStyle(r.Context(), nil, store.StyleRow{
		Key: body.Key, Name: name, Description: clipRunes(body.Description, 200),
		Tone: clipRunes(body.Tone, 120), MinTier: minTier, Enabled: enabled,
	}); err != nil {
		return err
	}
	rows, err := a.styleDicts(r.Context(), false)
	if err != nil {
		return err
	}
	writeJSON(w, 200, rows)
	return nil
}

func (a *App) adminTemplates(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	rows, err := a.templateDicts(r.Context(), false)
	if err != nil {
		return err
	}
	writeJSON(w, 200, rows)
	return nil
}

type templateBody struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MinTier     string `json:"min_tier"`
	Enabled     *bool  `json:"enabled"`
}

func (a *App) adminSaveTemplate(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	var body templateBody
	if err := readJSON(r, &body); err != nil {
		return err
	}
	body.Key = strings.TrimSpace(body.Key)
	if !validKey(body.Key) {
		return bad("模板标识只使用小写英文字母、数字和下划线")
	}
	name := clipRunes(body.Name, 40)
	if name == "" {
		return bad("请填写模板名称")
	}
	minTier := body.MinTier
	if minTier == "" {
		minTier = "free"
	}
	if !knownTier(minTier) {
		return bad("未知会员档位")
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	if err := a.Store.SaveTemplate(r.Context(), nil, store.TemplateRow{
		Key: body.Key, Name: name, Description: clipRunes(body.Description, 200), MinTier: minTier, Enabled: enabled,
	}); err != nil {
		return err
	}
	rows, err := a.templateDicts(r.Context(), false)
	if err != nil {
		return err
	}
	writeJSON(w, 200, rows)
	return nil
}

func verticalDict(row store.VerticalPack) map[string]any {
	return map[string]any{
		"id": row.ID, "key": row.Key, "name": row.Name, "keywords": row.Keywords,
		"subtopics": row.Subtopics, "red_lines": row.RedLines, "source_allow": row.SourceAllow,
		"image_rules": row.ImageRules, "disclaimer": row.Disclaimer, "enabled": row.Enabled,
	}
}

func (a *App) adminVerticals(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	rows, err := a.Store.ListVerticals(r.Context(), nil, false)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, verticalDict(row))
	}
	writeJSON(w, 200, out)
	return nil
}

type verticalBody struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Keywords    string `json:"keywords"`
	Subtopics   string `json:"subtopics"`
	RedLines    string `json:"red_lines"`
	SourceAllow string `json:"source_allow"`
	ImageRules  string `json:"image_rules"`
	Disclaimer  string `json:"disclaimer"`
	Enabled     *bool  `json:"enabled"`
}

func (a *App) adminSaveVertical(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	var body verticalBody
	if err := readJSON(r, &body); err != nil {
		return err
	}
	body.Key = strings.TrimSpace(body.Key)
	if !validKey(body.Key) {
		return bad("垂类标识只使用小写英文字母、数字和下划线")
	}
	name := clipRunes(body.Name, 40)
	if name == "" {
		return bad("请填写垂类名称")
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	pack := &store.VerticalPack{
		Key: body.Key, Name: name, Keywords: clipRunes(body.Keywords, 200),
		Subtopics: clipRunes(body.Subtopics, 500), RedLines: clipRunes(body.RedLines, 500),
		SourceAllow: clipRunes(body.SourceAllow, 300), ImageRules: clipRunes(body.ImageRules, 300),
		Disclaimer: clipRunes(body.Disclaimer, 200), Enabled: enabled,
	}
	if err := a.Store.SaveVertical(r.Context(), nil, pack, time.Now().UTC()); err != nil {
		return err
	}
	return a.adminVerticals(w, r, nil)
}

func (a *App) presetAllowed(ctx context.Context, tier, styleKey, templateKey string) error {
	styles, err := a.Store.ListStyles(ctx, nil)
	if err != nil {
		return err
	}
	var style *store.StyleRow
	for i := range styles {
		if styles[i].Key == styleKey {
			style = &styles[i]
			break
		}
	}
	templates, err := a.Store.ListTemplates(ctx, nil)
	if err != nil {
		return err
	}
	var template *store.TemplateRow
	for i := range templates {
		if templates[i].Key == templateKey {
			template = &templates[i]
			break
		}
	}
	if style == nil || !style.Enabled || template == nil || !template.Enabled {
		return bad("请选择风格和排版模板")
	}
	if !catalog.TierAllows(tier, style.MinTier) {
		return bad("当前会员不能使用这个风格")
	}
	if !catalog.TierAllows(tier, template.MinTier) {
		return bad("当前会员不能使用这个排版模板")
	}
	return nil
}

func (a *App) knownTemplate(ctx context.Context, key string) (bool, error) {
	rows, err := a.Store.ListTemplates(ctx, nil)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if row.Key == key && row.Enabled {
			return true, nil
		}
	}
	return catalog.TemplateByKey(key).Key != "", nil
}
