package server

import (
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newThumbTestServer builds a Server with templates, a real RunManager, and a
// RecentsStore. HOME is set to a temp dir so allowlist checks pass.
func newThumbTestServer(t *testing.T) *Server {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
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
	stateDir := t.TempDir()
	rm, err := NewRunManager(stateDir, "/dev/null")
	if err != nil {
		t.Fatalf("NewRunManager: %v", err)
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

// storeRun injects a pre-built Run into the RunManager's in-memory map.
func storeRun(m *RunManager, id string, r *Run) {
	m.mu.Lock()
	m.runs[id] = r
	m.mu.Unlock()
}

func TestHandleThumbnailsTab(t *testing.T) {
	srv := newThumbTestServer(t)
	req := httptest.NewRequest("GET", "/tab/thumbnails", nil)
	w := httptest.NewRecorder()
	srv.handleThumbnailsTab(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := w.Body.String()
	if !strings.Contains(body, "<form") {
		t.Error("body missing <form element")
	}
	if !strings.Contains(body, `hx-post="/api/thumbnails/preview"`) {
		t.Error("body missing hx-post=/api/thumbnails/preview")
	}
	if !strings.Contains(body, `name="max_edge"`) {
		t.Error("body missing max_edge field")
	}
}

func TestHandleThumbnailsPreview_LaunchesRun(t *testing.T) {
	srv := newThumbTestServer(t)
	// Use HOME (set to a temp dir by newThumbTestServer) as source so it
	// passes the IsAllowedPath allowlist check.
	srcPath := os.Getenv("HOME")

	form := url.Values{
		"source":             {srcPath},
		"max_edge":           {"512"},
		"maybe_max_edge":     {"1024"},
		"require_exif_match": {"on"},
	}
	req := httptest.NewRequest("POST", "/api/thumbnails/preview",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.handleThumbnailsPreview(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "data-run-id") {
		t.Error("body missing data-run-id (running panel not rendered)")
	}
	if !strings.Contains(body, "/api/thumbnails/results/") {
		t.Error("body missing /api/thumbnails/results/ NextURL")
	}
}

func TestHandleThumbnailsPreview_DisallowedPath(t *testing.T) {
	srv := newThumbTestServer(t)
	form := url.Values{
		"source": {"/etc/passwd"},
	}
	req := httptest.NewRequest("POST", "/api/thumbnails/preview",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.handleThumbnailsPreview(w, req)
	sc := w.Result().StatusCode
	if sc != http.StatusUnprocessableEntity && sc != http.StatusForbidden {
		t.Errorf("status = %d, want 422 or 403 for disallowed path", sc)
	}
}

func TestHandleThumbnailsResults_BuildsView(t *testing.T) {
	srv := newThumbTestServer(t)
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"thumb-r1","mode":"thumbnail_detect_preview","source":"/photos"}`,
		`{"type":"thumb_candidate","ts":2,"run_id":"thumb-r1","decision":"thumb_l2_exif","path":"/photos/small.jpg","keeper":"/photos/big.jpg","group_id":"sha1test","width":200,"height":150,"size_bytes":4096}`,
		`{"type":"run_end","ts":3,"run_id":"thumb-r1","total":2,"dupes":0,"moved":0,"cancelled":false}`,
	})
	r.Mode = "thumbnail_detect_preview"
	storeRun(srv.runs, "thumb-r1", r)
	req := httptest.NewRequest("GET", "/api/thumbnails/results/thumb-r1", nil)
	req.SetPathValue("id", "thumb-r1")
	w := httptest.NewRecorder()
	srv.handleThumbnailsResults(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{
		"sha1test",
		"/photos/small.jpg",
		`hx-post="/api/thumbnails/apply"`,
		`name="preview_run_id"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestHandleThumbnailsApply_WritesCSV(t *testing.T) {
	srv := newThumbTestServer(t)
	// srcPath must be under HOME so the allowlist check in Apply passes.
	srcPath := os.Getenv("HOME")
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"prev-apply","mode":"thumbnail_detect_preview","source":"` + srcPath + `"}`,
		`{"type":"thumb_candidate","ts":2,"run_id":"prev-apply","decision":"thumb_l2_exif","path":"` + srcPath + `/small.jpg","keeper":"` + srcPath + `/big.jpg","group_id":"gapply","width":200,"height":150,"size_bytes":4096}`,
		`{"type":"run_end","ts":3,"run_id":"prev-apply","total":2,"dupes":0,"moved":0,"cancelled":false}`,
	})
	r.Mode = "thumbnail_detect_preview"
	// Set Args so extractArgValue("--source") finds the path.
	r.Args = []string{"--thumbnail-detect", "--source", srcPath, "--dry-run", "--json-events"}
	storeRun(srv.runs, "prev-apply", r)

	form := url.Values{
		"preview_run_id":       {"prev-apply"},
		"group:gapply.member1": {"on"}, // member0=keeper (skipped), member1=thumbnail
	}
	req := httptest.NewRequest("POST", "/api/thumbnails/apply",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.handleThumbnailsApply(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, w.Body.String())
	}
	runsDir := filepath.Join(srv.opts.StateDir, "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", runsDir, err)
	}
	var csvPath string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".thumb-confirm.csv") {
			csvPath = filepath.Join(runsDir, e.Name())
			break
		}
	}
	if csvPath == "" {
		t.Fatal("no .thumb-confirm.csv file found under StateDir/runs/")
	}
	data, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if !strings.Contains(string(data), "small.jpg") {
		t.Errorf("CSV does not contain small.jpg:\n%s", data)
	}
	if !strings.Contains(string(data), "thumb_l2_exif") {
		t.Errorf("CSV does not contain thumb_l2_exif decision:\n%s", data)
	}
}

func TestHandleThumbnailsApply_LaunchesWithArgs(t *testing.T) {
	srv := newThumbTestServer(t)
	srcPath := os.Getenv("HOME")
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"prev-args","mode":"thumbnail_detect_preview","source":"` + srcPath + `"}`,
		`{"type":"thumb_candidate","ts":2,"run_id":"prev-args","decision":"thumb_l2_exif","path":"` + srcPath + `/s.jpg","keeper":"` + srcPath + `/b.jpg","group_id":"gargs","width":100,"height":80,"size_bytes":1024}`,
		`{"type":"run_end","ts":3,"run_id":"prev-args","total":2,"dupes":0,"moved":0,"cancelled":false}`,
	})
	r.Mode = "thumbnail_detect_preview"
	// Set Args so extractArgValue("--source") finds the path.
	r.Args = []string{"--thumbnail-detect", "--source", srcPath, "--dry-run", "--json-events"}
	storeRun(srv.runs, "prev-args", r)

	form := url.Values{
		"preview_run_id":      {"prev-args"},
		"group:gargs.member1": {"on"},
	}
	req := httptest.NewRequest("POST", "/api/thumbnails/apply",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.handleThumbnailsApply(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Result().StatusCode, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "/api/thumbnails/done/") {
		t.Error("body missing /api/thumbnails/done/ next URL")
	}
	if !strings.Contains(body, "data-run-id") {
		t.Error("body missing data-run-id (running panel not rendered)")
	}
}

func TestHandleThumbnailsDone(t *testing.T) {
	srv := newThumbTestServer(t)
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"done-r1","mode":"thumbnail_detect_apply","source":"/photos","dry_run":false}`,
		`{"type":"run_end","ts":2,"run_id":"done-r1","total":3,"dupes":0,"moved":2,"manifest_path":"/photos/_thumbnails/_manifest-done.tsv","cancelled":false}`,
	})
	r.Mode = "thumbnail_detect_apply"
	storeRun(srv.runs, "done-r1", r)
	req := httptest.NewRequest("GET", "/api/thumbnails/done/done-r1", nil)
	req.SetPathValue("id", "done-r1")
	w := httptest.NewRecorder()
	srv.handleThumbnailsDone(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; body: %s", resp.StatusCode, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "2") {
		t.Error("done page does not show moved count 2")
	}
}

func TestHandleThumbnailsL1Row_RendersCheckbox(t *testing.T) {
	srv := newThumbTestServer(t)
	type rowData struct {
		Member  ResultMember
		GroupID string
		Index   int
	}
	data := rowData{
		Member: ResultMember{
			Path:   "/photos/suspect.jpg",
			Reason: "l1_only_thumb",
			Width:  200,
			Height: 150,
		},
		GroupID: "l1-suspects",
		Index:   0,
	}
	var buf strings.Builder
	if err := srv.tmpl.ExecuteTemplate(&buf, "thumbnails_l1_row.html", data); err != nil {
		t.Fatalf("execute template: %v", err)
	}
	body := buf.String()
	for _, want := range []string{
		"/photos/suspect.jpg",
		"l1_only_thumb",
		`name="group:l1-suspects.member0"`,
		`/thumb?path=`,
		`200`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}
