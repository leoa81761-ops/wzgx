package job

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gonghao/internal/auth"
	"gonghao/internal/render"
	"gonghao/internal/server"
)

func (s *Service) Register(srv *server.Server, h *auth.Handlers) {
	user := func(f http.HandlerFunc) http.Handler { return h.Require(auth.UserOnly, f) }
	anyR := func(f http.HandlerFunc) http.Handler { return h.Require(auth.AnyRole, f) }
	staff := func(f http.HandlerFunc) http.Handler { return h.Require(auth.StaffOnly, f) }
	srv.Handle("POST /api/accounts/{id}/generate", user(s.hGenerate))
	srv.Handle("GET /api/jobs/{id}", anyR(s.hJob))
	srv.Handle("GET /api/articles", anyR(s.hArticles))
	srv.Handle("GET /api/articles/{id}", anyR(s.hArticle))
	srv.Handle("PUT /api/articles/{id}", user(s.hUpdateArticle))
	srv.Handle("GET /api/articles/{id}/preview", anyR(s.hPreview))
	srv.Handle("GET "+render.MediaPrefix+"{key}", http.HandlerFunc(s.hMedia))
	srv.Handle("PUT /api/admin/users/{uid}/daily-quota", staff(s.hQuota))
	srv.Handle("GET /api/me/today", user(s.hToday))
	srv.Handle("GET /api/admin/users", staff(s.hUsers))
	srv.Handle("GET /api/admin/audit", staff(s.hAudit))
	srv.Handle("GET /api/admin/sensitive-words", staff(s.hSensitive))
	srv.Handle("PUT /api/admin/sensitive-words", staff(s.hSaveSensitive))
	srv.Handle("GET /api/site", anyR(s.hSite))
	srv.Handle("PUT /api/accounts/{id}/controls", user(s.hControls))
	srv.Handle("GET /api/accounts/{id}/layout", user(s.hGetLayout))
	srv.Handle("GET /api/site/plans", http.HandlerFunc(s.hPublicPlans))
	srv.Handle("POST /api/complaints", http.HandlerFunc(s.hComplain))
	srv.Handle("GET /api/admin/tiers", staff(s.hAdminTiers))
	srv.Handle("PUT /api/admin/tiers/{key}", staff(s.hSaveTier))
	srv.Handle("GET /api/admin/complaints", staff(s.hAdminComplaints))
	srv.Handle("PUT /api/admin/complaints/{id}", staff(s.hHandleComplaint))
	srv.Handle("GET /api/me/export", user(s.hExport))
	srv.Handle("POST /api/me/delete", user(s.hRequestDeletion))
	srv.Handle("GET /api/me/invite", user(s.hMyInvite))
	h.OnRegister = s.OnRegistered
	h.OnLogin = s.CancelDeletion
	srv.Handle("PUT /api/accounts/{id}/layout", user(s.hSetLayout))
	srv.Handle("GET /api/accounts/{id}/ref-images", user(s.hRefList))
	srv.Handle("POST /api/accounts/{id}/ref-images", user(s.hRefAdd))
	srv.Handle("DELETE /api/accounts/{id}/ref-images/{rid}", user(s.hRefDelete))
	srv.Handle("POST /api/articles/{id}/ref-image", user(s.hArticleRef))
	srv.Handle("GET /api/accounts/{id}/calendar", user(s.hCalendar))
	srv.Handle("GET /api/me/topics", user(s.hPendingTopics))
	srv.Handle("POST /api/jobs/{id}/topic", user(s.hDecideTopic))
	srv.Handle("PUT /api/articles/{id}/feedback", user(s.hFeedback))
	srv.Handle("GET /api/admin/feedback", staff(s.hAdminFeedback))
	srv.Handle("GET /api/admin/settings", staff(s.hGetSettings))
	srv.Handle("PUT /api/admin/settings", staff(s.hSaveSettings))
	srv.Handle("GET /api/admin/jobs", staff(s.hAdminJobs))
	srv.Handle("GET /api/admin/jobs/{id}", staff(s.hAdminJob))
	srv.Handle("POST /api/admin/jobs/{id}/retry", staff(s.hAdminRetry))
	srv.Handle("POST /api/admin/jobs/{id}/cancel", staff(s.hAdminCancel))
	srv.Handle("GET /api/admin/costs", staff(s.hCosts))
	srv.Handle("GET /api/admin/budget", staff(s.hBudget))
	srv.Handle("GET /api/admin/users/{uid}/consents", staff(s.hConsents))
	srv.Handle("GET /api/admin/orders", staff(s.hOrders))
	srv.Handle("POST /api/admin/orders", staff(s.hActivate))
	srv.Handle("GET /api/accounts/{id}/plans", user(s.hPlans))
	srv.Handle("POST /api/accounts/{id}/plans", user(s.hSavePlan))
	srv.Handle("PUT /api/accounts/{id}/plans/{pid}", user(s.hSavePlan))
	srv.Handle("DELETE /api/accounts/{id}/plans/{pid}", user(s.hDeletePlan))
}

