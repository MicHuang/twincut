package server

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

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

	// 1. Self-check apply, success, moved=2.
	writeNDJSON(t, filepath.Join(runs, "A.ndjson"),
		`{"type":"run_start","ts":100,"run_id":"A","mode":"self_check_apply","source":"/p/a"}`,
		`{"type":"run_end","ts":110,"run_id":"A","moved":2,"manifest_path":"/p/a/_QUARANTINE/_m.tsv","cancelled":false}`,
	)
	// 2. Self-check preview — must be filtered out.
	writeNDJSON(t, filepath.Join(runs, "B.ndjson"),
		`{"type":"run_start","ts":200,"run_id":"B","mode":"self_check","source":"/p/b"}`,
		`{"type":"run_end","ts":201,"run_id":"B","moved":0,"manifest_path":"","cancelled":false}`,
	)
	// 3. Self-check apply but no moves — filtered out (nothing to restore).
	writeNDJSON(t, filepath.Join(runs, "C.ndjson"),
		`{"type":"run_start","ts":300,"run_id":"C","mode":"self_check_apply","source":"/p/c"}`,
		`{"type":"run_end","ts":301,"run_id":"C","moved":0,"manifest_path":"","cancelled":false}`,
	)
	// 4. Self-check apply, cancelled-partial (moved>0) — keep.
	writeNDJSON(t, filepath.Join(runs, "D.ndjson"),
		`{"type":"run_start","ts":400,"run_id":"D","mode":"self_check_apply","source":"/p/d"}`,
		`{"type":"run_end","ts":410,"run_id":"D","moved":5,"manifest_path":"/p/d/_QUARANTINE/_m.tsv","cancelled":true}`,
	)
	// 5. Apply with no run_end (process killed) — filtered out.
	writeNDJSON(t, filepath.Join(runs, "E.ndjson"),
		`{"type":"run_start","ts":500,"run_id":"E","mode":"self_check_apply","source":"/p/e"}`,
	)

	got, err := collectHistory(state)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].Timestamp > got[j].Timestamp })

	wantIDs := []string{"D", "A"}
	gotIDs := make([]string, len(got))
	for i, e := range got {
		gotIDs[i] = e.RunID
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Errorf("history IDs (desc by ts) = %v; want %v", gotIDs, wantIDs)
	}
	if got[0].Status != "cancelled-partial" {
		t.Errorf("entry D status = %q; want cancelled-partial", got[0].Status)
	}
	if got[1].Status != "success" {
		t.Errorf("entry A status = %q; want success", got[1].Status)
	}
	if got[1].Folder != "/p/a" {
		t.Errorf("entry A folder = %q; want /p/a", got[1].Folder)
	}
	if got[1].MovedCount != 2 {
		t.Errorf("entry A moved = %d; want 2", got[1].MovedCount)
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
		`{"type":"run_start","ts":100,"run_id":"A","mode":"self_check_apply","source":"/p/a"}`,
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
	state := t.TempDir()
	runs := filepath.Join(state, "runs")
	manifest := filepath.Join(state, "scratch", "_m.tsv")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(manifest, []byte(""), 0o644)
	writeNDJSON(t, filepath.Join(runs, "OK.ndjson"),
		`{"type":"run_start","ts":1,"run_id":"OK","mode":"self_check_apply","source":"/p"}`,
		`{"type":"run_end","ts":2,"run_id":"OK","moved":1,"manifest_path":"`+manifest+`","cancelled":false}`,
	)
	writeNDJSON(t, filepath.Join(runs, "GONE.ndjson"),
		`{"type":"run_start","ts":3,"run_id":"GONE","mode":"self_check_apply","source":"/p"}`,
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
