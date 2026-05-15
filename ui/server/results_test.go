package server

import (
	"path/filepath"
	"testing"
	"time"
)

// Helper: synthesize a Run from a list of canned NDJSON event lines.
func runFromEvents(t *testing.T, lines []string) *Run {
	t.Helper()
	r := &Run{
		ID:        "synthetic",
		StartedAt: time.Now(),
		status:    RunStatusSucceeded,
		done:      make(chan struct{}),
	}
	close(r.done)
	for _, line := range lines {
		ev, err := ParseEvent([]byte(line))
		if err != nil {
			t.Fatalf("parse fixture event: %v", err)
		}
		ev.Seq = len(r.events) + 1
		r.events = append(r.events, ev)
	}
	return r
}

func TestBuildResults_SelfCheckGroup(t *testing.T) {
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"x","mode":"self_check","source":"/photos"}`,
		`{"type":"dup_group","ts":2,"run_id":"x","group_id":1,"match_reason":"md5","hash":"abc",
		 "keep_path":"/photos/a.jpg","keep_size":1024,"keep_mtime":100,
		 "remove":[{"path":"/photos/b.jpg","size":1024,"mtime":200},{"path":"/photos/c.jpg","size":1024,"mtime":300}]}`,
		`{"type":"run_end","ts":3,"run_id":"x","total":3,"dupes":0,"moved":0,"cancelled":false}`,
	})
	view, err := BuildResults(r)
	if err != nil {
		t.Fatalf("BuildResults: %v", err)
	}
	if view.SourcePath != "/photos" {
		t.Errorf("SourcePath = %q, want /photos", view.SourcePath)
	}
	if view.NumGroups != 1 {
		t.Fatalf("NumGroups = %d, want 1", view.NumGroups)
	}
	g := view.Groups[0]
	if g.Keep.Path != "/photos/a.jpg" || g.Keep.Name != "a.jpg" {
		t.Errorf("Keep mismatch: %+v", g.Keep)
	}
	if len(g.Remove) != 2 {
		t.Fatalf("len(Remove) = %d, want 2", len(g.Remove))
	}
	if view.NumFiles != 2 {
		t.Errorf("NumFiles = %d, want 2", view.NumFiles)
	}
	if view.BytesReclaim != 2048 {
		t.Errorf("BytesReclaim = %d, want 2048", view.BytesReclaim)
	}
	if view.BytesHuman != "2.0 KB" {
		t.Errorf("BytesHuman = %q, want '2.0 KB'", view.BytesHuman)
	}
}

func TestBuildResults_CrossCheckShape(t *testing.T) {
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"x","mode":"cross_check","source":"/src"}`,
		`{"type":"dup_group","ts":2,"run_id":"x","group_id":1,"match_reason":"md5","hash":"def",
		 "keep_path":"/bk/a.jpg","keep_size":1024,"keep_mtime":100,
		 "remove_path":"/src/a.jpg","remove_size":1024,"remove_mtime":200}`,
		`{"type":"run_end","ts":3,"run_id":"x","total":1,"dupes":1,"moved":0,"cancelled":false}`,
	})
	view, err := BuildResults(r)
	if err != nil {
		t.Fatalf("BuildResults: %v", err)
	}
	if view.NumGroups != 1 {
		t.Fatalf("NumGroups = %d", view.NumGroups)
	}
	g := view.Groups[0]
	if len(g.Remove) != 1 {
		t.Fatalf("len(Remove) = %d, want 1", len(g.Remove))
	}
	if g.Remove[0].Path != "/src/a.jpg" {
		t.Errorf("Remove[0].Path = %q, want /src/a.jpg", g.Remove[0].Path)
	}
}