func pid(r *http.Request, k string) int64 { n, _ := strconv.ParseInt(r.PathValue(k), 10, 64); return n }
func actor(r *http.Request) auth.User     { u, _ := auth.FromContext(r.Context()); return u }

func fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		server.WriteError(w, 404, "不存在")
	case errors.Is(err, ErrForbidden):
		server.WriteError(w, 403, err.Error())
	case errors.Is(err, ErrQuota):
		server.WriteError(w, 429, err.Error())
	case errors.As(err, new(ErrInvalid)):
		server.WriteError(w, 400, err.Error())
	default:
		server.WriteError(w, 500, "服务器错误")
	}
}

func (s *Service) hGenerate(w http.ResponseWriter, r *http.Request) {
	var in EnqueueInput
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
			server.WriteError(w, 400, "请求格式不正确")
			return
		}
	}
	id, err := s.Enqueue(r.Context(), actor(r), pid(r, "id"), in, r.Header.Get("Idempotency-Key"))
	if err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 202, map[string]any{"job_id": id, "dry_run": true})
}

// owner filter: users see their own rows; staff see all.
func ownerClause(u auth.User, col string, argN int) (string, []any) {
	if u.Role.IsStaff() {
		return "TRUE", nil
	}
	return col + "=$" + strconv.Itoa(argN), []any{u.ID}
}

func (s *Service) hJob(w http.ResponseWriter, r *http.Request) {
	cl, args := ownerClause(actor(r), "user_id", 2)
	var out struct {
		ID         int64           `json:"id"`
		Status     string          `json:"status"`
		Failure    string          `json:"failure"`
		Trace      json.RawMessage `json:"trace"`
		ArticleID  *int64          `json:"article_id"`
		FinishedAt *time.Time      `json:"finished_at"`
	}
	err := s.DB.QueryRow(r.Context(), `SELECT j.id, j.status, j.failure, j.trace, (SELECT max(id) FROM article WHERE job_id=j.id), j.finished_at
		FROM job j WHERE j.id=$1 AND `+cl, append([]any{pid(r, "id")}, args...)...).
		Scan(&out.ID, &out.Status, &out.Failure, &out.Trace, &out.ArticleID, &out.FinishedAt)
	if err != nil {
		fail(w, ErrNotFound)
		return
	}
	server.WriteJSON(w, 200, out)
}

type articleRow struct {
	PublishStatus string          `json:"publish_status"`
	PublishAt     *time.Time      `json:"publish_at"`
	PublishNote   string          `json:"publish_note"`
	PublishURL    string          `json:"publish_url"`
	PublishedAt   *time.Time      `json:"published_at"`
	Layout        string          `json:"layout"`
	CoverURL      string          `json:"cover_url"`  // generated cover ('' = default cover at push)
	CoverRef      bool            `json:"cover_ref"`  // the cover is from the user's authorized pictures
	Images        int             `json:"ai_images"`  // generated pictures stored for this article
	FactCheck     json.RawMessage `json:"fact_check"` // null = not checked
	ID            int64           `json:"id"`
	AccountID     int64           `json:"account_id"`
	AccountName   string          `json:"account_name"`
	ColumnID      *int64          `json:"column_id"`
	Title         string          `json:"title"`
	Topic         string          `json:"topic"`
	TopicKind     *string         `json:"topic_kind"` // news | practical (news-type columns only)
	Summary       string          `json:"summary"`
	Status        string          `json:"status"`
	BlockReason   string          `json:"block_reason"`
	CreatedAt     time.Time       `json:"created_at"`
	PushedAt      *time.Time      `json:"pushed_at"`
	Readback      string          `json:"readback_status"` // '' | unedited | edited | gone | error
	ChangeRatio   *float64        `json:"change_ratio"`
	ReadbackAt    *time.Time      `json:"readback_at"`
	ReadbackNote  string          `json:"readback_note"`
	Rating        *int            `json:"rating"` // the user's feedback: 1 / -1
	Note          *string         `json:"feedback_note"`
	Body          json.RawMessage `json:"body,omitempty"`
	HTML          string          `json:"html,omitempty"`
}

