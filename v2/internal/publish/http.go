package publish

import (
	"errors"
	"net/http"
	"strconv"

	"gonghao/internal/auth"
	"gonghao/internal/server"
)

func (s *Service) Register(srv *server.Server, h *auth.Handlers) {
	srv.Handle("POST /api/accounts/{id}/check", h.Require(auth.UserOnly, http.HandlerFunc(s.hCheck)))
	srv.Handle("POST /api/admin/accounts/{id}/check", h.Require(auth.StaffOnly, http.HandlerFunc(s.hCheck)))
	srv.Handle("POST /api/articles/{id}/push", h.Require(auth.UserOnly, http.HandlerFunc(s.hPush)))
	srv.Handle("POST /api/articles/{id}/readback", h.Require(auth.UserOnly, http.HandlerFunc(s.hReadBack)))
}

func id(r *http.Request) int64      { n, _ := strconv.ParseInt(r.PathValue("id"), 10, 64); return n }
func who(r *http.Request) auth.User { u, _ := auth.FromContext(r.Context()); return u }

func (s *Service) hCheck(w http.ResponseWriter, r *http.Request) {
	rep, err := s.Check(r.Context(), who(r), id(r))
	if err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, rep)
}

func (s *Service) hPush(w http.ResponseWriter, r *http.Request) {
	mediaID, err := s.Push(r.Context(), who(r), id(r))
	if err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, map[string]any{"status": "pushed", "draft_media_id": mediaID})
}

func (s *Service) hReadBack(w http.ResponseWriter, r *http.Request) {
	res, err := s.ReadBack(r.Context(), who(r), id(r))
	if err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, res)
}

func fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		server.WriteError(w, 404, "不存在")
	case errors.Is(err, ErrState):
		server.WriteError(w, 409, err.Error())
	default:
		server.WriteError(w, 502, err.Error())
	}
}
