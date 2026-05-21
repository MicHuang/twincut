package server

import (
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// newCrossCheckTestServer builds a minimal Server with templates parsed from
// the on-disk templates directory. Mirrors newHistoryTestServer.
func newCrossCheckTestServer(t *testing.T) *Server {
	t.Helper()
	stateDir := t.TempDir()
	funcMap := template.FuncMap{
		"dict": func(args ...any) (map[string]any, error) {
			if len(args)%2 != 0 {
				return nil, fmt.Errorf("dict requires even number of args")
			}
			m := make(map[string]any, len(args)/2)
			for i := 0; i < len(args); i += 2 {
				key, ok := args[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict key %v is not a string", args[i])
				}
				m[key] = args[i+1]
			}
			return m, nil
		},
		"hasPrefix": strings.HasPrefix,
	}
	tmpl, err := template.New("").Funcs(funcMap).ParseGlob("../templates/*.html")
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	rm, err := NewRunManager(stateDir, "/dev/null")
	if err != nil {
		t.Fatalf("run manager: %v", err)
	}
	return &Server{
		opts: Options{
			StateDir:    stateDir,
			TwincutPath: "/dev/null",
		},
		tmpl:    tmpl,
		runs:    rm,
		recents: NewRecentsStore(stateDir),
	}
}

func TestParseCrossCheckForm_Valid(t *testing.T) {
	form := url.Values{
		"source": {"/home/me/photos"},
		"backup": {"/Volumes/bk1", "/Volumes/bk2"},
	}
	src, bks, err := parseCrossCheckForm(form)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if src != "/home/me/photos" {
		t.Errorf("source = %q, want %q", src, "/home/me/photos")
	}
	if !reflect.DeepEqual(bks, []string{"/Volumes/bk1", "/Volumes/bk2"}) {
		t.Errorf("backups = %v", bks)
	}
}

func TestParseCrossCheckForm_FiltersEmptyBackups(t *testing.T) {
	form := url.Values{
		"source": {"/home/me/photos"},
		"backup": {"/Volumes/bk1", "", "  ", "/Volumes/bk2"},
	}
	_, bks, err := parseCrossCheckForm(form)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !reflect.DeepEqual(bks, []string{"/Volumes/bk1", "/Volumes/bk2"}) {
		t.Errorf("backups = %v, want [/Volumes/bk1 /Volumes/bk2]", bks)
	}
}

func TestParseCrossCheckForm_RequiresSource(t *testing.T) {
	form := url.Values{
		"source": {""},
		"backup": {"/Volumes/bk1"},
	}
	_, _, err := parseCrossCheckForm(form)
	if err == nil {
		t.Fatal("want error for empty source, got nil")
	}
}

func TestParseCrossCheckForm_RequiresAtLeastOneBackup(t *testing.T) {
	form := url.Values{
		"source": {"/home/me/photos"},
		"backup": {"", "  "},
	}
	_, _, err := parseCrossCheckForm(form)
	if err == nil {
		t.Fatal("want error for no non-empty backups, got nil")
	}
}

func TestHandleCrossCheckTab_RendersForm(t *testing.T) {
	srv := newCrossCheckTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/tab/cross-check", nil)
	rec := httptest.NewRecorder()
	srv.handleCrossCheckTab(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, fragment := range []string{
		`name="source"`,
		`name="backup"`,
		`+ Add backup`,
		`Matching mode`,
	} {
		if !strings.Contains(body, fragment) {
			t.Errorf("body missing %q", fragment)
		}
	}
}

func TestHandleCrossCheckPreview_RejectsEmptySource(t *testing.T) {
	srv := newCrossCheckTestServer(t)
	form := url.Values{
		"source": {""},
		"backup": {"/Volumes/bk1"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/cross-check/preview",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleCrossCheckPreview(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCrossCheckPreview_RejectsNoBackups(t *testing.T) {
	srv := newCrossCheckTestServer(t)
	form := url.Values{
		"source": {"/Users/me/photos"},
		"backup": {""},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/cross-check/preview",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleCrossCheckPreview(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCrossCheckAddBackupRow_ReturnsRowFragment(t *testing.T) {
	srv := newCrossCheckTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/cross-check/add-backup-row", nil)
	rec := httptest.NewRecorder()
	srv.handleCrossCheckAddBackupRow(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `name="backup"`) {
		t.Errorf("row fragment missing backup input; got: %s", body)
	}
}
