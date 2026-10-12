package server

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/leoa81761-ops/wzgx/internal/chart"
	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/quota"
	"github.com/leoa81761-ops/wzgx/internal/schedule"
	"github.com/leoa81761-ops/wzgx/internal/security"
	"github.com/leoa81761-ops/wzgx/internal/stats"
	"github.com/leoa81761-ops/wzgx/internal/store"
	"github.com/leoa81761-ops/wzgx/internal/wechat"
)

func (a *App) ownedAccount(r *http.Request, user *model.User) (*model.WxAccount, error) {
	id, err := pathID(r, "account_id", "找不到公众号")
	if err != nil {
		return nil, err
	}
	account, err := a.Store.GetAccount(r.Context(), nil, id)
	if err != nil {
		return nil, err
	}
	if account == nil || account.UserID != user.ID || account.BindStatus == "unbound" {
		return nil, &AppError{Status: 404, Message: "找不到公众号"}
	}
	return account, nil
}

func (a *App) consentAnalytics(w http.ResponseWriter, r *http.Request, user *model.User) error {
	var body struct {
		Agree bool `json:"agree"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if !body.Agree {
		return bad("读取统计前需要勾选同意")
	}
	now := time.Now().UTC()
	if err := a.Store.InsertConsent(r.Context(), nil, &model.Consent{
		UserID: user.ID, DocType: "analytics", Version: "1", ConsentItem: "read_stats",
		IP: clientIP(r), UserAgent: userAgent(r), CreatedAt: now,
	}); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *App) listStats(w http.ResponseWriter, r *http.Request, user *model.User) error {
	account, err := a.ownedAccount(r, user)
	if err != nil {
		return err
	}
	rows, err := a.Store.ListStats(r.Context(), account.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"items": statViews(rows), "series": readSeries(rows)})
	return nil
}

func (a *App) saveManualStat(w http.ResponseWriter, r *http.Request, user *model.User) error {
	account, err := a.ownedAccount(r, user)
	if err != nil {
		return err
	}
	var body struct {
		Date    string `json:"date"`
		Reads   int    `json:"reads"`
		Shares  int    `json:"shares"`
		Favors  int    `json:"favors"`
		Follows int    `json:"follows"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	day := strings.TrimSpace(body.Date)
	if _, err := time.Parse("2006-01-02", day); err != nil {
		return bad("日期格式用 YYYY-MM-DD")
	}
	for _, n := range []int{body.Reads, body.Shares, body.Favors, body.Follows} {
		if n < 0 || n > 100000000 {
			return bad("阅读、分享、收藏和关注请填 0 到 1 亿之间的整数")
		}
	}
	row := store.StatRow{
		AccountID: account.ID, Date: day, Key: "manual", Reads: body.Reads, Shares: body.Shares,
		Favors: body.Favors, Follows: body.Follows, Source: "manual", CreatedAt: time.Now().UTC(),
	}
	if err := a.Store.SaveStat(r.Context(), nil, row); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *App) pullAccountStats(w http.ResponseWriter, r *http.Request, user *model.User) error {
	account, err := a.ownedAccount(r, user)
	if err != nil {
		return err
	}
	if !account.Verified {
		return bad("未认证号不能拉取微信统计，可以手工填写阅读数")
	}
	agreed, err := a.Store.HasConsentVersion(r.Context(), user.ID, "analytics", "1")
	if err != nil {
		return err
	}
	if !agreed {
		return bad("请先同意读取本账号的统计数据")
	}
	day := stats.Yesterday(time.Now().UTC())
	rows, err := a.collectAccount(r, account, day)
	if err != nil {
		return bad("微信没有返回统计：" + err.Error())
	}
	if err := a.saveCollected(r, account.ID, day, rows); err != nil {
		return err
	}
	saved, err := a.Store.ListStats(r.Context(), account.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"items": statViews(saved), "series": readSeries(saved)})
	return nil
}

func (a *App) collectAccount(r *http.Request, account *model.WxAccount, day string) ([]stats.Row, error) {
	secret, err := security.DecryptSecret(a.Cfg.MasterKey, account.SecretEncrypted)
	if err != nil {
		return nil, err
	}
	wx, err := a.Cache.WeChat(r.Context(), nil)
	if err != nil {
		return nil, err
	}
	mode := "live"
	if wx.Mode == "mock" || strings.HasPrefix(account.AppID, "wx_mock") {
		mode = "mock"
	}
	client := wechat.Build(account.AppID, secret, mode, wx.Base, a.HTTP, a.Tokens)
	return stats.Collect(r.Context(), client, day)
}

