package server

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/leoa81761-ops/wzgx/internal/model"
	"github.com/leoa81761-ops/wzgx/internal/security"
)

type AppError struct {
	Status  int
	Message string
}

func (e *AppError) Error() string { return e.Message }

func bad(msg string) error { return &AppError{Status: 400, Message: msg} }

func (a *App) handle(fn func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				writeJSON(w, 500, map[string]any{"detail": "服务器内部错误"})
			}
		}()
		err := fn(w, r)
		if err == nil {
			return
		}
		var appErr *AppError
		if errors.As(err, &appErr) {
			writeJSON(w, appErr.Status, map[string]any{"detail": appErr.Message})
			return
		}
		log.Printf("请求失败 %s %s: %v", r.Method, r.URL.Path, err)
		writeJSON(w, 500, map[string]any{"detail": "服务器内部错误"})
	}
}

func (a *App) withUser(admin bool, fn func(http.ResponseWriter, *http.Request, *model.User) error) http.HandlerFunc {
	return a.handle(func(w http.ResponseWriter, r *http.Request) error {
		user, err := a.authenticate(r)
		if err != nil {
			return err
		}
		if admin && !user.IsAdmin {
			return &AppError{Status: 403, Message: "需要管理员权限"}
		}
		return fn(w, r, user)
	})
}

func (a *App) authenticate(r *http.Request) (*model.User, error) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return nil, &AppError{Status: 401, Message: "请先登录"}
	}
	token := strings.TrimSpace(header[7:])
	id, err := security.ReadToken(token, a.Cfg.JWTSecret)
	if err != nil {
		return nil, &AppError{Status: 401, Message: "登录已失效，请重新登录"}
	}
	user, err := a.Store.GetUser(r.Context(), nil, id)
	if err != nil {
		return nil, err
	}
	if user == nil || !user.IsActive {
		return nil, &AppError{Status: 401, Message: "请先登录"}
	}
	return user, nil
}

