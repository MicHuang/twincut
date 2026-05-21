package server

import (
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
