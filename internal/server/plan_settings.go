package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/catalog"
	"github.com/leoa81761-ops/wzgx/internal/db"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/quota"
	"github.com/leoa81761-ops/wzgx/internal/schedule"
	"github.com/leoa81761-ops/wzgx/internal/store"
)

type planSettings struct {
	Opening     string
	Ending      string
	ColumnName  string
	ColumnStyle string
	Keywords    string
	Audience    string
	BannedWords string
	VerticalKey string
	TopicMode   string
	ImageMode   string
	AssetID     int64
	ProfileID   int64
}

func (s planSettings) extra(planID int64) store.PlanExtra {
	return store.PlanExtra{
		PlanID: planID, Opening: s.Opening, Ending: s.Ending,
		ColumnName: s.ColumnName, ColumnStyle: s.ColumnStyle,
		AssetID: s.AssetID, ProfileID: s.ProfileID,
		Keywords: s.Keywords, Audience: s.Audience, BannedWords: s.BannedWords,
		VerticalKey: s.VerticalKey, TopicMode: s.TopicMode, ImageMode: s.ImageMode,
	}
}

func (a *App) normalizePlanSettings(ctx context.Context, q db.Q, userID int64, tier string, raw planSettings) (planSettings, error) {
	topic, err := normalizeTopicMode(raw.TopicMode)
	if err != nil {
		return planSettings{}, err
	}
	if topic == "confirm" && tier != "premium" && tier != "lifetime" {
		return planSettings{}, bad("选题确认是高级版和买断版的功能")
	}
	image, err := normalizeImageMode(raw.ImageMode)
	if err != nil {
		return planSettings{}, err
	}
	keywords, err := boundedText("关键词", raw.Keywords, 80)
	if err != nil {
		return planSettings{}, err
	}
	audience, err := boundedText("读者", raw.Audience, 80)
	if err != nil {
		return planSettings{}, err
	}
	banned, err := boundedText("禁用词", raw.BannedWords, 80)
	if err != nil {
		return planSettings{}, err
	}
	verticalKey := strings.TrimSpace(raw.VerticalKey)
	if utf8.RuneCountInString(verticalKey) > 40 {
		return planSettings{}, bad("垂类资料包不正确")
	}
	if err := a.knownVertical(ctx, q, verticalKey); err != nil {
		return planSettings{}, err
	}
	if err := a.ownAssetID(ctx, userID, raw.AssetID); err != nil {
		return planSettings{}, err
	}
	if err := a.ownProfileID(ctx, userID, raw.ProfileID); err != nil {
		return planSettings{}, err
	}
	return planSettings{
		Opening: clipText(raw.Opening, 200), Ending: clipText(raw.Ending, 200),
		ColumnName: strings.TrimSpace(raw.ColumnName), ColumnStyle: strings.TrimSpace(raw.ColumnStyle),
		Keywords: keywords, Audience: audience, BannedWords: banned, VerticalKey: verticalKey,
		TopicMode: topic, ImageMode: image, AssetID: raw.AssetID, ProfileID: raw.ProfileID,
	}, nil
}

func normalizeTopicMode(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "auto"
	}
	switch raw {
	case "auto", "rotate", "hot", "confirm":
		return raw, nil
	default:
		return "", bad("选题方式只支持自动、只按方向轮换、优先热点、先确认选题")
	}
}

func normalizeImageMode(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "auto"
	}
	switch raw {
	case "auto", "none", "cover", "asset":
		return raw, nil
	default:
		return "", bad("配图方式只支持自动、不用 AI 配图、只要封面、优先我的素材")
	}
}

func boundedText(label, raw string, n int) (string, error) {
	raw = strings.TrimSpace(raw)
	if utf8.RuneCountInString(raw) > n {
		return "", bad(label + "请控制在 " + strconv.Itoa(n) + " 字以内")
	}
	return raw, nil
}

func (a *App) knownVertical(ctx context.Context, q db.Q, key string) error {
	if key == "" {
		return nil
	}
	packs, err := a.Store.ListVerticals(ctx, q, true)
	if err != nil {
		return err
	}
	for _, pack := range packs {
		if pack.Key == key {
			return nil
		}
	}
	return bad("找不到这个垂类资料包")
}

