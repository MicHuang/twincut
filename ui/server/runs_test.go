package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// findTwincut walks up from the test working dir to locate bin/twincut.sh.
// Tests need a real script — these are integration tests by design, since the
// run manager's contract is "spawn twincut.sh and parse its output."
func findTwincut(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for i := 0; i < 6; i++ {
		p := filepath.Join(dir, "bin", "twincut.sh")
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
		dir = filepath.Dir(dir)
	}
	t.Skip("bin/twincut.sh not found above CWD; skipping integration test")
	return ""
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestRunManager_SelfCheckDryRun(t *testing.T) {
	twincut := findTwincut(t)
	state := t.TempDir()
	fixture := t.TempDir()
	writeFile(t, filepath.Join(fixture, "a.jpg"), "duplicate-content")
	writeFile(t, filepath.Join(fixture, "b.jpg"), "duplicate-content")
	writeFile(t, filepath.Join(fixture, "u.jpg"), "unique-here")

	mgr, err := NewRunManager(state, twincut)
	if err != nil {
		t.Fatalf("NewRunManager: %v", err)
	}
	run, err := mgr.Start(StartOptions{
		Mode: "self_check_test",
		Args: []string{"--self-check", fixture, "--dry-run"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case <-run.Done():
	case <-time.After(30 * time.Second):
		t.Fatalf("run did not finish within timeout; status=%s", run.Status())
	}

	if run.Status() != RunStatusSucceeded {
		t.Fatalf("run status = %s, want succeeded (exit=%d)", run.Status(), run.ExitCode)
	}

	events := run.EventsSince(0)
	if len(events) < 3 {
		t.Fatalf("got %d events, want at least run_start + dup_group + run_end", len(events))
	}
	if events[0].Type != EventRunStart {
		t.Errorf("first event type = %s, want run_start", events[0].Type)
	}
	if events[len(events)-1].Type != EventRunEnd {
		t.Errorf("last event type = %s, want run_end", events[len(events)-1].Type)
	}

	// Each event must have a monotonically increasing seq.
	for i, ev := range events {
		if ev.Seq != i+1 {
			t.Errorf("event %d: seq = %d, want %d", i, ev.Seq, i+1)
		}
	}

	// Run id must match across all events and equal the run's ID.
	for i, ev := range events {
		if ev.RunID != run.ID {
			t.Errorf("event %d: run_id = %q, want %q", i, ev.RunID, run.ID)
		}
	}

	// Find the dup_group event and verify the keep + remove paths.
	var dup *Event
	for i := range events {
		if events[i].Type == EventDupGroup {
			dup = &events[i]
			break
		}
	}
	if dup == nil {
		t.Fatalf("no dup_group event in stream")
	}
	var payload struct {
		KeepPath string `json:"keep_path"`
		Remove   []struct {
			Path string `json:"path"`
		} `json:"remove"`
	}
	if err := json.Unmarshal(dup.Raw, &payload); err != nil {
		t.Fatalf("dup_group JSON: %v", err)
	}
	expected := map[string]bool{
		filepath.Join(fixture, "a.jpg"): false,
		filepath.Join(fixture, "b.jpg"): false,
	}
	if _, ok := expected[payload.KeepPath]; !ok {
		t.Errorf("unexpected keep_path %q", payload.KeepPath)
	}
	expected[payload.KeepPath] = true
	if len(payload.Remove) != 1 {
		t.Errorf("len(remove) = %d, want 1", len(payload.Remove))
	} else if _, ok := expected[payload.Remove[0].Path]; !ok || expected[payload.Remove[0].Path] {
		t.Errorf("unexpected remove path %q", payload.Remove[0].Path)
	}

	// Journal file must exist and contain the same number of events.
	journal := filepath.Join(state, "runs", run.ID+".ndjson")
	data, err := os.ReadFile(journal)
	if err != nil {
		t.Fatalf("journal read: %v", err)
	}
	lines := 0
	for _, b := range data {
		if b == '\n' {
			lines++
		}
	}
	if lines != len(events) {
		t.Errorf("journal lines = %d, events = %d", lines, len(events))
	}
}

func TestRunManager_SubscribeReceivesLiveEvents(t *testing.T) {
	twincut := findTwincut(t)
	state := t.TempDir()
	fixture := t.TempDir()
	writeFile(t, filepath.Join(fixture, "a.jpg"), "live-test")
	writeFile(t, filepath.Join(fixture, "b.jpg"), "live-test")

	mgr, err := NewRunManager(state, twincut)
	if err != nil {
		t.Fatalf("NewRunManager: %v", err)
	}
	run, err := mgr.Start(StartOptions{
		Mode: "live_test",
		Args: []string{"--self-check", fixture, "--dry-run"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	_, ch, unsub := run.Subscribe()
	defer unsub()

	// Drain everything that arrives until the run finishes. Since the
	// run is very fast, some events may have arrived BEFORE Subscribe
	// (race). We accept that here — the test is about subscription
	// delivery, not no-loss replay (covered by EventsSince in the SSE
	// handler).
	got := []Event{}
	timeout := time.After(30 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				goto done
			}
			got = append(got, ev)
		case <-timeout:
			t.Fatalf("subscriber did not see run end within timeout")
		}
	}
done:
	if run.Status() != RunStatusSucceeded {
		t.Fatalf("run status = %s, want succeeded", run.Status())
	}
}

func TestRunManager_StartFailsForBadArgs(t *testing.T) {
	twincut := findTwincut(t)
	state := t.TempDir()
	mgr, err := NewRunManager(state, twincut)
	if err != nil {
		t.Fatalf("NewRunManager: %v", err)
	}
	run, err := mgr.Start(StartOptions{
		Mode: "bad_args",
		Args: []string{"--self-check", "/no/such/path/should/exist", "--dry-run"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-run.Done():
	case <-time.After(15 * time.Second):
		t.Fatalf("run did not finish; status=%s", run.Status())
	}
	if run.Status() != RunStatusFailed {
		t.Errorf("status = %s, want failed (exit=%d)", run.Status(), run.ExitCode)
	}
	// Should still emit at least an error event.
	saw := false
	for _, ev := range run.EventsSince(0) {
		if ev.Type == EventError {
			saw = true
			break
		}
	}
	if !saw {
		t.Errorf("no error event in stream")
	}
}
