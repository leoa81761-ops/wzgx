package svccfg

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"gonghao/internal/auth"
	"gonghao/internal/server"
)

func (s *Service) Register(srv *server.Server, h *auth.Handlers) {
	staff := func(f http.HandlerFunc) http.Handler { return h.Require(auth.StaffOnly, f) }
	root := func(f http.HandlerFunc) http.Handler { return h.Require(auth.SuperAdminOnly, f) }
	srv.Handle("GET /api/admin/configs", staff(s.hList))
	srv.Handle("GET /api/admin/configs/export", root(s.hExport))
	srv.Handle("POST /api/admin/configs/import", root(s.hImport))
	srv.Handle("GET /api/admin/configs/{id}", staff(s.hGet))
	srv.Handle("POST /api/admin/configs", root(s.hSave))
	srv.Handle("PUT /api/admin/configs/{id}", root(s.hSave))
	srv.Handle("DELETE /api/admin/configs/{id}", root(s.hDelete))
	srv.Handle("POST /api/admin/configs/{id}/test", root(s.hTest))
	srv.Handle("PUT /api/admin/configs/{id}/pricing", root(s.hPricing))
	srv.Handle("GET /api/admin/routes", staff(s.hRoutes))
	srv.Handle("PUT /api/admin/routes/{stage}", root(s.hSaveRoute))
	srv.Handle("DELETE /api/admin/routes/{stage}", root(s.hDeleteRoute))
}

func id(r *http.Request) int64        { n, _ := strconv.ParseInt(r.PathValue("id"), 10, 64); return n }
func actor(r *http.Request) auth.User { u, _ := auth.FromContext(r.Context()); return u }

func reply(w http.ResponseWriter, code int, v any, err error) {
	var ve ValidationError
	switch {
	case err == nil:
		server.WriteJSON(w, code, v)
	case errors.As(err, &ve):
		server.WriteError(w, http.StatusBadRequest, ve.Error())
	case errors.Is(err, ErrNotFound):
		server.WriteError(w, http.StatusNotFound, "不存在")
	case errors.Is(err, ErrForbidden):
		server.WriteError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, ErrInUse):
		server.WriteError(w, http.StatusConflict, err.Error())
	default:
		server.WriteError(w, http.StatusInternalServerError, "服务器错误")
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		server.WriteError(w, http.StatusBadRequest, "请求格式不正确")
		return false
	}
	return true
}

func (s *Service) hList(w http.ResponseWriter, r *http.Request) {
	cs, err := s.List(r.Context(), r.URL.Query().Get("kind"))
	reply(w, 200, cs, err)
}

func (s *Service) hGet(w http.ResponseWriter, r *http.Request) {
	c, err := s.Get(r.Context(), id(r))
	reply(w, 200, c, err)
}

func (s *Service) hSave(w http.ResponseWriter, r *http.Request) {
	var in Input
	if decode(w, r, &in) {
		code := 200
		if id(r) == 0 {
			code = 201
		}
		c, err := s.Save(r.Context(), actor(r), id(r), in)
		reply(w, code, c, err)
	}
}

func (s *Service) hDelete(w http.ResponseWriter, r *http.Request) {
	reply(w, 200, map[string]bool{"ok": true}, s.Delete(r.Context(), actor(r), id(r)))
}

func (s *Service) hTest(w http.ResponseWriter, r *http.Request) {
	res, err := s.TestConnection(r.Context(), actor(r), id(r))
	reply(w, 200, res, err)
}

func (s *Service) hRoutes(w http.ResponseWriter, r *http.Request) {
	rs, err := s.Routes(r.Context())
	reply(w, 200, map[string]any{"routes": rs, "stages": RouteStages}, err)
}

func (s *Service) hSaveRoute(w http.ResponseWriter, r *http.Request) {
	var in Route
	if decode(w, r, &in) {
		in.Stage = r.PathValue("stage")
		out, err := s.SaveRoute(r.Context(), actor(r), in)
		reply(w, 200, out, err)
	}
}

func (s *Service) hDeleteRoute(w http.ResponseWriter, r *http.Request) {
	reply(w, 200, map[string]bool{"ok": true}, s.DeleteRoute(r.Context(), actor(r), r.PathValue("stage"), r.URL.Query().Get("tier")))
}

func (s *Service) hPricing(w http.ResponseWriter, r *http.Request) {
	var p Pricing
	if decode(w, r, &p) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		out, err := s.SavePricing(r.Context(), actor(r), id, p)
		reply(w, 200, out, err)
	}
}

func (s *Service) hExport(w http.ResponseWriter, r *http.Request) {
	data, err := s.Export(r.Context(), actor(r))
	if err != nil {
		reply(w, 500, nil, err)
		return
	}
	filename := fmt.Sprintf("gonghao-config-%s.json", time.Now().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	server.WriteJSON(w, 200, data)
}

func (s *Service) hImport(w http.ResponseWriter, r *http.Request) {
	var in ExportData
	if decode(w, r, &in) {
		res, err := s.Import(r.Context(), actor(r), in)
		reply(w, 200, res, err)
	}
}
