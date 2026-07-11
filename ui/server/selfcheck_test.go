package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestResolveSimilarVideo(t *testing.T) {
	cases := []struct {
		name        string
		form        url.Values
		hasVideos   bool
		wantOn      bool
		wantSizePct string
	}{
		{
			name:        "auto + has videos defaults on with 5%",
			form:        url.Values{"include_similar_video": {"auto"}},
			hasVideos:   true,
			wantOn:      true,
			wantSizePct: "5",
		},
		{
			name:        "auto + no videos stays off",
			form:        url.Values{"include_similar_video": {"auto"}},
			hasVideos:   false,
			wantOn:      false,
			wantSizePct: "",
		},
		{
			name:        "explicit on overrides empty folder",
			form:        url.Values{"include_similar_video": {"on"}},
			hasVideos:   false,
			wantOn:      true,
			wantSizePct: "5",
		},
		{
			name:        "explicit off overrides video presence",
			form:        url.Values{"include_similar_video": {"off"}},
			hasVideos:   true,
			wantOn:      false,
			wantSizePct: "",
		},
		{
			name: "off suppresses user-supplied size_pct (dead flag)",
			form: url.Values{
				"include_similar_video": {"off"},
				"size_pct":              {"3"},
			},
			hasVideos:   true,
			wantOn:      false,
			wantSizePct: "",
		},
		{
			name: "user size_pct override survives auto-default",
			form: url.Values{
				"include_similar_video": {"auto"},
				"size_pct":              {"2"},
			},
			hasVideos:   true,
			wantOn:      true,
			wantSizePct: "2",
		},
		{
			name:        "blank mode treated as auto (legacy form support)",
			form:        url.Values{},
			hasVideos:   true,
			wantOn:      true,
			wantSizePct: "5",
		},
		{
			name:        "legacy mode=1 treated as on",
			form:        url.Values{"include_similar_video": {"1"}},
			hasVideos:   false,
			wantOn:      true,
			wantSizePct: "5",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotOn, gotPct := resolveSimilarVideo(tc.form, func() bool { return tc.hasVideos })
			if gotOn != tc.wantOn || gotPct != tc.wantSizePct {
				t.Errorf("resolveSimilarVideo = (%v, %q); want (%v, %q)",
					gotOn, gotPct, tc.wantOn, tc.wantSizePct)
			}
		})
	}
}

func TestHandleSelfCheckApply_RejectsWrongModePreview(t *testing.T) {
	srv := newThumbTestServer(t)
	srcPath := os.Getenv("HOME")
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"prev-wrongmode","mode":"thumbnail_detect_preview","source":"` + srcPath + `"}`,
		`{"type":"run_end","ts":2,"run_id":"prev-wrongmode","cancelled":false}`,
	})
	r.Mode = "thumbnail_detect_preview" // not a self_check_preview
	r.Args = []string{"--thumbnail-detect", "--source", srcPath, "--dry-run"}
	storeRun(srv.runs, "prev-wrongmode", r)

	form := url.Values{"preview_run_id": {"prev-wrongmode"}}
	req := httptest.NewRequest("POST", "/api/self-check/apply", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.handleSelfCheckApply(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("wrong-mode preview: got %d, want 422", w.Code)
	}
}

func TestHandleSelfCheckApply_RejectsRunningPreview(t *testing.T) {
	srv := newThumbTestServer(t)
	srcPath := os.Getenv("HOME")
	// Construct a Run directly (in-package, same idiom as thumbnail_test.go)
	// so we can pin status to running.
	r := &Run{
		ID:     "prev-running",
		Mode:   "self_check_preview",
		Args:   []string{"--self-check", srcPath, "--dry-run"},
		status: RunStatusRunning,
		done:   make(chan struct{}),
	}
	storeRun(srv.runs, "prev-running", r)

	form := url.Values{"preview_run_id": {"prev-running"}}
	req := httptest.NewRequest("POST", "/api/self-check/apply", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.handleSelfCheckApply(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("running preview: got %d, want 409", w.Code)
	}
}
