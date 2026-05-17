package server

import (
	"fmt"
	"html/template"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// newHistoryTestServer builds a minimal Server with templates parsed from the
// on-disk templates directory (relative to the package under test).
func newHistoryTestServer(t *testing.T, stateDir string) *Server {
	t.Helper()
	tmpl, err := template.ParseGlob("../templates/*.html")
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	return &Server{
		opts: Options{
			StateDir:    stateDir,
			TwincutPath: "/dev/null",
		},
		tmpl: tmpl,
	}
}

func writeNDJSON(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCollectHistory_FiltersAndSorts(t *testing.T) {
	state := t.TempDir()
	runs := filepath.Join(state, "runs")

	// 1. Self-check apply (dry_run=false, moved>0) — keep.
	writeNDJSON(t, filepath.Join(runs, "A.ndjson"),
		`{"type":"run_start","ts":100,"run_id":"A","mode":"self_check","source":"/p/a","dry_run":false}`,
		`{"type":"run_end","ts":110,"run_id":"A","moved":2,"manifest_path":"/p/a/_QUARANTINE/_m.tsv","cancelled":false}`,
	)
	// 2. Self-check preview (dry_run=true) — filter out, nothing to restore.
	writeNDJSON(t, filepath.Join(runs, "B.ndjson"),
		`{"type":"run_start","ts":200,"run_id":"B","mode":"self_check","source":"/p/b","dry_run":true}`,
		`{"type":"run_end","ts":201,"run_id":"B","moved":0,"manifest_path":"","cancelled":false}`,
	)
	// 3. Self-check apply but no moves — filter out (nothing to restore).
	writeNDJSON(t, filepath.Join(runs, "C.ndjson"),
		`{"type":"run_start","ts":300,"run_id":"C","mode":"self_check","source":"/p/c","dry_run":false}`,
		`{"type":"run_end","ts":301,"run_id":"C","moved":0,"manifest_path":"","cancelled":false}`,
	)
	// 4. Self-check apply, cancelled-partial (moved>0) — keep.
	writeNDJSON(t, filepath.Join(runs, "D.ndjson"),
		`{"type":"run_start","ts":400,"run_id":"D","mode":"self_check","source":"/p/d","dry_run":false}`,
		`{"type":"run_end","ts":410,"run_id":"D","moved":5,"manifest_path":"/p/d/_QUARANTINE/_m.tsv","cancelled":true}`,
	)
	// 5. Apply with no run_end (process killed) — filter out.
	writeNDJSON(t, filepath.Join(runs, "E.ndjson"),
		`{"type":"run_start","ts":500,"run_id":"E","mode":"self_check","source":"/p/e","dry_run":false}`,
	)
	// 6. Cross-check apply (dry_run=false, moved>0) — keep.
	writeNDJSON(t, filepath.Join(runs, "F.ndjson"),
		`{"type":"run_start","ts":600,"run_id":"F","mode":"cross_check","source":"/p/src","backups":["/p/bk"],"dry_run":false}`,
		`{"type":"run_end","ts":610,"run_id":"F","moved":3,"manifest_path":"/p/src/_QUARANTINE/_m.tsv","cancelled":false}`,
	)
	// 7. Cross-check preview (dry_run=true) — filter out.
	writeNDJSON(t, filepath.Join(runs, "G.ndjson"),
		`{"type":"run_start","ts":700,"run_id":"G","mode":"cross_check","source":"/p/src","backups":["/p/bk"],"dry_run":true}`,
		`{"type":"run_end","ts":701,"run_id":"G","moved":0,"manifest_path":"","cancelled":false}`,
	)
	// 8. Restore run (mode=restore) — filter out, not an apply.
	writeNDJSON(t, filepath.Join(runs, "H.ndjson"),
		`{"type":"run_start","ts":800,"run_id":"H","mode":"restore","source":"/p/a/_QUARANTINE/_m.tsv","dry_run":false}`,
		`{"type":"run_end","ts":801,"run_id":"H","restored":2,"cancelled":false}`,
	)

	got, err := collectHistory(state)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].Timestamp > got[j].Timestamp })

	// Expect: A (self_check apply), D (self_check cancelled-partial), F (cross_check apply).
	if len(got) != 3 {
		t.Fatalf("want 3 entries, got %d: %+v", len(got), got)
	}
	gotIDs := []string{got[0].RunID, got[1].RunID, got[2].RunID}
	wantIDs := []string{"F", "D", "A"} // sorted by timestamp desc
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Errorf("ordering mismatch: got %v, want %v", gotIDs, wantIDs)
	}

	// Cross-check entry F should have Mode "cross_check".
	for _, e := range got {
		if e.RunID == "F" {
			if e.Mode != "cross_check" {
				t.Errorf("entry F Mode = %q, want %q", e.Mode, "cross_check")
			}
		}
		if e.RunID == "A" || e.RunID == "D" {
			if e.Mode != "self_check" {
				t.Errorf("entry %s Mode = %q, want %q", e.RunID, e.Mode, "self_check")
			}
		}
	}
}

