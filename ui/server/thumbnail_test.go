package server

import (
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestHandleThumbnailsApply_UsesJsonIn(t *testing.T) {
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

	// Intercept the spawn to capture opts without running a real process.
	var capturedOpts StartOptions
	SetTestSpawnHook(t, srv.runs, func(opts StartOptions) { capturedOpts = opts })

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

	// Assert new argv shape: --json-in present, --thumb-confirm and --thumb-dir absent.
	argsStr := strings.Join(capturedOpts.Args, " ")
	if !strings.Contains(argsStr, "--json-in") {
		t.Errorf("args missing --json-in; full args: %s", argsStr)
	}
	if !strings.Contains(argsStr, "--thumbnail-detect-apply") {
		t.Errorf("args missing --thumbnail-detect-apply; full args: %s", argsStr)
	}
	if strings.Contains(argsStr, "--thumb-confirm") {
		t.Errorf("args still contain --thumb-confirm (should be removed); full args: %s", argsStr)
	}
	if strings.Contains(argsStr, "--thumb-dir") {
		t.Errorf("args still contain --thumb-dir (should be removed); full args: %s", argsStr)
	}

	// Assert stdin carries the expected JSON-lines for the one thumbnail member.
	if capturedOpts.Stdin == nil {
		t.Fatal("Stdin is nil; expected JSON-lines stream")
	}
	stdinBytes, err := io.ReadAll(capturedOpts.Stdin)
	if err != nil {
		t.Fatalf("read Stdin: %v", err)
	}
	stdinStr := string(stdinBytes)
	if !strings.Contains(stdinStr, "apply_move") {
		t.Errorf("stdin missing apply_move; got: %s", stdinStr)
	}
	if !strings.Contains(stdinStr, "small.jpg") {
		t.Errorf("stdin missing small.jpg; got: %s", stdinStr)
	}
	if !strings.Contains(stdinStr, "thumb_l2_exif") {
		t.Errorf("stdin missing thumb_l2_exif; got: %s", stdinStr)
	}

	// No .thumb-confirm.tsv should be written.
	runsDir := filepath.Join(srv.opts.StateDir, "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", runsDir, err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".thumb-confirm.tsv") {
			t.Errorf("unexpected .thumb-confirm.tsv file found: %s", e.Name())
		}
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

func TestAppHTML_ThumbnailsNavLink(t *testing.T) {
	srv := newThumbTestServer(t)
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.handleIndex(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status = %d", w.Result().StatusCode)
	}
	body := w.Body.String()
	if !strings.Contains(body, `hx-get="/tab/thumbnails"`) {
		t.Error("sidebar missing Thumbnails nav link")
	}
	if strings.Contains(body, `nav-item disabled`) {
		t.Error("sidebar still has nav-item disabled class (stale)")
	}
	if strings.Contains(body, `muted-tag`) {
		t.Error("sidebar still has muted-tag soon badge (stale)")
	}
	if !strings.Contains(body, "stage 8") {
		t.Error("footer still says stage 4 or other old value")
	}
}

func TestRunningPanelTitle_ThumbnailModes(t *testing.T) {
	srv := newThumbTestServer(t)
	for _, tc := range []struct {
		mode string
		want string
	}{
		{"thumbnail_detect_preview", "Detecting thumbnails"},
		{"thumbnail_detect_apply", "Confirming thumbnail moves"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			var buf strings.Builder
			data := selfCheckRunningData{
				RunID:   "x",
				Folder:  "/photos",
				Mode:    tc.mode,
				NextURL: "/api/thumbnails/results/x",
			}
			if err := srv.tmpl.ExecuteTemplate(&buf, "selfcheck_running.html", data); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if !strings.Contains(buf.String(), tc.want) {
				t.Errorf("running panel title missing %q for mode=%s", tc.want, tc.mode)
			}
		})
	}
}

func TestHandleThumbnailsPreview_PassesThumbPrefixedFlags(t *testing.T) {
	srv := newThumbTestServer(t)
	srcPath := os.Getenv("HOME")

	form := url.Values{
		"source":             {srcPath},
		"max_edge":           {"600"},
		"maybe_max_edge":     {"1200"},
		"require_exif_match": {"on"},
	}
	req := httptest.NewRequest("POST", "/api/thumbnails/preview",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.handleThumbnailsPreview(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Result().StatusCode, w.Body.String())
	}

	// Extract the run ID from the response to verify args.
	body := w.Body.String()
	if !strings.Contains(body, "data-run-id") {
		t.Fatal("body missing data-run-id")
	}

	// Find the run ID by looking for data-run-id="..."
	var runID string
	for _, line := range strings.Split(body, "\n") {
		if idx := strings.Index(line, "data-run-id="); idx >= 0 {
			start := idx + len("data-run-id=\"")
			end := strings.Index(line[start:], "\"")
			if end > 0 {
				runID = line[start : start+end]
				break
			}
		}
	}
	if runID == "" {
		t.Fatal("could not extract runID from data-run-id attribute")
	}

	run := srv.runs.Get(runID)
	if run == nil {
		t.Fatalf("run not found: %s", runID)
	}

	snap := run.Snapshot()
	argsStr := strings.Join(snap.Args, " ")

	for _, want := range []string{
		"--thumb-max-edge", "600",
		"--thumb-maybe-max-edge", "1200",
		"--thumb-require-exif-match",
	} {
		if !strings.Contains(argsStr, want) {
			t.Errorf("args missing %q; full args: %s", want, argsStr)
		}
	}
}

func TestHandleThumbnailsApply_PassesJsonInAndSource(t *testing.T) {
	srv := newThumbTestServer(t)
	srcPath := os.Getenv("HOME")
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"prev-apply2","mode":"thumbnail_detect_preview","source":"` + srcPath + `"}`,
		`{"type":"thumb_candidate","ts":2,"run_id":"prev-apply2","decision":"thumb_l2_exif","path":"` + srcPath + `/s.jpg","keeper":"` + srcPath + `/b.jpg","group_id":"gapply2","width":100,"height":80,"size_bytes":1024}`,
		`{"type":"run_end","ts":3,"run_id":"prev-apply2","total":2,"dupes":0,"moved":0,"cancelled":false}`,
	})
	r.Mode = "thumbnail_detect_preview"
	r.Args = []string{"--thumbnail-detect", "--source", srcPath, "--dry-run", "--json-events"}
	storeRun(srv.runs, "prev-apply2", r)

	var capturedOpts StartOptions
	SetTestSpawnHook(t, srv.runs, func(opts StartOptions) { capturedOpts = opts })

	form := url.Values{
		"preview_run_id":        {"prev-apply2"},
		"group:gapply2.member1": {"on"},
	}
	req := httptest.NewRequest("POST", "/api/thumbnails/apply",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.handleThumbnailsApply(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Result().StatusCode, w.Body.String())
	}

	argsStr := strings.Join(capturedOpts.Args, " ")

	// New shape: --json-in replaces --thumb-dir / --thumb-confirm.
	if !strings.Contains(argsStr, "--json-in") {
		t.Errorf("args missing --json-in; full args: %s", argsStr)
	}
	if strings.Contains(argsStr, "--thumb-dir") {
		t.Errorf("args still contain --thumb-dir (should be removed); full args: %s", argsStr)
	}
	if strings.Contains(argsStr, "--thumb-confirm") {
		t.Errorf("args still contain --thumb-confirm (should be removed); full args: %s", argsStr)
	}

	// --source must still be present.
	if !strings.Contains(argsStr, "--source") {
		t.Errorf("args missing --source; full args: %s", argsStr)
	}
	if !strings.Contains(argsStr, srcPath) {
		t.Errorf("args missing source path %q; full args: %s", srcPath, argsStr)
	}

	// dstDir in stdin should reference _QUARANTINE/_thumbs, not _thumbnails.
	if capturedOpts.Stdin == nil {
		t.Fatal("Stdin is nil; expected JSON-lines stream")
	}
	stdinBytes, err := io.ReadAll(capturedOpts.Stdin)
	if err != nil {
		t.Fatalf("read Stdin: %v", err)
	}
	if !strings.Contains(string(stdinBytes), "_QUARANTINE/_thumbs") {
		t.Errorf("stdin dst_dir missing _QUARANTINE/_thumbs; got: %s", string(stdinBytes))
	}
}

func TestHandleThumbnailsApply_RejectsWrongMode(t *testing.T) {
	srv := newThumbTestServer(t)
	srcPath := os.Getenv("HOME")
	leakedMode := "internal_sensitive_mode"
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"wrong-mode","mode":"self_check_preview","source":"` + srcPath + `"}`,
		`{"type":"run_end","ts":2,"run_id":"wrong-mode","total":0,"dupes":0,"moved":0,"cancelled":false}`,
	})
	r.Mode = leakedMode
	r.Args = []string{"--self-check", "--source", srcPath, "--dry-run", "--json-events"}
	storeRun(srv.runs, "wrong-mode", r)

	form := url.Values{
		"preview_run_id": {"wrong-mode"},
	}
	req := httptest.NewRequest("POST", "/api/thumbnails/apply",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.handleThumbnailsApply(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d; body: %s", resp.StatusCode, http.StatusUnprocessableEntity, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, leakedMode) {
		t.Fatalf("wrong-mode preview leaked internal mode in response: %q", body)
	}
	if want := "preview_run_id refers to a non-thumbnail-preview run\n"; body != want {
		t.Fatalf("wrong-mode response = %q, want %q", body, want)
	}
}