func (a *App) saveCollected(r *http.Request, accountID int64, day string, rows []stats.Row) error {
	if len(rows) == 0 {
		rows = []stats.Row{{Date: day, Key: "empty"}}
	}
	now := time.Now().UTC()
	for _, row := range rows {
		if row.Date == "" {
			row.Date = day
		}
		if err := a.Store.SaveStat(r.Context(), nil, store.StatRow{
			AccountID: accountID, Date: row.Date, Key: row.Key, Reads: row.Reads, Shares: row.Shares,
			Favors: row.Favors, Follows: row.Follows, Source: "wechat", Title: row.Title, CreatedAt: now,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) statsPNG(w http.ResponseWriter, r *http.Request, user *model.User) error {
	account, err := a.ownedAccount(r, user)
	if err != nil {
		return err
	}
	rows, err := a.Store.ListStats(r.Context(), account.ID)
	if err != nil {
		return err
	}
	values := seriesValues(readSeries(rows))
	png, err := chart.BarPNG(values)
	if err != nil {
		return bad("还没有可画的阅读数")
	}
	w.Header().Set("Content-Type", "image/png")
	w.WriteHeader(200)
	_, _ = w.Write(png)
	return nil
}

func (a *App) createReport(w http.ResponseWriter, r *http.Request, user *model.User) error {
	account, err := a.ownedAccount(r, user)
	if err != nil {
		return err
	}
	fresh, err := a.Store.GetUser(r.Context(), nil, user.ID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	tier, _ := quota.Membership(fresh, now)
	if tier == "free" || tier == "" {
		return bad("认证号周报从普通版开始")
	}
	saved, err := a.Store.ListStats(r.Context(), account.ID)
	if err != nil {
		return err
	}
	statRows := make([]stats.Row, 0, len(saved))
	for _, row := range saved {
		statRows = append(statRows, stats.Row{Date: row.Date, Key: row.Key, Title: row.Title, Reads: row.Reads, Shares: row.Shares, Favors: row.Favors, Follows: row.Follows})
	}
	summary, advice, days := stats.Describe(statRows)
	adjustment := "样本不足，不调整选题权重。"
	adopted := ""
	if days >= 3 && (tier == "premium" || tier == "lifetime") {
		counts, err := a.Store.FeedbackTopics(r.Context(), account.ID)
		if err != nil {
			return err
		}
		var topics []stats.TopicCount
		for sub, pair := range counts {
			topics = append(topics, stats.TopicCount{Subtopic: sub, Published: pair[0], Discarded: pair[1]})
		}
		weights, note := stats.SuggestWeights(topics)
		briefs, err := a.Store.ListArticleBriefs(r.Context(), account.ID)
		if err != nil {
			return err
		}
		hits := make([]stats.ArticleHit, 0, len(briefs))
		for _, article := range briefs {
			hits = append(hits, stats.ArticleHit{ID: article.ID, Title: article.Title, Subtopic: article.Subtopic, DraftMediaID: article.DraftMediaID})
		}
		var shape string
		weights, note, shape = stats.MergeTune(weights, note, stats.MatchReads(hits, statRows))
		adjustment = note
		if shape != "" {
			if err := a.Store.SaveTitleBias(r.Context(), account.ID, shape, now); err != nil {
				return err
			}
		}
		if len(weights) > 0 {
			plans, err := a.Store.ListPlansByAccount(r.Context(), nil, account.ID, "active")
			if err != nil {
				return err
			}
			for i := range plans {
				for sub, weight := range weights {
					if err := a.Store.SaveTopicWeight(r.Context(), plans[i].ID, sub, weight, now); err != nil {
						return err
					}
				}
			}
			adopted = "applied"
		}
	}
	report := &store.OpsReport{
		AccountID: account.ID, Period: "week-" + schedule.ShanghaiNow(now).AddDate(0, 0, -6).Format("2006-01-02"),
		Summary: summary, Advice: advice, Adjustment: adjustment, Adopted: adopted, CreatedAt: now,
	}
	if err := a.Store.InsertReport(r.Context(), report); err != nil {
		return err
	}
	writeJSON(w, 200, reportView(*report))
	return nil
}

func (a *App) listReports(w http.ResponseWriter, r *http.Request, user *model.User) error {
	account, err := a.ownedAccount(r, user)
	if err != nil {
		return err
	}
	rows, err := a.Store.ListReports(r.Context(), account.ID)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, reportView(row))
	}
	writeJSON(w, 200, out)
	return nil
}

func (a *App) resetWeights(w http.ResponseWriter, r *http.Request, user *model.User) error {
	id, err := pathID(r, "plan_id", "找不到写作计划")
	if err != nil {
		return err
	}
	plan, err := a.Store.GetPlan(r.Context(), nil, id)
	if err != nil {
		return err
	}
	if plan == nil || plan.UserID != user.ID {
		return &AppError{Status: 404, Message: "找不到写作计划"}
	}
	if err := a.Store.ClearTopicWeights(r.Context(), plan.ID); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *App) bindTemplate(w http.ResponseWriter, r *http.Request, user *model.User) error {
	if !a.effective(r.Context()).TemplateEnabled() {
		return bad("服务号模板消息还没有配置")
	}
	var body struct {
		OpenID string `json:"openid"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	openid := strings.TrimSpace(body.OpenID)
	if !validOpenID(openid) {
		return bad("OpenID 由字母、数字、下划线或短横线组成")
	}
	if err := a.Store.SaveTemplateOpenID(r.Context(), user.ID, openid, time.Now().UTC()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (a *App) adminLayoutChecks(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	rows, err := a.Store.ListLayoutChecks(r.Context(), strings.TrimSpace(r.URL.Query().Get("layout_key")))
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"id": row.ID, "layout_key": row.LayoutKey, "device": row.Device, "theme": row.Theme,
			"result": row.Result, "note": row.Note, "created_at": schedule.Format(row.CreatedAt),
		})
	}
	writeJSON(w, 200, out)
	return nil
}

func (a *App) adminSaveLayoutCheck(w http.ResponseWriter, r *http.Request, _ *model.User) error {
	var body struct {
		LayoutKey string `json:"layout_key"`
		Device    string `json:"device"`
		Theme     string `json:"theme"`
		Result    string `json:"result"`
		Note      string `json:"note"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	key := strings.TrimSpace(body.LayoutKey)
	if key == "" || utf8.RuneCountInString(key) > 40 {
		return bad("请填写模板标识")
	}
	theme := body.Theme
	if theme != "light" && theme != "dark" {
		return bad("主题只记录浅色或暗色")
	}
	result := body.Result
	if result != "pass" && result != "fail" {
		return bad("结果只记录通过或未通过")
	}
	device := clipRunes(strings.TrimSpace(body.Device), 40)
	if device == "" {
		return bad("请填写机型")
	}
	row := &store.LayoutCheck{
		LayoutKey: key, Device: device, Theme: theme, Result: result,
		Note: clipRunes(strings.TrimSpace(body.Note), 200), CreatedAt: time.Now().UTC(),
	}
	if err := a.Store.InsertLayoutCheck(r.Context(), row); err != nil {
		return err
	}
	measured, err := a.Store.LayoutMeasured(r.Context(), key)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"id": row.ID, "measured": measured})
	return nil
}

func statViews(rows []store.StatRow) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"date": row.Date, "key": row.Key, "reads": row.Reads, "shares": row.Shares,
			"favors": row.Favors, "follows": row.Follows, "source": row.Source,
		})
	}
	return out
}

func readSeries(rows []store.StatRow) []map[string]any {
	order := []string{}
	sums := map[string]int{}
	for _, row := range rows {
		if row.Key == "follows" || row.Key == "empty" {
			continue
		}
		if _, ok := sums[row.Date]; !ok {
			order = append(order, row.Date)
		}
		sums[row.Date] += row.Reads
	}
	out := make([]map[string]any, 0, len(order))
	for _, day := range order {
		out = append(out, map[string]any{"date": day, "reads": sums[day]})
	}
	return out
}

func seriesValues(series []map[string]any) []int {
	out := make([]int, 0, len(series))
	for _, item := range series {
		n, _ := item["reads"].(int)
		out = append(out, n)
	}
	return out
}

func reportView(row store.OpsReport) map[string]any {
	return map[string]any{
		"id": row.ID, "period": row.Period, "summary": row.Summary, "advice": row.Advice,
		"adjustment": row.Adjustment, "adopted": row.Adopted, "created_at": schedule.Format(row.CreatedAt),
	}
}

func validOpenID(s string) bool {
	if len(s) < 6 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}