func (a *App) ownAssetID(ctx context.Context, userID, id int64) error {
	if id == 0 {
		return nil
	}
	if id < 0 {
		return bad("找不到这个素材")
	}
	asset, err := a.Store.GetAsset(ctx, id)
	if err != nil {
		return err
	}
	if asset == nil || asset.UserID != userID {
		return bad("找不到这个素材")
	}
	return nil
}

func (a *App) ownProfileID(ctx context.Context, userID, id int64) error {
	if id == 0 {
		return nil
	}
	if id < 0 {
		return bad("找不到这个风格画像")
	}
	profile, err := a.Store.GetStyleProfile(ctx, id)
	if err != nil {
		return err
	}
	if profile == nil || profile.UserID != userID {
		return bad("找不到这个风格画像")
	}
	return nil
}

func (a *App) getPlan(w http.ResponseWriter, r *http.Request, user *model.User) error {
	id, err := pathID(r, "plan_id", "找不到这个写作计划")
	if err != nil {
		return err
	}
	plan, err := a.ownPlan(r.Context(), nil, user.ID, id)
	if err != nil {
		return err
	}
	view, err := a.planView(r.Context(), plan)
	if err != nil {
		return err
	}
	writeJSON(w, 200, view)
	return nil
}

type planPatch struct {
	WxAccountID  *int64  `json:"wx_account_id"`
	Direction    *string `json:"direction"`
	StyleKey     *string `json:"style_key"`
	TemplateKey  *string `json:"template_key"`
	WordTarget   *int    `json:"word_target"`
	DailyCount   *int    `json:"daily_count"`
	ScheduleType *string `json:"schedule_type"`
	Weekdays     *[]int  `json:"weekdays"`
	TimeWindow   *string `json:"time_window"`
	Opening      *string `json:"opening"`
	Ending       *string `json:"ending"`
	ColumnName   *string `json:"column_name"`
	ColumnStyle  *string `json:"column_style"`
	AssetID      *int64  `json:"asset_id"`
	ProfileID    *int64  `json:"profile_id"`
	Keywords     *string `json:"keywords"`
	Audience     *string `json:"audience"`
	BannedWords  *string `json:"banned_words"`
	VerticalKey  *string `json:"vertical_key"`
	TopicMode    *string `json:"topic_mode"`
	ImageMode    *string `json:"image_mode"`
}

func pickString(next *string, current string) string {
	if next == nil {
		return current
	}
	return *next
}

