package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeDB struct{ err error }

func (f fakeDB) Ping(context.Context) error { return f.err }

func get(t *testing.T, s http.Handler, path string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return rec, m
}

func TestHealth(t *testing.T) {
	rec, m := get(t, New(Options{DB: fakeDB{}}), "/api/health")
	if rec.Code != 200 || m["ok"] != true {
		t.Fatalf("got %d %v", rec.Code, m)
	}
	rec, m = get(t, New(Options{DB: fakeDB{err: errors.New("x")}}), "/api/health")
	if rec.Code != 503 || m["ok"] != false {
		t.Fatalf("got %d %v", rec.Code, m)
	}
}

func TestVersion(t *testing.T) {
	s := New(Options{Schema: func(context.Context) (int, error) { return 3, nil }})
	rec, m := get(t, s, "/api/version")
	if rec.Code != 200 || m["commit"] != "dev" || m["schema"] != float64(3) {
		t.Fatalf("got %d %v", rec.Code, m)
	}
}

func TestStaticFallback(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>app</html>"), 0o644)
	os.WriteFile(filepath.Join(dir, "a.js"), []byte("js"), 0o644)
	s := New(Options{WebDir: dir})
	if rec, _ := get(t, s, "/articles/5"); rec.Code != 200 || rec.Body.String() != "<html>app</html>" {
		t.Fatalf("spa fallback: %d %q", rec.Code, rec.Body.String())
	}
	if rec, _ := get(t, s, "/a.js"); rec.Body.String() != "js" {
		t.Fatal("asset not served")
	}
	if rec, _ := get(t, s, "/api/nope"); rec.Code != 404 {
		t.Fatal("unknown api must 404")
	}
	if rec, _ := get(t, s, "/../../etc/passwd"); strings.Contains(rec.Body.String(), "root:") {
		t.Fatal("path traversal not contained")
	}
}