const artCols = `id, account_id, (SELECT w.name FROM wx_account w WHERE w.id = article.account_id), column_id, title, topic, topic_kind,
	summary, status, block_reason, created_at, pushed_at, readback_status, change_ratio, readback_at, readback_note,
	(SELECT f.rating::int FROM article_feedback f WHERE f.article_id = article.id), (SELECT f.note FROM article_feedback f WHERE f.article_id = article.id), layout,
	publish_status, publish_at, publish_note, publish_url, published_at,
	COALESCE((SELECT '` + render.MediaPrefix + `' || i.key FROM article_image i WHERE i.article_id = article.id AND i.kind = 'picture' AND i.slot = 0 ORDER BY i.id DESC LIMIT 1), ''),
	COALESCE((SELECT i.ref_id IS NOT NULL FROM article_image i WHERE i.article_id = article.id AND i.kind = 'picture' AND i.slot = 0 ORDER BY i.id DESC LIMIT 1), FALSE),
	(SELECT count(*)::int FROM article_image i WHERE i.article_id = article.id), fact_check`

func (a *articleRow) fields() []any {
	return []any{&a.ID, &a.AccountID, &a.AccountName, &a.ColumnID, &a.Title, &a.Topic, &a.TopicKind, &a.Summary, &a.Status, &a.BlockReason,
		&a.CreatedAt, &a.PushedAt, &a.Readback, &a.ChangeRatio, &a.ReadbackAt, &a.ReadbackNote, &a.Rating, &a.Note, &a.Layout, &a.PublishStatus, &a.PublishAt, &a.PublishNote, &a.PublishURL, &a.PublishedAt, &a.CoverURL, &a.CoverRef, &a.Images, &a.FactCheck}
}

func (s *Service) hArticles(w http.ResponseWriter, r *http.Request) {
	cl, args := ownerClause(actor(r), "user_id", 1)
	if accStr := r.URL.Query().Get("account_id"); accStr != "" {
		if accID, err := strconv.ParseInt(accStr, 10, 64); err == nil && accID > 0 {
			cl += fmt.Sprintf(" AND account_id=$%d", len(args)+1)
			args = append(args, accID)
		}
	}
	if st := r.URL.Query().Get("status"); st != "" {
		cl += fmt.Sprintf(" AND status=$%d", len(args)+1)
		args = append(args, st)
	}
	rows, err := s.DB.Query(r.Context(), `SELECT `+artCols+` FROM article WHERE `+cl+` ORDER BY id DESC LIMIT 100`, args...)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	out := []articleRow{}
	for rows.Next() {
		var a articleRow
		if err := rows.Scan(a.fields()...); err != nil {
			fail(w, err)
			return
		}
		out = append(out, a)
	}
	server.WriteJSON(w, 200, out)
}

type updateArticleInput struct {
	Title   *string `json:"title"`
	Summary *string `json:"summary"`
	HTML    *string `json:"html"`
}

func (s *Service) hUpdateArticle(w http.ResponseWriter, r *http.Request) {
	act := actor(r)
	artID := pid(r, "id")
	cl, args := ownerClause(act, "user_id", 2)

	var in updateArticleInput
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		server.WriteError(w, http.StatusBadRequest, "请求格式不正确")
		return
	}

	var curStatus string
	err := s.DB.QueryRow(r.Context(), `SELECT status FROM article WHERE id=$1 AND `+cl, append([]any{artID}, args...)...).Scan(&curStatus)
	if err != nil {
		server.WriteError(w, http.StatusNotFound, "文章不存在")
		return
	}
	if curStatus == "pushed" {
		server.WriteError(w, http.StatusConflict, "这篇已经推送到草稿箱，请在公众号后台修改")
		return
	}
	if in.Title != nil {
		if n := len([]rune(strings.TrimSpace(*in.Title))); n == 0 || n > 64 {
			server.WriteError(w, http.StatusBadRequest, "标题需要 1 到 64 个字")
			return
		}
	}

	sets := []string{}
	setArgs := []any{artID}
	if in.Title != nil {
		setArgs = append(setArgs, strings.TrimSpace(*in.Title))
		sets = append(sets, fmt.Sprintf("title=$%d", len(setArgs)))
	}
	if in.Summary != nil {
		setArgs = append(setArgs, strings.TrimSpace(*in.Summary))
		sets = append(sets, fmt.Sprintf("summary=$%d", len(setArgs)))
	}
	if in.HTML != nil {
		setArgs = append(setArgs, sanitizeArticleHTML(*in.HTML))
		sets = append(sets, fmt.Sprintf("html=$%d", len(setArgs)))
	}

	if len(sets) > 0 {
		sets = append(sets, "updated_at=now()")
		query := fmt.Sprintf("UPDATE article SET %s WHERE id=$1 AND status<>'pushed'", strings.Join(sets, ", "))
		if _, err := s.DB.Exec(r.Context(), query, setArgs...); err != nil {
			fail(w, err)
			return
		}
		s.Audit.Log(r.Context(), act.ID, "article.edit", fmt.Sprintf("%d", artID), map[string]any{"article_id": artID})
	}

	a, err := s.article(r)
	if err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, a)
}

