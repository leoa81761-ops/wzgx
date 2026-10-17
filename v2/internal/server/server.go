// Package server wires HTTP routes. Each feature package registers its own handlers.
package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gonghao/internal/version"
)

// Pinger reports database health.
type Pinger interface{ Ping(ctx context.Context) error }

type Server struct {
	mux    *http.ServeMux
	db     Pinger
	schema func(ctx context.Context) (int, error)
	webDir string
	log    *slog.Logger
}

type Options struct {
	DB     Pinger
	Schema func(ctx context.Context) (int, error) // applied schema version
	WebDir string
	Log    *slog.Logger
}

func New(o Options) *Server {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	s := &Server{mux: http.NewServeMux(), db: o.DB, schema: o.Schema, webDir: o.WebDir, log: o.Log}
	s.mux.HandleFunc("GET /api/health", s.health)
	s.mux.HandleFunc("GET /api/version", s.version)
	s.mux.HandleFunc("/", s.static)
	return s
}

// Handle lets feature packages register routes, e.g. "POST /api/auth/login".
func (s *Server) Handle(pattern string, h http.Handler) { s.mux.Handle(pattern, h) }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rw := &statusWriter{ResponseWriter: w, code: 200}
	defer func() {
		if rec := recover(); rec != nil {
			s.log.Error("panic", "path", r.URL.Path, "err", rec)
			if !rw.wrote {
				WriteError(rw, http.StatusInternalServerError, "internal error")
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			s.log.Info("http", "method", r.Method, "path", r.URL.Path, "status", rw.code, "ms", time.Since(start).Milliseconds())
		}
	}()
	rw.Header().Set("X-Content-Type-Options", "nosniff")
	s.mux.ServeHTTP(rw, r)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if s.db != nil {
		if err := s.db.Ping(ctx); err != nil {
			WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "db": "down"})
			return
		}
	}
	WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "db": "up"})
}

func (s *Server) version(w http.ResponseWriter, r *http.Request) {
	info := version.Info{Commit: version.Commit, BuiltAt: version.BuiltAt}
	if s.schema != nil {
		if v, err := s.schema(r.Context()); err == nil {
			info.Schema = v
		}
	}
	WriteJSON(w, http.StatusOK, info)
}

// static serves the built SPA; unknown non-API paths fall back to index.html.
func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		WriteError(w, http.StatusNotFound, "not found")
		return
	}
	if s.webDir == "" {
		http.NotFound(w, r)
		return
	}
	clean := filepath.Clean("/" + r.URL.Path)
	p := filepath.Join(s.webDir, clean)
	if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
		http.ServeFile(w, r, p)
		return
	}
	index := filepath.Join(s.webDir, "index.html")
	if _, err := os.Stat(index); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, index)
}

func WriteJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func WriteError(w http.ResponseWriter, code int, msg string) {
	WriteJSON(w, code, map[string]string{"error": msg})
}

type statusWriter struct {
	http.ResponseWriter
	code  int
	wrote bool
}

func (w *statusWriter) WriteHeader(c int) { w.code, w.wrote = c, true; w.ResponseWriter.WriteHeader(c) }
func (w *statusWriter) Write(b []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(b)
}
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
