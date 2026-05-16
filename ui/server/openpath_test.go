package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func newOpenTestServer(t *testing.T, home string) *Server {
	t.Helper()
	t.Setenv("HOME", home)
	return &Server{
		opts: Options{
			StateDir:    t.TempDir(),
			TwincutPath: "/dev/null",
		},
	}
}

func postOpen(t *testing.T, srv *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"path": {path}}
	req := httptest.NewRequest("POST", "/api/open", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleOpenPath(rec, req)
	return rec
}

func TestOpenPath_RequiresPath(t *testing.T) {
	srv := newOpenTestServer(t, t.TempDir())
	rec := postOpen(t, srv, "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestOpenPath_DisallowedPath(t *testing.T) {
	srv := newOpenTestServer(t, t.TempDir())
	rec := postOpen(t, srv, "/etc")
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestOpenPath_MissingPath(t *testing.T) {
	home := t.TempDir()
	srv := newOpenTestServer(t, home)
	rec := postOpen(t, srv, filepath.Join(home, "nope"))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// We deliberately skip a "successful spawn opens Finder" test — running it
// would pop a Finder window on every `go test` invocation. The success path
// is exercised by the manual browser smoke test instead.
