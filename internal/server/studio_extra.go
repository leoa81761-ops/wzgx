package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/catalog"
	"github.com/leoa81761-ops/wzgx/internal/content"
	"github.com/leoa81761-ops/wzgx/internal/llm"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/quota"
	"github.com/leoa81761-ops/wzgx/internal/schedule"
	"github.com/leoa81761-ops/wzgx/internal/store"
)

func (a *App) styleTrial(w http.ResponseWriter, r *http.Request, user *model.User) error {
	var body struct {
		StyleKey string `json:"style_key"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	style := catalog.StyleByKey(strings.TrimSpace(body.StyleKey))
	if style.Key == "" {
		return bad("请选择风格")
	}
	now := time.Now().UTC()
	day := schedule.ShanghaiDay(now)
	used, err := a.Store.CountTrials(r.Context(), user.ID, day)
	if err != nil {
		return err
	}
	if used >= a.trialCap(r) {
		return bad("今天的试写次数已用完")
	}
	paragraph := trialParagraph(style.Name)
	if a.Gateway != nil && !a.Gateway.Mock {
		groups := [][]map[string]string{
			{{"role": "user", "content": "用" + style.Name + "写一段约200字的示例，不要标题。"}},
			{{"role": "user", "content": "给上面这段配一个12字以内的标题。"}},
		}
		if batch, err := a.Gateway.CompleteBatch(r.Context(), []llm.Provider{{Name: "trial", Protocol: "openai", Model: "trial", BaseURL: "https://example.invalid"}}, groups); err == nil && len(batch) > 0 {
			text := strings.TrimSpace(batch[0].Content)
			if utf8.RuneCountInString(text) >= 40 && !strings.HasPrefix(text, "{") {
				paragraph = clipText(text, 200)
			}
		}
	}
	if err := a.Store.InsertTrial(r.Context(), user.ID, day, now); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"style_key": style.Key, "text": paragraph, "runes": utf8.RuneCountInString(paragraph)})
	return nil
}

func (a *App) trialCap(r *http.Request) int {
	raw, err := a.Store.GetSetting(r.Context(), nil, "trial_daily_cap")
	if err != nil {
		return 3
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 {
		return 3
	}
	return n
}

func trialParagraph(styleName string) string {
	base := "这是一段" + styleName + "的试写。先从一个很小的场景说起，再给出今天就能做的一步，最后提醒一个容易忽略的地方。"
	filler := "把句子长短错开，少用空话，把读者能立刻执行的动作写清楚。"
	for utf8.RuneCountInString(base) < 200 {
		base += filler
	}
	return clipText(base, 200)
}

func (a *App) sampleDraft(w http.ResponseWriter, r *http.Request, user *model.User) error {
	if title, body, ok, err := a.Store.GetSample(r.Context(), user.ID); err != nil {
		return err
	} else if ok {
		return bad("已经生成过样稿")
	} else if title != "" || body != "" {
		return bad("已经生成过样稿")
	}
	now := time.Now().UTC()
	title := "先看一篇样稿"
	body := "这篇样稿只在站内给你看效果，没有写入公众号。看完后可以去绑定自己的公众号，再保存写作方向。"
	if err := a.Store.SaveSample(r.Context(), user.ID, title, body, now); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"title": title, "body": body})
	return nil
}

func (a *App) saveStyleProfile(w http.ResponseWriter, r *http.Request, user *model.User) error {
	fresh, err := a.Store.GetUser(r.Context(), nil, user.ID)
	if err != nil || fresh == nil {
		return &AppError{Status: 401, Message: "请先登录"}
	}
	tier, _ := quota.Membership(fresh, time.Now().UTC())
	if tier != "premium" && tier != "lifetime" {
		return bad("风格画像只对高级版和买断版开放")
	}
	var body struct {
		Name    string   `json:"name"`
		Samples []string `json:"samples"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || utf8.RuneCountInString(name) > 20 {
		return bad("请给风格起一个 20 字以内的名字")
	}
	features, err := content.StyleFeatures(body.Samples)
	if err != nil {
		return bad(err.Error())
	}
	raw, _ := json.Marshal(features)
	id, err := a.Store.InsertStyleProfile(r.Context(), user.ID, name, string(raw), time.Now().UTC())
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"id": id, "name": name, "features": features})
	return nil
}

func (a *App) saveAsset(w http.ResponseWriter, r *http.Request, user *model.User) error {
	var body struct {
		Name        string `json:"name"`
		Mime        string `json:"mime"`
		LicenseNote string `json:"license_note"`
		DataBase64  string `json:"data_base64"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	note := strings.TrimSpace(body.LicenseNote)
	if note == "" {
		return bad("请填写图片授权说明")
	}
	if body.Mime != "image/png" && body.Mime != "image/jpeg" {
		return bad("素材只接受 PNG 或 JPEG")
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body.DataBase64))
	if err != nil || len(data) == 0 || len(data) > 200*1024 {
		return bad("图片请控制在 200KB 以内")
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = "素材"
	}
	row := &store.UserAsset{UserID: user.ID, Name: name, Mime: body.Mime, LicenseNote: note, Data: data, CreatedAt: time.Now().UTC()}
	if err := a.Store.InsertAsset(r.Context(), row); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"id": row.ID, "name": row.Name, "license_note": row.LicenseNote})
	return nil
}

func (a *App) consentDraftTitles(w http.ResponseWriter, r *http.Request, user *model.User) error {
	var body struct {
		Agree bool `json:"agree"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if !body.Agree {
		return bad("读取草稿标题需要单独同意")
	}
	if err := a.Store.InsertConsent(r.Context(), nil, &model.Consent{
		UserID: user.ID, DocType: "draft_titles", Version: "1", ConsentItem: "读取本账号草稿标题用于去重",
		IP: clientIP(r), UserAgent: r.UserAgent(), CreatedAt: time.Now().UTC(),
	}); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *App) adminSavePromptVariant(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	var body struct {
		Stage string `json:"stage"`
		Label string `json:"label"`
		Body  string `json:"body"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	stage := strings.TrimSpace(body.Stage)
	label := strings.TrimSpace(body.Label)
	text := strings.TrimSpace(body.Body)
	if stage == "" || label == "" || text == "" {
		return bad("请填写环节、分组和提示词")
	}
	if err := a.Store.SavePromptVariant(r.Context(), stage, label, text); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func clipText(text string, n int) string {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) <= n {
		return text
	}
	return string([]rune(text)[:n])
}
