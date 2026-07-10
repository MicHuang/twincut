package server

import (
	"bufio"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestRunMode_ThumbnailModes verifies that a Run with Mode set to the new
// thumbnail modes does not cause BuildResults to error or panic.
func TestRunMode_ThumbnailModes(t *testing.T) {
	modes := []string{"thumbnail_detect_preview", "thumbnail_detect_apply"}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			r := &Run{
				ID:        "synthetic-" + mode,
				Mode:      mode,
				StartedAt: time.Now(),
				status:    RunStatusSucceeded,
				done:      make(chan struct{}),
			}
			close(r.done)
			view, err := BuildResults(r)
			if err != nil {
				t.Errorf("BuildResults with mode %q returned error: %v", mode, err)
			}
			if view.ApplyURL != "/api/thumbnails/apply" {
				t.Errorf("ApplyURL = %q, want /api/thumbnails/apply", view.ApplyURL)
			}
		})
	}
}

// TestRunMode_UnknownModeIsPassthrough verifies that an unknown mode string
// does not cause BuildResults to panic — it falls through to the safe default.
func TestRunMode_UnknownModeIsPassthrough(t *testing.T) {
	r := &Run{
		ID:        "synthetic-garbage",
		Mode:      "thumbnail_garbage",
		StartedAt: time.Now(),
		status:    RunStatusSucceeded,
		done:      make(chan struct{}),
	}
	close(r.done)
	view, err := BuildResults(r)
	if err != nil {
		t.Errorf("BuildResults with unknown mode errored: %v", err)
	}
	if view.ApplyURL == "" {
		t.Error("ApplyURL is empty for unknown mode; expected safe fallback")
	}
}

func newTestRunManager(t *testing.T) *RunManager {
	t.Helper()
	tmp := t.TempDir()
	mgr, err := NewRunManager(tmp, "/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	return mgr
}

func TestStart_GeneratesIDWhenOptsIDEmpty(t *testing.T) {
	mgr := newTestRunManager(t)
	r, err := mgr.Start(StartOptions{
		Mode: "self_check",
		Args: []string{"--help"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if r.ID == "" {
		t.Fatal("Run.ID empty")
	}
	if !regexp.MustCompile(`^\d{8}T\d{6}Z-[a-z0-9]+$`).MatchString(r.ID) {
		t.Errorf("Run.ID does not match expected shape: %q", r.ID)
	}
}

func TestStart_RejectsMalformedCallerID(t *testing.T) {
	mgr := newTestRunManager(t)
	bad := []string{
		"../etc/passwd",
		"not-a-run-id",
		"20260521T140000Z-",
		"20260521T140000Z-UPPER",
		"20260521T140000Z-abc/de",
	}
	for _, id := range bad {
		_, err := mgr.Start(StartOptions{
			ID:   id,
			Mode: "self_check",
			Args: []string{"--help"},
		})
		if err == nil {
			t.Errorf("Start with malformed ID %q: expected error, got nil", id)
		}
	}
}

func TestStart_RejectsCollidingCallerID(t *testing.T) {
	mgr := newTestRunManager(t)
	id := "20260521T140000Z-stage85t5"

	journalDir := filepath.Join(mgr.stateDir, "runs")
	if err := os.MkdirAll(journalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(journalDir, id+".ndjson")
	if err := os.WriteFile(journalPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := mgr.Start(StartOptions{
		ID:   id,
		Mode: "self_check",
		Args: []string{"--help"},
	})
	if err == nil {
		t.Fatal("Start with colliding ID: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error should mention collision; got %v", err)
	}
}

func TestStart_AcceptsValidCallerID(t *testing.T) {
	mgr := newTestRunManager(t)
	id := "20260521T140000Z-stage85t5b"

	r, err := mgr.Start(StartOptions{
		ID:   id,
		Mode: "self_check",
		Args: []string{"--help"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if r.ID != id {
		t.Errorf("Run.ID: got %q want %q", r.ID, id)
	}
	journalPath := filepath.Join(mgr.stateDir, "runs", id+".ndjson")
	if _, err := os.Stat(journalPath); err != nil {
		t.Errorf("journal not created at expected path %q: %v", journalPath, err)
	}
}

func TestScanCRorLFSplitsCarriageReturns(t *testing.T) {
	in := "a\rbb\rccc\nlast"
	sc := bufio.NewScanner(strings.NewReader(in))
	sc.Split(scanCRorLF)
	var got []string
	for sc.Scan() {
		got = append(got, sc.Text())
	}
	want := []string{"a", "bb", "ccc", "last"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