func TestCollectHistory_RestoredSidecarDetected(t *testing.T) {
	state := t.TempDir()
	runs := filepath.Join(state, "runs")
	manifest := filepath.Join(state, "scratch", "_manifest-A.tsv")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest+".restored", []byte("/p/a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeNDJSON(t, filepath.Join(runs, "A.ndjson"),
		`{"type":"run_start","ts":100,"run_id":"A","mode":"self_check","source":"/p/a","dry_run":false}`,
		`{"type":"run_end","ts":110,"run_id":"A","moved":1,"manifest_path":"`+manifest+`","cancelled":false}`,
	)
	got, err := collectHistory(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Restored {
		t.Errorf("expected one entry with Restored=true; got %+v", got)
	}
}

func TestCollectHistory_EmptyDir(t *testing.T) {
	got, err := collectHistory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("empty stateDir yielded %d entries; want 0", len(got))
	}
}

func TestResolveManifest_SuccessAndMissing(t *testing.T) {
	// The manifest path must pass IsAllowedPath (which requires $HOME or /Volumes).
	// t.TempDir() on macOS resolves under /var/folders which is outside the
	// allowlist, so we create a scratch dir directly under $HOME and clean up.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	scratchDir := filepath.Join(home, fmt.Sprintf(".twincut-test-tmp-%d", rand.Int()))
	if err := os.MkdirAll(scratchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(scratchDir) })

	state := filepath.Join(scratchDir, "state")
	runs := filepath.Join(state, "runs")
	manifest := filepath.Join(scratchDir, "scratch", "_m.tsv")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(manifest, []byte(""), 0o644)
	writeNDJSON(t, filepath.Join(runs, "OK.ndjson"),
		`{"type":"run_start","ts":1,"run_id":"OK","mode":"self_check","source":"/p","dry_run":false}`,
		`{"type":"run_end","ts":2,"run_id":"OK","moved":1,"manifest_path":"`+manifest+`","cancelled":false}`,
	)
	writeNDJSON(t, filepath.Join(runs, "GONE.ndjson"),
		`{"type":"run_start","ts":3,"run_id":"GONE","mode":"self_check","source":"/p","dry_run":false}`,
		`{"type":"run_end","ts":4,"run_id":"GONE","moved":1,"manifest_path":"/nope/_m.tsv","cancelled":false}`,
	)

	gotPath, err := resolveManifest(state, "OK")
	if err != nil {
		t.Fatalf("resolveManifest OK: %v", err)
	}
	if gotPath != manifest {
		t.Errorf("got %q; want %q", gotPath, manifest)
	}
	if _, err := resolveManifest(state, "GONE"); err == nil {
		t.Errorf("expected error for missing manifest; got nil")
	}
	if _, err := resolveManifest(state, "NO_SUCH_RUN"); err == nil {
		t.Errorf("expected error for unknown run; got nil")
	}
}

func TestHandleHistoryTab_RendersEntries(t *testing.T) {
	state := t.TempDir()
	runs := filepath.Join(state, "runs")
	manifest := filepath.Join(state, "_m.tsv")
	os.WriteFile(manifest, []byte(""), 0o644)
	writeNDJSON(t, filepath.Join(runs, "A.ndjson"),
		`{"type":"run_start","ts":100,"run_id":"A","mode":"self_check","source":"/p/a","dry_run":false}`,
		`{"type":"run_end","ts":110,"run_id":"A","moved":3,"manifest_path":"`+manifest+`","cancelled":false}`,
	)
	s := newHistoryTestServer(t, state)
	req := httptest.NewRequest("GET", "/tab/history", nil)
	w := httptest.NewRecorder()
	s.handleHistoryTab(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "/p/a") {
		t.Errorf("response missing folder /p/a; body=\n%s", body)
	}
	if !strings.Contains(body, "/history/A/preview") {
		t.Errorf("response missing restore link; body=\n%s", body)
	}
}

func TestHandleHistoryTab_EmptyState(t *testing.T) {
	state := t.TempDir()
	s := newHistoryTestServer(t, state)
	req := httptest.NewRequest("GET", "/tab/history", nil)
	w := httptest.NewRecorder()
	s.handleHistoryTab(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "No history yet") {
		t.Errorf("missing empty-state message")
	}
}