func (a *App) updatePlan(w http.ResponseWriter, r *http.Request, user *model.User) error {
	id, err := pathID(r, "plan_id", "找不到这个写作计划")
	if err != nil {
		return err
	}
	var body planPatch
	if err := readJSON(r, &body); err != nil {
		return err
	}
	now := time.Now().UTC()
	var saved *model.WritingPlan
	err = a.DB.WithTx(r.Context(), func(tx *db.Tx) error {
		plan, err := a.ownPlan(r.Context(), tx, user.ID, id)
		if err != nil {
			return err
		}
		if body.WxAccountID != nil && *body.WxAccountID != plan.WxAccountID {
			return bad("写作计划属于原来的公众号。要给另一个号用不同设置，请为那个号单独新建一个计划")
		}
		fresh, err := a.Store.GetUser(r.Context(), tx, user.ID)
		if err != nil {
			return err
		}
		if fresh == nil {
			return &AppError{Status: 401, Message: "请先登录"}
		}
		tier, _ := quota.Membership(fresh, now)
		direction := strings.TrimSpace(pickString(body.Direction, plan.Direction))
		if utf8.RuneCountInString(direction) < 1 || utf8.RuneCountInString(direction) > 40 {
			return invalid()
		}
		for _, word := range catalog.BlockedDirectionWords {
			if strings.Contains(direction, word) {
				return bad("「" + word + "」一类方向风险较高，当前版本不开放自动写作")
			}
		}
		styleKey := pickString(body.StyleKey, plan.StyleKey)
		templateKey := pickString(body.TemplateKey, plan.TemplateKey)
		if styleKey == "" || templateKey == "" {
			return bad("请选择风格和排版模板")
		}
		if err := a.presetAllowed(r.Context(), tier, styleKey, templateKey); err != nil {
			return err
		}
		wordTarget := plan.WordTarget
		if body.WordTarget != nil {
			wordTarget = *body.WordTarget
		}
		limits, err := quota.LimitsFor(r.Context(), a.Store, tx, tier)
		if err != nil {
			return err
		}
		if !catalog.WordAllowed(wordTarget) || wordTarget > limits.MaxWords {
			return bad("当前会员单篇上限为 " + strconv.Itoa(limits.MaxWords) + " 字")
		}
		scheduleType := pickString(body.ScheduleType, plan.ScheduleType)
		if scheduleType != "daily" && scheduleType != "weekly" {
			return bad("周期只支持每天或每周")
		}
		weekdays := plan.Weekdays
		if body.Weekdays != nil {
			weekdays = normalizeDays(*body.Weekdays)
		}
		if scheduleType == "weekly" && len(weekdays) == 0 {
			return bad("每周生成请至少选择一天")
		}
		for _, day := range weekdays {
			if day < 1 || day > 7 {
				return bad("星期取值应为 1 到 7")
			}
		}
		window := pickString(body.TimeWindow, plan.TimeWindow)
		if _, _, ok := catalog.WindowHours(window); !ok {
			return bad("请选择生成时段")
		}
		if tier == "free" {
			window = "night"
		}
		daily := plan.ArticlesPerDay()
		if body.DailyCount != nil {
			daily = *body.DailyCount
			if err := quota.CheckDailyCount(r.Context(), a.Store, tx, fresh, now, plan.ID, daily); err != nil {
				return asQuota(err)
			}
		}
		extra, err := a.Store.GetPlanExtra(r.Context(), tx, plan.ID)
		if err != nil {
			return err
		}
		topicIn := extra.TopicMode
		if body.TopicMode != nil {
			topicIn = *body.TopicMode
		} else if topicIn == "" && plan.ConfirmMode {
			topicIn = "confirm"
		}
		settings, err := a.normalizePlanSettings(r.Context(), tx, user.ID, tier, planSettings{
			Opening: pickString(body.Opening, extra.Opening), Ending: pickString(body.Ending, extra.Ending),
			ColumnName: pickString(body.ColumnName, extra.ColumnName), ColumnStyle: pickString(body.ColumnStyle, extra.ColumnStyle),
			Keywords: pickString(body.Keywords, extra.Keywords), Audience: pickString(body.Audience, extra.Audience),
			BannedWords: pickString(body.BannedWords, extra.BannedWords), VerticalKey: pickString(body.VerticalKey, extra.VerticalKey),
			TopicMode: topicIn, ImageMode: pickString(body.ImageMode, extra.ImageMode),
			AssetID: pickInt(body.AssetID, extra.AssetID), ProfileID: pickInt(body.ProfileID, extra.ProfileID),
		})
		if err != nil {
			return err
		}
		if body.TopicMode != nil || topicIn == "confirm" {
			plan.ConfirmMode = settings.TopicMode == "confirm"
		}
		scheduleChanged := body.ScheduleType != nil || body.Weekdays != nil || body.TimeWindow != nil || (tier == "free" && plan.TimeWindow != "night")
		plan.Direction = direction
		plan.StyleKey = styleKey
		plan.TemplateKey = templateKey
		plan.WordTarget = wordTarget
		plan.DailyCount = daily
		plan.ScheduleType = scheduleType
		plan.Weekdays = weekdays
		plan.TimeWindow = window
		if plan.Status == "active" && scheduleChanged {
			next, err := schedule.ComputeNextRun(now, plan.ScheduleType, plan.Weekdays, plan.TimeWindow, sysRNG{})
			if err != nil {
				return bad(err.Error())
			}
			plan.NextRunAt = &next
		}
		if err := a.Store.UpdatePlan(r.Context(), tx, plan); err != nil {
			return err
		}
		if err := a.Store.SavePlanExtra(r.Context(), tx, settings.extra(plan.ID)); err != nil {
			return err
		}
		saved = plan
		return nil
	})
	if err != nil {
		return err
	}
	view, err := a.planView(r.Context(), saved)
	if err != nil {
		return err
	}
	writeJSON(w, 200, view)
	return nil
}

func pickInt(next *int64, current int64) int64 {
	if next == nil {
		return current
	}
	return *next
}
