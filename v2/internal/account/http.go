package account

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"gonghao/internal/auth"
	"gonghao/internal/server"
)

// Routes: users manage their own accounts under /api/accounts; staff use /api/admin/... for any account.
func (s *Service) Register(srv *server.Server, h *auth.Handlers) {
	user := func(f http.HandlerFunc) http.Handler { return h.Require(auth.UserOnly, f) }
	any := func(f http.HandlerFunc) http.Handler { return h.Require(auth.AnyRole, f) }
	staff := func(f http.HandlerFunc) http.Handler { return h.Require(auth.StaffOnly, f) }

	srv.Handle("POST /api/accounts", user(s.hBind))
	srv.Handle("GET /api/accounts", user(s.hList))
	srv.Handle("GET /api/article-types", any(s.hListTypes))
	srv.Handle("PUT /api/me/tavily", user(s.hTavily))
	srv.Handle("GET /api/me/tavily", user(s.hTavilyGet))
	for _, p := range []struct {
		prefix string
		wrap   func(http.HandlerFunc) http.Handler
	}{{"/api/accounts", user}, {"/api/admin/accounts", staff}} {
		srv.Handle("GET "+p.prefix+"/{id}", p.wrap(s.hGet))
		srv.Handle("PATCH "+p.prefix+"/{id}", p.wrap(s.hUpdate))
		srv.Handle("DELETE "+p.prefix+"/{id}", p.wrap(s.hUnbind))
		srv.Handle("GET "+p.prefix+"/{id}/profile", p.wrap(s.hGetProfile))
		srv.Handle("PUT "+p.prefix+"/{id}/profile", p.wrap(s.hSaveProfile))
		srv.Handle("GET "+p.prefix+"/{id}/columns", p.wrap(s.hListColumns))
		srv.Handle("POST "+p.prefix+"/{id}/columns", p.wrap(s.hSaveColumn))
		srv.Handle("PUT "+p.prefix+"/{id}/columns/{cid}", p.wrap(s.hSaveColumn))
		srv.Handle("DELETE "+p.prefix+"/{id}/columns/{cid}", p.wrap(s.hDeleteColumn))
	}
	srv.Handle("GET /api/admin/accounts", staff(s.hList))
	srv.Handle("POST /api/admin/article-types", staff(s.hSaveType))
	srv.Handle("PUT /api/admin/article-types/{tid}", staff(s.hSaveType))
	srv.Handle("PUT /api/admin/users/{uid}/allowance", staff(s.hAllowance))
}

func pathID(r *http.Request, k string) int64 {
	n, _ := strconv.ParseInt(r.PathValue(k), 10, 64)
	return n
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		server.WriteError(w, http.StatusBadRequest, "请求格式不正确")
		return false
	}
	return true
}

func reply(w http.ResponseWriter, code int, v any, err error) {
	switch {
	case err == nil:
		server.WriteJSON(w, code, v)
	case errors.Is(err, ErrNotFound):
		server.WriteError(w, http.StatusNotFound, "不存在")
	case errors.Is(err, ErrForbidden), errors.Is(err, ErrStaffNoBind):
		server.WriteError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, ErrAppTaken), errors.Is(err, ErrCooldown), errors.Is(err, ErrAllowance):
		server.WriteError(w, http.StatusConflict, err.Error())
	default:
		var code = http.StatusBadRequest
		msg := err.Error()
		if isInternal(err) {
			code, msg = http.StatusInternalServerError, "服务器错误"
		}
		server.WriteError(w, code, msg)
	}
}

// Validation errors are plain errors.New; anything wrapped from the DB driver is internal.
func isInternal(err error) bool {
	var u interface{ Unwrap() error }
	return errors.As(err, &u) || !isUserFacing(err.Error())
}

func isUserFacing(msg string) bool {
	for _, r := range msg {
		if r > 0x2E80 { // contains CJK: our own validation messages are Chinese
			return true
		}
	}
	return false
}

func actor(r *http.Request) auth.User { u, _ := auth.FromContext(r.Context()); return u }

func (s *Service) hBind(w http.ResponseWriter, r *http.Request) {
	var in BindInput
	if decode(w, r, &in) {
		a, err := s.Bind(r.Context(), actor(r), in)
		reply(w, http.StatusCreated, a, err)
	}
}

func (s *Service) hList(w http.ResponseWriter, r *http.Request) {
	uid, _ := strconv.ParseInt(r.URL.Query().Get("user_id"), 10, 64)
	as, err := s.List(r.Context(), actor(r), uid)
	reply(w, 200, as, err)
}

func (s *Service) hGet(w http.ResponseWriter, r *http.Request) {
	a, err := s.Get(r.Context(), actor(r), pathID(r, "id"))
	reply(w, 200, a, err)
}

func (s *Service) hUpdate(w http.ResponseWriter, r *http.Request) {
	var p AccountPatch
	if decode(w, r, &p) {
		a, err := s.Update(r.Context(), actor(r), pathID(r, "id"), p)
		reply(w, 200, a, err)
	}
}

func (s *Service) hUnbind(w http.ResponseWriter, r *http.Request) {
	reply(w, 200, map[string]bool{"ok": true}, s.Unbind(r.Context(), actor(r), pathID(r, "id")))
}

func (s *Service) hGetProfile(w http.ResponseWriter, r *http.Request) {
	p, err := s.GetProfile(r.Context(), actor(r), pathID(r, "id"))
	reply(w, 200, p, err)
}

func (s *Service) hSaveProfile(w http.ResponseWriter, r *http.Request) {
	var p Profile
	if decode(w, r, &p) {
		out, err := s.SaveProfile(r.Context(), actor(r), pathID(r, "id"), p)
		reply(w, 200, out, err)
	}
}

func (s *Service) hListColumns(w http.ResponseWriter, r *http.Request) {
	cs, err := s.ListColumns(r.Context(), actor(r), pathID(r, "id"))
	reply(w, 200, cs, err)
}

func (s *Service) hSaveColumn(w http.ResponseWriter, r *http.Request) {
	var c Column
	if decode(w, r, &c) {
		c.ID = pathID(r, "cid")
		code := 200
		if c.ID == 0 {
			code = 201
		}
		out, err := s.SaveColumn(r.Context(), actor(r), pathID(r, "id"), c)
		reply(w, code, out, err)
	}
}

func (s *Service) hDeleteColumn(w http.ResponseWriter, r *http.Request) {
	reply(w, 200, map[string]bool{"ok": true}, s.DeleteColumn(r.Context(), actor(r), pathID(r, "id"), pathID(r, "cid")))
}

func (s *Service) hListTypes(w http.ResponseWriter, r *http.Request) {
	ts, err := s.ListTypes(r.Context(), actor(r))
	reply(w, 200, ts, err)
}

func (s *Service) hSaveType(w http.ResponseWriter, r *http.Request) {
	var t ArticleType
	if decode(w, r, &t) {
		t.ID = pathID(r, "tid")
		code := 200
		if t.ID == 0 {
			code = 201
		}
		out, err := s.SaveType(r.Context(), actor(r), t)
		reply(w, code, out, err)
	}
}

func (s *Service) hAllowance(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MaxAccounts int    `json:"max_accounts"`
		Tier        string `json:"tier"`
		QuotaExempt bool   `json:"quota_exempt"`
	}
	if decode(w, r, &in) {
		err := s.SetAllowance(r.Context(), actor(r), pathID(r, "uid"), in.MaxAccounts, in.Tier, in.QuotaExempt)
		reply(w, 200, map[string]bool{"ok": true}, err)
	}
}
