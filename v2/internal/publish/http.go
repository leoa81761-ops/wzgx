package publish

import (
	"encoding/json"
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
	srv.Handle("POST /api/articles/{id}/publish/cancel", h.Require(auth.UserOnly, http.HandlerFunc(s.hCancelPublish)))
	srv.Handle("POST /api/accounts/{id}/stats/refresh", h.Require(auth.UserOnly, http.HandlerFunc(s.hRefreshStats)))
	srv.Handle("PUT /api/me/publish-pause", h.Require(auth.UserOnly, http.HandlerFunc(s.hPublishPause)))
	srv.Handle("GET /api/admin/publish-pause", h.Require(auth.StaffOnly, http.HandlerFunc(s.hGetPlatformPause)))
	srv.Handle("PUT /api/admin/publish-pause", h.Require(auth.StaffOnly, http.HandlerFunc(s.hPlatformPause)))
}

func (s *Service) hCancelPublish(w http.ResponseWriter, r *http.Request) {
	if err := s.CancelPublish(r.Context(), who(r), id(r)); err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, map[string]any{"publish_status": "cancelled"})
}

type pauseIn struct {
	Paused bool `json:"paused"`
}

func (s *Service) hPublishPause(w http.ResponseWriter, r *http.Request) {
	var in pauseIn
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		server.WriteError(w, 400, "请求格式不正确")
		return
	}
	if err := s.SetPublishPaused(r.Context(), who(r), in.Paused); err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, in)
}

func (s *Service) hGetPlatformPause(w http.ResponseWriter, r *http.Request) {
	server.WriteJSON(w, 200, map[string]bool{"paused": s.PlatformPublishPaused(r.Context()), "enabled": s.AutoPublish})
}

func (s *Service) hPlatformPause(w http.ResponseWriter, r *http.Request) {
	var in pauseIn
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		server.WriteError(w, 400, "请求格式不正确")
		return
	}
	if err := s.SetPlatformPublishPaused(r.Context(), who(r), in.Paused); err != nil {
		fail(w, err)
		return
	}
	server.WriteJSON(w, 200, map[string]bool{"paused": in.Paused, "enabled": s.AutoPublish})
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