func (s *Service) article(r *http.Request) (articleRow, error) {
	cl, args := ownerClause(actor(r), "user_id", 2)
	var a articleRow
	err := s.DB.QueryRow(r.Context(), `SELECT `+artCols+`, body_json, html
		FROM article WHERE id=$1 AND `+cl, append([]any{pid(r, "id")}, args...)...).
		Scan(append(a.fields(), &a.Body, &a.HTML)...)
	if err != nil {
		return a, ErrNotFound
	}
	return a, nil
}

func (s *Service) hArticle(w http.ResponseWriter, r *http.Request) {
	a, err := s.article(r)
	if err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, a)
}

// hPreview renders the stored HTML in a phone-width page, exactly as it would be pushed.
func (s *Service) hPreview(w http.ResponseWriter, r *http.Request) {
	a, err := s.article(r)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src * data:; style-src 'unsafe-inline'")
	body := a.HTML
	// ?layout= shows this article in another template without saving (only while it is not in a WeChat draft)
	if l := r.URL.Query().Get("layout"); l != "" && l != a.Layout && render.ValidLayout(l) && a.Status != "pushed" && s.Render != nil {
		if h, err := s.Render.RenderStored(r.Context(), a.ID, l); err == nil {
			body = h
		}
	}
	// fact check: claims without support in the research are marked in the preview only (never in the draft)
	if len(a.FactCheck) > 0 && r.URL.Query().Get("marks") != "0" {
		var fc struct {
			Claims []struct {
				Text      string `json:"text"`
				Supported bool   `json:"supported"`
			} `json:"claims"`
		}
		if json.Unmarshal(a.FactCheck, &fc) == nil {
			var texts []string
			for _, c := range fc.Claims {
				if !c.Supported {
					texts = append(texts, c.Text)
				}
			}
			body = render.MarkClaims(body, texts)
		}
	}
	w.Write([]byte(render.PreviewPage(a.Title, body, r.URL.Query().Get("theme") == "dark")))
}

func (s *Service) hQuota(w http.ResponseWriter, r *http.Request) {
	var in struct {
		DailyQuota int `json:"daily_quota"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		server.WriteError(w, 400, "请求格式不正确")
		return
	}
	if err := s.SetDailyQuota(r.Context(), actor(r), pid(r, "uid"), in.DailyQuota); err != nil {
		if err.Error() == "额度不正确" {
			server.WriteError(w, 400, err.Error())
			return
		}
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Service) hPlans(w http.ResponseWriter, r *http.Request) {
	ps, err := s.ListPlans(r.Context(), actor(r), pid(r, "id"))
	if err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, ps)
}

func (s *Service) hSavePlan(w http.ResponseWriter, r *http.Request) {
	var p Plan
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&p); err != nil {
		server.WriteError(w, 400, "请求格式不正确")
		return
	}
	p.ID = pid(r, "pid")
	out, err := s.SavePlan(r.Context(), actor(r), pid(r, "id"), p)
	if err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, out)
}

func (s *Service) hDeletePlan(w http.ResponseWriter, r *http.Request) {
	if err := s.DeletePlan(r.Context(), actor(r), pid(r, "id"), pid(r, "pid")); err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, map[string]any{"ok": true})
}

// hMedia serves a generated picture by its random key (used by previews; WeChat gets its own copy at push).
func (s *Service) hMedia(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if len(key) != 32 {
		http.NotFound(w, r)
		return
	}
	var mime string
	var data []byte
	if err := s.DB.QueryRow(r.Context(), `SELECT mime, data FROM article_image WHERE key=$1
		UNION ALL SELECT mime, data FROM ref_image WHERE key=$1 LIMIT 1`, key).Scan(&mime, &data); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(data)
}