func readJSON(r *http.Request, dest any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(dest); err != nil {
		if errors.Is(err, io.EOF) {
			return &AppError{Status: 422, Message: "请求参数不正确"}
		}
		return &AppError{Status: 422, Message: "请求参数不正确"}
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(payload)
}

func clientIP(r *http.Request) string {
	forwarded := r.Header.Get("X-Forwarded-For")
	if forwarded != "" {
		part := strings.TrimSpace(strings.Split(forwarded, ",")[0])
		if len(part) > 64 {
			part = part[:64]
		}
		return part
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}

func userAgent(r *http.Request) string {
	ua := r.Header.Get("User-Agent")
	if len(ua) > 256 {
		ua = ua[:256]
	}
	return ua
}

func jtime(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

func jtimev(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", a.handle(a.health))
	mux.HandleFunc("POST /api/auth/register", a.handle(a.register))
	mux.HandleFunc("POST /api/auth/register/code", a.handle(a.sendRegisterCode))
	mux.HandleFunc("POST /api/auth/login", a.handle(a.login))
	mux.HandleFunc("GET /api/auth/options", a.handle(a.authOptions))
	mux.HandleFunc("POST /api/auth/sms/send", a.handle(a.sendSMS))
	mux.HandleFunc("POST /api/auth/sms/login", a.handle(a.loginSMS))
	mux.HandleFunc("POST /api/auth/recover/send", a.handle(a.recoverSend))
	mux.HandleFunc("POST /api/auth/recover/reset", a.handle(a.recoverReset))
	mux.HandleFunc("GET /api/auth/wechat/start", a.handle(a.wechatScanStart))
	mux.HandleFunc("GET /api/auth/wechat/callback", a.handle(a.wechatScanCallback))
	mux.HandleFunc("POST /api/auth/wechat/ticket", a.handle(a.wechatTicket))
	mux.HandleFunc("GET /api/wechat/component/notify", a.handle(a.componentNotify))
	mux.HandleFunc("POST /api/wechat/component/notify", a.handle(a.componentNotify))
	mux.HandleFunc("GET /api/auth/wechat/component/callback", a.handle(a.componentCallback))
	mux.HandleFunc("GET /api/auth/me", a.withUser(false, func(w http.ResponseWriter, r *http.Request, u *model.User) error {
		writeJSON(w, 200, userDict(u))
		return nil
	}))
	mux.HandleFunc("GET /api/legal", a.handle(a.legalList))
	mux.HandleFunc("GET /api/legal/{doc_type}", a.handle(a.legalDetail))
	mux.HandleFunc("GET /api/meta", a.handle(a.meta))
	mux.HandleFunc("GET /api/me/quota", a.withUser(false, a.myQuota))
	mux.HandleFunc("GET /api/me/referral", a.withUser(false, a.myReferral))
	mux.HandleFunc("GET /api/me/consents", a.withUser(false, a.myConsents))
	mux.HandleFunc("POST /api/me/consents/{id}/withdraw", a.withUser(false, a.withdrawConsent))
	mux.HandleFunc("POST /api/me/consents/style-diff", a.withUser(false, a.consentStyleDiff))
	mux.HandleFunc("POST /api/me/rebind/send", a.withUser(false, a.rebindSend))
	mux.HandleFunc("POST /api/me/rebind", a.withUser(false, a.rebindPhone))
	mux.HandleFunc("GET /api/me/consent-status", a.withUser(false, a.consentStatus))
	mux.HandleFunc("POST /api/me/reconsent", a.withUser(false, a.reconsent))
	mux.HandleFunc("POST /api/me/consents/analytics", a.withUser(false, a.consentAnalytics))
	mux.HandleFunc("POST /api/me/template-bind", a.withUser(false, a.bindTemplate))
	mux.HandleFunc("GET /api/me/export", a.withUser(false, a.exportMe))
	mux.HandleFunc("DELETE /api/me", a.withUser(false, a.deleteMe))
	mux.HandleFunc("GET /api/notifications", a.withUser(false, a.listNotifications))
	mux.HandleFunc("POST /api/notifications/{id}/read", a.withUser(false, a.readNotification))
	mux.HandleFunc("POST /api/billing/checkout", a.withUser(false, a.checkout))
	mux.HandleFunc("POST /api/billing/refund-request", a.withUser(false, a.requestRefund))
	mux.HandleFunc("POST /api/billing/sandbox/complete", a.withUser(false, a.sandboxComplete))
	mux.HandleFunc("GET /api/billing/orders/{out_trade_no}", a.withUser(false, a.billingOrder))
	mux.HandleFunc("POST /api/billing/wechat/notify", a.handle(a.wechatNotify))
	mux.HandleFunc("POST /api/billing/alipay/notify", a.handle(a.alipayNotify))
	mux.HandleFunc("POST /api/complaints", a.handle(a.createComplaint))
	mux.HandleFunc("GET /api/accounts", a.withUser(false, a.listAccounts))
	mux.HandleFunc("POST /api/accounts", a.withUser(false, a.bindAccount))
	mux.HandleFunc("POST /api/accounts/component/start", a.withUser(false, a.componentStart))
	mux.HandleFunc("POST /api/accounts/{account_id}/recheck", a.withUser(false, a.recheckAccount))
	mux.HandleFunc("DELETE /api/accounts/{account_id}", a.withUser(false, a.unbindAccount))
	mux.HandleFunc("GET /api/accounts/{account_id}/stats", a.withUser(false, a.listStats))
	mux.HandleFunc("POST /api/accounts/{account_id}/stats", a.withUser(false, a.saveManualStat))
	mux.HandleFunc("POST /api/accounts/{account_id}/stats/pull", a.withUser(false, a.pullAccountStats))
	mux.HandleFunc("GET /api/accounts/{account_id}/stats.png", a.withUser(false, a.statsPNG))
	mux.HandleFunc("GET /api/accounts/{account_id}/reports", a.withUser(false, a.listReports))
	mux.HandleFunc("POST /api/accounts/{account_id}/reports", a.withUser(false, a.createReport))
	mux.HandleFunc("POST /api/setup/preview", a.withUser(false, a.setupPreview))
	mux.HandleFunc("GET /api/plans", a.withUser(false, a.listPlans))
	mux.HandleFunc("POST /api/plans", a.withUser(false, a.createPlan))
	mux.HandleFunc("GET /api/plans/{plan_id}", a.withUser(false, a.getPlan))
	mux.HandleFunc("PUT /api/plans/{plan_id}", a.withUser(false, a.updatePlan))
	mux.HandleFunc("POST /api/styles/trial", a.withUser(false, a.styleTrial))
	mux.HandleFunc("POST /api/sample", a.withUser(false, a.sampleDraft))
	mux.HandleFunc("POST /api/style-profiles", a.withUser(false, a.saveStyleProfile))
	mux.HandleFunc("POST /api/assets", a.withUser(false, a.saveAsset))
	mux.HandleFunc("POST /api/me/consents/draft-titles", a.withUser(false, a.consentDraftTitles))
	mux.HandleFunc("PUT /api/plans/{plan_id}/daily", a.withUser(false, a.setPlanDaily))
	mux.HandleFunc("PUT /api/plans/{plan_id}/confirm", a.withUser(false, a.setPlanConfirm))
	mux.HandleFunc("POST /api/plans/{plan_id}/proposal/accept", a.withUser(false, a.acceptProposal))
	mux.HandleFunc("POST /api/plans/{plan_id}/proposal/reject", a.withUser(false, a.rejectProposal))
	mux.HandleFunc("POST /api/plans/{plan_id}/pause", a.withUser(false, a.pausePlan))
	mux.HandleFunc("POST /api/plans/{plan_id}/resume", a.withUser(false, a.resumePlan))
	mux.HandleFunc("DELETE /api/plans/{plan_id}", a.withUser(false, a.deletePlan))
	mux.HandleFunc("POST /api/plans/{plan_id}/generate", a.withUser(false, a.generateNow))
	mux.HandleFunc("GET /api/plans/{plan_id}/calendar", a.withUser(false, a.planCalendar))
	mux.HandleFunc("POST /api/plans/{plan_id}/veto", a.withUser(false, a.addVeto))
	mux.HandleFunc("POST /api/plans/{plan_id}/unveto", a.withUser(false, a.removeVeto))
	mux.HandleFunc("POST /api/plans/{plan_id}/weights/reset", a.withUser(false, a.resetWeights))
	mux.HandleFunc("PUT /api/plans/{plan_id}/publish", a.withUser(false, a.setPlanPublish))
	mux.HandleFunc("PUT /api/plans/{plan_id}/pin", a.withUser(false, a.setPin))
	mux.HandleFunc("DELETE /api/plans/{plan_id}/pin", a.withUser(false, a.clearPin))
	mux.HandleFunc("GET /api/articles", a.withUser(false, a.listArticles))
	mux.HandleFunc("GET /api/articles/{article_id}", a.withUser(false, a.articleDetail))
	mux.HandleFunc("GET /api/articles/{article_id}/export", a.withUser(false, a.exportArticle))
	mux.HandleFunc("POST /api/articles/{article_id}/regenerate", a.withUser(false, a.regenerate))
	mux.HandleFunc("POST /api/articles/{article_id}/feedback", a.withUser(false, a.saveFeedback))
	mux.HandleFunc("POST /api/articles/{article_id}/reread", a.withUser(false, a.rereadDraft))
	mux.HandleFunc("POST /api/admin/prompt-variants", a.withUser(true, a.adminSavePromptVariant))
	mux.HandleFunc("GET /api/admin/plan-configs", a.withUser(true, a.adminPlanConfigs))
	mux.HandleFunc("PUT /api/admin/plan-configs/{tier}", a.withUser(true, a.adminSavePlanConfig))
	mux.HandleFunc("GET /api/admin/users", a.withUser(true, a.adminUsers))
	mux.HandleFunc("PUT /api/admin/users/{user_id}/tier", a.withUser(true, a.adminSetTier))
	mux.HandleFunc("PUT /api/admin/users/{user_id}/active", a.withUser(true, a.adminSetActive))
	mux.HandleFunc("DELETE /api/admin/users/{user_id}", a.withUser(true, a.adminDeleteUser))
	mux.HandleFunc("GET /api/admin/users/{user_id}/contract", a.withUser(true, a.adminGetContract))
	mux.HandleFunc("PUT /api/admin/users/{user_id}/contract", a.withUser(true, a.adminSaveContract))
	mux.HandleFunc("GET /api/admin/integrations", a.withUser(true, a.adminIntegrations))
	mux.HandleFunc("PUT /api/admin/integrations/{section}", a.withUser(true, a.adminSaveIntegration))
	mux.HandleFunc("POST /api/admin/integrations/{section}/test", a.withUser(true, a.adminTestIntegration))
	mux.HandleFunc("GET /api/admin/providers", a.withUser(true, a.adminProviders))
	mux.HandleFunc("POST /api/admin/providers", a.withUser(true, a.adminCreateProvider))
	mux.HandleFunc("PUT /api/admin/providers/{provider_id}", a.withUser(true, a.adminUpdateProvider))
	mux.HandleFunc("DELETE /api/admin/providers/{provider_id}", a.withUser(true, a.adminDeleteProvider))
	mux.HandleFunc("POST /api/admin/providers/{provider_id}/test", a.withUser(true, a.adminTestProvider))
	mux.HandleFunc("GET /api/admin/routes", a.withUser(true, a.adminRoutes))
	mux.HandleFunc("PUT /api/admin/routes", a.withUser(true, a.adminSaveRoutes))
	mux.HandleFunc("GET /api/admin/wechat", a.withUser(true, a.adminGetWechat))
	mux.HandleFunc("PUT /api/admin/wechat", a.withUser(true, a.adminSaveWechat))
	mux.HandleFunc("POST /api/admin/connectivity-test", a.withUser(true, a.adminConnectivity))
	mux.HandleFunc("GET /api/admin/sensitive-words", a.withUser(true, a.adminGetWords))
	mux.HandleFunc("PUT /api/admin/sensitive-words", a.withUser(true, a.adminSaveWords))
	mux.HandleFunc("GET /api/admin/source-domains", a.withUser(true, a.adminGetSourceDomains))
	mux.HandleFunc("PUT /api/admin/source-domains", a.withUser(true, a.adminSaveSourceDomains))
	mux.HandleFunc("GET /api/admin/styles", a.withUser(true, a.adminStyles))
	mux.HandleFunc("PUT /api/admin/styles", a.withUser(true, a.adminSaveStyle))
	mux.HandleFunc("GET /api/admin/templates", a.withUser(true, a.adminTemplates))
	mux.HandleFunc("GET /api/admin/layout-checks", a.withUser(true, a.adminLayoutChecks))
	mux.HandleFunc("POST /api/admin/layout-checks", a.withUser(true, a.adminSaveLayoutCheck))
	mux.HandleFunc("PUT /api/admin/templates", a.withUser(true, a.adminSaveTemplate))
	mux.HandleFunc("GET /api/admin/verticals", a.withUser(true, a.adminVerticals))
	mux.HandleFunc("PUT /api/admin/verticals", a.withUser(true, a.adminSaveVertical))
	mux.HandleFunc("GET /api/admin/audit-logs", a.withUser(true, a.adminAudit))
	mux.HandleFunc("GET /api/admin/metrics", a.withUser(true, a.adminMetrics))
	mux.HandleFunc("PUT /api/admin/cost", a.withUser(true, a.adminSaveCost))
	mux.HandleFunc("GET /api/admin/consents", a.withUser(true, a.adminConsents))
	mux.HandleFunc("GET /api/admin/legal", a.withUser(true, a.adminLegal))
	mux.HandleFunc("POST /api/admin/legal", a.withUser(true, a.adminPublishLegal))
	mux.HandleFunc("GET /api/admin/referrals", a.withUser(true, a.adminReferrals))
	mux.HandleFunc("POST /api/admin/referrals/{id}/approve", a.withUser(true, a.adminApproveReferral))
	mux.HandleFunc("POST /api/admin/referrals/{id}/revoke", a.withUser(true, a.adminRevokeReferral))
	mux.HandleFunc("GET /api/admin/invite-rules", a.withUser(true, a.adminInviteRules))
	mux.HandleFunc("PUT /api/admin/invite-rules", a.withUser(true, a.adminSaveInviteRules))
	mux.HandleFunc("GET /api/admin/hot-sources", a.withUser(true, a.adminHotSources))
	mux.HandleFunc("POST /api/admin/hot-sources", a.withUser(true, a.adminSaveHotSource))
	mux.HandleFunc("DELETE /api/admin/hot-sources/{id}", a.withUser(true, a.adminDeleteHotSource))
	mux.HandleFunc("POST /api/admin/hot-sources/{id}/test", a.withUser(true, a.adminTestHotSource))
	mux.HandleFunc("GET /api/admin/tasks", a.withUser(true, a.adminTasks))
	mux.HandleFunc("GET /api/admin/orders", a.withUser(true, a.adminOrders))
	mux.HandleFunc("POST /api/admin/orders/{id}/refund", a.withUser(true, a.adminRefundOrder))
	mux.HandleFunc("GET /api/admin/complaints", a.withUser(true, a.adminComplaints))
	mux.HandleFunc("PUT /api/admin/complaints/{id}", a.withUser(true, a.adminReplyComplaint))
	mux.HandleFunc("GET /{$}", a.spa)
	mux.HandleFunc("GET /{path...}", a.spa)
	return cors(mux)
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "http://localhost:5173" || origin == "http://127.0.0.1:5173" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) health(w http.ResponseWriter, r *http.Request) error {
	if err := a.DB.QueryRow(r.Context(), "SELECT 1").Scan(new(int)); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"status": "ok"})
	return nil
}
