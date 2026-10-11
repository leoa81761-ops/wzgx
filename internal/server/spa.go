package server

import (
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/leoa81761-ops/wzgx/internal/web"
)

func (a *App) spa(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "." {
		name = ""
	}
	if name == "api" || strings.HasPrefix(name, "api/") {
		writeJSON(w, http.StatusNotFound, map[string]any{"detail": "接口不存在"})
		return
	}
	if name != "" {
		if body, ok := a.readStatic(name); ok {
			w.Header().Set("Content-Type", contentType(name))
			_, _ = w.Write(body)
			return
		}
	}
	if body, ok := a.readStatic("index.html"); ok {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(body)
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"detail": "前端尚未构建"})
}

func (a *App) readStatic(name string) ([]byte, bool) {
	if strings.Contains(name, "..") {
		return nil, false
	}
	if a.Cfg.StaticDir != "" {
		body, err := os.ReadFile(path.Join(a.Cfg.StaticDir, name))
		if err == nil {
			return body, true
		}
		return nil, false
	}
	body, err := fs.ReadFile(web.Dist, "dist/"+name)
	if err != nil {
		return nil, false
	}
	return body, true
}

func contentType(name string) string {
	switch {
	case strings.HasSuffix(name, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(name, ".js"):
		return "text/javascript; charset=utf-8"
	case strings.HasSuffix(name, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(name, ".png"):
		return "image/png"
	case strings.HasSuffix(name, ".json"):
		return "application/json; charset=utf-8"
	case strings.HasSuffix(name, ".woff2"):
		return "font/woff2"
	default:
		return "application/octet-stream"
	}
}