func TestBuildResults_PreservesWarningsAndError(t *testing.T) {
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"x","mode":"self_check","source":"/p"}`,
		`{"type":"warn","ts":2,"run_id":"x","code":"bad_video","path":"/p/bad.mov","detail":"ffprobe failed"}`,
		`{"type":"warn","ts":3,"run_id":"x","code":"appledouble","path":"/p/._x"}`,
		`{"type":"error","ts":4,"run_id":"x","code":"io_error","detail":"disk full"}`,
		`{"type":"run_end","ts":5,"run_id":"x","total":2,"dupes":0,"moved":0,"cancelled":false}`,
	})
	view, _ := BuildResults(r)
	if view.NumWarnings != 2 {
		t.Errorf("NumWarnings = %d, want 2", view.NumWarnings)
	}
	if !view.HasError {
		t.Error("HasError = false, want true")
	}
	if view.ErrorMessage == "" {
		t.Error("ErrorMessage is empty")
	}
}

func TestBuildResults_SimilarVideoSurfacesMetadata(t *testing.T) {
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"x","mode":"self_check","source":"/v"}`,
		`{"type":"dup_group","ts":2,"run_id":"x","group_id":1,"match_reason":"video_fast",
		 "keep_path":"/v/a.mp4","keep_size":4200000,"keep_mtime":100,
		 "keep_duration":45.5,"keep_width":1920,"keep_height":1080,"keep_fps":29.97,"keep_bitrate":5000000,
		 "remove_path":"/v/b.mp4","remove_size":3900000,"remove_mtime":200,
		 "remove_duration":45.5,"remove_width":1920,"remove_height":1080,"remove_fps":29.97,"remove_bitrate":4700000}`,
		`{"type":"run_end","ts":3,"run_id":"x","total":2,"dupes":0,"moved":0,"cancelled":false}`,
	})
	view, err := BuildResults(r)
	if err != nil {
		t.Fatalf("BuildResults: %v", err)
	}
	if view.NumGroups != 1 {
		t.Fatalf("NumGroups = %d, want 1", view.NumGroups)
	}
	g := view.Groups[0]
	if !g.IsSimilar {
		t.Error("IsSimilar = false; want true for video_fast")
	}
	if !g.Keep.HasMedia || g.Keep.DimensionsStr != "1920x1080" || g.Keep.FPSStr != "29.97 fps" || g.Keep.BitrateStr != "5.0 Mbps" || g.Keep.DurationStr != "0:46" {
		t.Errorf("Keep media metadata wrong: %+v", g.Keep)
	}
	if len(g.Remove) != 1 || g.Remove[0].DimensionsStr != "1920x1080" || g.Remove[0].BitrateStr != "4.7 Mbps" {
		t.Errorf("Remove media metadata wrong: %+v", g.Remove)
	}
}

func TestBuildResults_Md5GroupNotMarkedSimilar(t *testing.T) {
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"x","mode":"self_check","source":"/p"}`,
		`{"type":"dup_group","ts":2,"run_id":"x","group_id":1,"match_reason":"md5",
		 "keep_path":"/p/a.jpg","keep_size":1024,"keep_mtime":100,
		 "remove":[{"path":"/p/b.jpg","size":1024,"mtime":200}]}`,
		`{"type":"run_end","ts":3,"run_id":"x","total":2,"dupes":0,"moved":0,"cancelled":false}`,
	})
	view, _ := BuildResults(r)
	if view.Groups[0].IsSimilar {
		t.Error("IsSimilar = true for md5 group; want false")
	}
	if view.Groups[0].Keep.HasMedia {
		t.Error("HasMedia = true on md5 keep; expected zero metadata")
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, ""},
		{0.5, "500ms"},
		{1, "0:01"},
		{61, "1:01"},
		{3600, "1:00:00"},
		{3725, "1:02:05"},
	}
	for _, c := range cases {
		if got := formatDuration(c.in); got != c.want {
			t.Errorf("formatDuration(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatBitrate(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, ""},
		{500, "500 bps"},
		{128000, "128 kbps"},
		{5_000_000, "5.0 Mbps"},
	}
	for _, c := range cases {
		if got := formatBitrate(c.in); got != c.want {
			t.Errorf("formatBitrate(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBuildResults_RunEndPopulatesApplyFields(t *testing.T) {
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"x","mode":"self_check","source":"/p"}`,
		`{"type":"run_end","ts":2,"run_id":"x","total":3,"dupes":2,"moved":2,"deleted":0,"cancelled":false,"manifest_path":"/p/_QUARANTINE/_manifest-foo.tsv"}`,
	})
	view, _ := BuildResults(r)
	if view.MovedCount != 2 {
		t.Errorf("MovedCount = %d, want 2", view.MovedCount)
	}
	if view.ManifestPath == "" {
		t.Error("ManifestPath is empty")
	}
}

func TestHumanBytes(t *testing.T) {
	mb := int64(1024 * 1024)
	gb := mb * 1024
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{mb, "1.0 MB"},
		{mb*5/2, "2.5 MB"},   // 2.5 MB
		{gb*34/10, "3.4 GB"}, // 3.4 GB
	}
	for _, c := range cases {
		got := humanBytes(c.in)
		if got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Sanity check that filepath.Base behaves as expected on the platform.
func TestBaseName(t *testing.T) {
	if got := filepath.Base("/a/b/c.jpg"); got != "c.jpg" {
		t.Errorf("filepath.Base = %q", got)
	}
}
