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
	mux.HandleFunc("POST /api/auth/login", a.handle(a.login))
	mux.HandleFunc("GET /api/auth/me", a.withUser(false, func(w http.ResponseWriter, r *http.Request, u *model.User) error {
		writeJSON(w, 200, userDict(u))
		return nil
	}))
	mux.HandleFunc("GET /api/legal", a.handle(a.legalList))
	mux.HandleFunc("GET /api/legal/{doc_type}", a.handle(a.legalDetail))
	mux.HandleFunc("GET /api/meta", a.handle(a.meta))
	mux.HandleFunc("GET /api/me/quota", a.withUser(false, a.myQuota))
	mux.HandleFunc("GET /api/me/consents", a.withUser(false, a.myConsents))
	mux.HandleFunc("GET /api/accounts", a.withUser(false, a.listAccounts))
	mux.HandleFunc("POST /api/accounts", a.withUser(false, a.bindAccount))
	mux.HandleFunc("POST /api/accounts/{account_id}/recheck", a.withUser(false, a.recheckAccount))
	mux.HandleFunc("DELETE /api/accounts/{account_id}", a.withUser(false, a.unbindAccount))
	mux.HandleFunc("GET /api/plans", a.withUser(false, a.listPlans))
	mux.HandleFunc("POST /api/plans", a.withUser(false, a.createPlan))
	mux.HandleFunc("POST /api/plans/{plan_id}/pause", a.withUser(false, a.pausePlan))
	mux.HandleFunc("POST /api/plans/{plan_id}/resume", a.withUser(false, a.resumePlan))
	mux.HandleFunc("DELETE /api/plans/{plan_id}", a.withUser(false, a.deletePlan))
	mux.HandleFunc("POST /api/plans/{plan_id}/generate", a.withUser(false, a.generateNow))
	mux.HandleFunc("GET /api/articles", a.withUser(false, a.listArticles))
	mux.HandleFunc("GET /api/articles/{article_id}", a.withUser(false, a.articleDetail))
	mux.HandleFunc("GET /api/articles/{article_id}/export", a.withUser(false, a.exportArticle))
	mux.HandleFunc("POST /api/articles/{article_id}/regenerate", a.withUser(false, a.regenerate))
	mux.HandleFunc("GET /api/admin/users", a.withUser(true, a.adminUsers))
	mux.HandleFunc("PUT /api/admin/users/{user_id}/tier", a.withUser(true, a.adminSetTier))
	mux.HandleFunc("PUT /api/admin/users/{user_id}/active", a.withUser(true, a.adminSetActive))
	mux.HandleFunc("DELETE /api/admin/users/{user_id}", a.withUser(true, a.adminDeleteUser))
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
	mux.HandleFunc("GET /api/admin/audit-logs", a.withUser(true, a.adminAudit))
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