func TestHandleThumbnailsApply_RejectsStillRunning(t *testing.T) {
	srv := newThumbTestServer(t)
	srcPath := os.Getenv("HOME")

	// Build a run with RunStatusRunning by creating a Run struct directly
	// and setting it to not have a closed done channel.
	r := &Run{
		ID:        "still-running",
		Mode:      "thumbnail_detect_preview",
		Args:      []string{"--thumbnail-detect", "--source", srcPath, "--dry-run", "--json-events"},
		StartedAt: time.Now(),
		status:    RunStatusRunning,
		events:    []Event{},
		done:      make(chan struct{}), // not closed
	}
	storeRun(srv.runs, "still-running", r)

	form := url.Values{
		"preview_run_id": {"still-running"},
	}
	req := httptest.NewRequest("POST", "/api/thumbnails/apply",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.handleThumbnailsApply(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body: %s", resp.StatusCode, http.StatusConflict, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "still in progress") {
		t.Errorf("body missing 'still in progress'; got: %s", body)
	}
}

func TestHandleThumbnailsApply_RejectsFailedRun(t *testing.T) {
	srv := newThumbTestServer(t)
	srcPath := os.Getenv("HOME")
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"failed-run","mode":"thumbnail_detect_preview","source":"` + srcPath + `"}`,
		`{"type":"run_end","ts":2,"run_id":"failed-run","total":0,"dupes":0,"moved":0,"cancelled":false,"exit_code":1}`,
	})
	r.Mode = "thumbnail_detect_preview"
	r.Args = []string{"--thumbnail-detect", "--source", srcPath, "--dry-run", "--json-events"}
	// Manually set status to Failed
	r.mu.Lock()
	r.status = RunStatusFailed
	r.mu.Unlock()
	storeRun(srv.runs, "failed-run", r)

	form := url.Values{
		"preview_run_id": {"failed-run"},
	}
	req := httptest.NewRequest("POST", "/api/thumbnails/apply",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.handleThumbnailsApply(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d; body: %s", resp.StatusCode, http.StatusUnprocessableEntity, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "did not succeed") {
		t.Errorf("body missing 'did not succeed'; got: %s", body)
	}
}

func TestHandleThumbnailsApply_NoTSVWritten(t *testing.T) {
	// Stage 9 T8: apply no longer writes a .thumb-confirm.tsv. Commands are
	// streamed via stdin (--json-in). Verify zero TSV files are written.
	srv := newThumbTestServer(t)
	srcPath := os.Getenv("HOME")

	previewID := "20260521T140000Z-stage85t6prev"
	previewRun := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"` + previewID + `","mode":"thumbnail_detect_preview","source":"` + srcPath + `"}`,
		`{"type":"thumb_candidate","ts":2,"run_id":"` + previewID + `","decision":"thumb_l2_exif","path":"` + srcPath + `/keeper.jpg","keeper":"` + srcPath + `/keeper.jpg","group_id":"l2:fake","width":400,"height":300,"size_bytes":8192}`,
		`{"type":"thumb_candidate","ts":3,"run_id":"` + previewID + `","decision":"thumb_l2_exif","path":"` + srcPath + `/thumb.jpg","keeper":"` + srcPath + `/keeper.jpg","group_id":"l2:fake","width":200,"height":150,"size_bytes":4096}`,
		`{"type":"run_end","ts":4,"run_id":"` + previewID + `","total":2,"dupes":0,"moved":0,"cancelled":false}`,
	})
	previewRun.Mode = "thumbnail_detect_preview"
	previewRun.Args = []string{"--thumbnail-detect", "--source", srcPath, "--dry-run", "--json-events"}
	storeRun(srv.runs, previewID, previewRun)

	SetTestSpawnHook(t, srv.runs, func(opts StartOptions) {}) // intercept so no real bash is spawned

	form := url.Values{}
	form.Set("preview_run_id", previewID)
	form.Set("group:l2:fake.member1", "on")

	req := httptest.NewRequest("POST", "/api/thumbnails/apply", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleThumbnailsApply(rec, req)

	if rec.Code/100 != 2 && rec.Code/100 != 3 {
		t.Fatalf("apply handler: status %d, body %s", rec.Code, rec.Body.String())
	}

	// Verify no .thumb-confirm.tsv files exist under stateDir/runs/.
	runsDir := filepath.Join(srv.opts.StateDir, "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		t.Fatalf("ReadDir runs: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".thumb-confirm.tsv") {
			t.Errorf("unexpected .thumb-confirm.tsv written: %s (Stage 9 T8: TSV replaced by --json-in stdin)", e.Name())
		}
	}
}
