package server

import (
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/catalog"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/quota"
	"github.com/leoa81761-ops/wzgx/internal/setup"
)

func (a *App) setupPreview(w http.ResponseWriter, r *http.Request, user *model.User) error {
	var body struct {
		AccountID int64  `json:"account_id"`
		Direction string `json:"direction"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	account, err := a.ownAccount(r.Context(), nil, user.ID, body.AccountID)
	if err != nil {
		return err
	}
	fresh, err := a.Store.GetUser(r.Context(), nil, user.ID)
	if err != nil {
		return err
	}
	if fresh == nil {
		return &AppError{Status: 401, Message: "请先登录"}
	}
	now := time.Now().UTC()
	tier, _ := quota.Membership(fresh, now)
	limits, err := quota.LimitsFor(r.Context(), a.Store, nil, tier)
	if err != nil {
		return err
	}
	packs, err := a.Store.ListVerticals(r.Context(), nil, true)
	if err != nil {
		return err
	}
	sug := setup.Suggest(setup.Input{
		AccountName: account.Name, SubjectType: account.SubjectType, ServiceType: account.ServiceType,
		Verified: account.Verified, Direction: body.Direction, Tier: tier, MaxWords: limits.MaxWords, Packs: packs,
	})
	style := catalog.StyleByKey(sug.StyleKey)
	template := catalog.TemplateByKey(sug.TemplateKey)
	text := setup.Sample(sug.Direction, style.Name)
	writeJSON(w, 200, map[string]any{
		"account_id": account.ID, "account_name": account.Name,
		"direction": sug.Direction, "style_key": sug.StyleKey, "style_name": style.Name,
		"template_key": sug.TemplateKey, "template_name": template.Name,
		"time_window": sug.TimeWindow, "window_label": catalog.WindowLabel(sug.TimeWindow),
		"vertical_key": sug.VerticalKey, "audience": sug.Audience, "keywords": sug.Keywords,
		"word_target": sug.WordTarget, "schedule_type": "daily", "daily_count": 1,
		"topic_mode": sug.TopicMode, "image_mode": sug.ImageMode,
		"sample": text, "runes": utf8.RuneCountInString(text), "reason": sug.Reason,
	})
	return nil
}
