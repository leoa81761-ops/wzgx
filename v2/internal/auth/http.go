package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"

	"gonghao/internal/server"
)

const CookieName = "gh2_session"

type ctxKey struct{}

// FromContext returns the authenticated user set by Require.
func FromContext(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(ctxKey{}).(User)
	return u, ok
}

// WithUser is exported for tests in other packages.
func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

type Handlers struct {
	svc          *Service
	cookieSecure bool
}

func NewHandlers(svc *Service, cookieSecure bool) *Handlers {
	return &Handlers{svc: svc, cookieSecure: cookieSecure}
}

// Require wraps h so it only runs for a logged-in user whose role passes allow.
func (h *Handlers) Require(allow func(Role) bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(CookieName)
		if err != nil {
			server.WriteError(w, http.StatusUnauthorized, "请先登录")
			return
		}
		u, err := h.svc.Authenticate(r.Context(), c.Value)
		if err != nil {
			server.WriteError(w, http.StatusUnauthorized, "请先登录")
			return
		}
		if allow != nil && !allow(u.Role) {
			server.WriteError(w, http.StatusForbidden, "没有权限")
			return
		}
		next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), u)))
	})
}

func AnyRole(Role) bool          { return true }
func UserOnly(r Role) bool       { return r == RoleUser }
func StaffOnly(r Role) bool      { return r.IsStaff() }
func SuperAdminOnly(r Role) bool { return r == RoleSuperAdmin }

func (h *Handlers) Register(s *server.Server) {
	s.Handle("POST /api/auth/register", http.HandlerFunc(h.register))
	s.Handle("POST /api/auth/login", http.HandlerFunc(h.login))
	s.Handle("POST /api/auth/logout", http.HandlerFunc(h.logout))
	s.Handle("GET /api/auth/me", h.Require(AnyRole, http.HandlerFunc(h.me)))
	s.Handle("POST /api/admin/operators", h.Require(SuperAdminOnly, http.HandlerFunc(h.createOperator)))
	s.Handle("GET /api/admin/operators", h.Require(SuperAdminOnly, http.HandlerFunc(h.listStaff)))
	s.Handle("POST /api/admin/operators/{id}/status", h.Require(SuperAdminOnly, http.HandlerFunc(h.operatorStatus)))
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		server.WriteError(w, http.StatusBadRequest, "请求格式不正确")
		return false
	}
	return true
}

func clientIP(r *http.Request) string {
	if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
		first, _, _ := strings.Cut(xf, ",")
		return strings.TrimSpace(first)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (h *Handlers) register(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email, Password, Name string
		TermsVersion          string `json:"terms_version"`
		PrivacyVersion        string `json:"privacy_version"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, err := h.svc.Register(r.Context(), in.Email, in.Password, in.Name, Consent{in.TermsVersion, in.PrivacyVersion}, clientIP(r), r.UserAgent())
	if errors.Is(err, ErrEmailTaken) {
		server.WriteError(w, http.StatusConflict, "该邮箱已注册")
		return
	}
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	server.WriteJSON(w, http.StatusCreated, u)
}

func (h *Handlers) login(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Password string }
	if !decode(w, r, &in) {
		return
	}
	u, tok, err := h.svc.Login(r.Context(), in.Email, in.Password, clientIP(r), r.UserAgent())
	switch {
	case errors.Is(err, ErrRateLimited):
		server.WriteError(w, http.StatusTooManyRequests, err.Error())
		return
	case errors.Is(err, ErrBadLogin), errors.Is(err, ErrDisabled):
		server.WriteError(w, http.StatusUnauthorized, err.Error())
		return
	case err != nil:
		server.WriteError(w, http.StatusInternalServerError, "登录失败")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: tok, Path: "/", HttpOnly: true, Secure: h.cookieSecure,
		SameSite: http.SameSiteLaxMode, MaxAge: int(h.svc.ttl.Seconds()),
	})
	server.WriteJSON(w, http.StatusOK, u)
}

func (h *Handlers) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CookieName); err == nil {
		_ = h.svc.Logout(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", HttpOnly: true, Secure: h.cookieSecure, MaxAge: -1})
	server.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handlers) me(w http.ResponseWriter, r *http.Request) {
	u, _ := FromContext(r.Context())
	server.WriteJSON(w, http.StatusOK, u)
}

func (h *Handlers) createOperator(w http.ResponseWriter, r *http.Request) {
	actor, _ := FromContext(r.Context())
	var in struct{ Email, Password, Name string }
	if !decode(w, r, &in) {
		return
	}
	u, err := h.svc.CreateOperator(r.Context(), actor, in.Email, in.Password, in.Name)
	switch {
	case errors.Is(err, ErrEmailTaken):
		server.WriteError(w, http.StatusConflict, "该邮箱已注册")
	case errors.Is(err, ErrForbidden):
		server.WriteError(w, http.StatusForbidden, "没有权限")
	case err != nil:
		server.WriteError(w, http.StatusBadRequest, err.Error())
	default:
		server.WriteJSON(w, http.StatusCreated, u)
	}
}

func (h *Handlers) listStaff(w http.ResponseWriter, r *http.Request) {
	us, err := h.svc.store.ListStaff(r.Context())
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "读取失败")
		return
	}
	server.WriteJSON(w, http.StatusOK, us)
}

func (h *Handlers) operatorStatus(w http.ResponseWriter, r *http.Request) {
	actor, _ := FromContext(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "id 不正确")
		return
	}
	var in struct{ Active bool }
	if !decode(w, r, &in) {
		return
	}
	switch err := h.svc.SetOperatorStatus(r.Context(), actor, id, in.Active); {
	case errors.Is(err, ErrNotFound):
		server.WriteError(w, http.StatusNotFound, "用户不存在")
	case errors.Is(err, ErrForbidden):
		server.WriteError(w, http.StatusForbidden, "只能停用或启用运营人员")
	case err != nil:
		server.WriteError(w, http.StatusInternalServerError, "操作失败")
	default:
		server.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}
