package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildResults_L1FromEvents_NoDiskRead(t *testing.T) {
	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "src")
	runID := "20260521T140000Z-stage85t2"

	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1700000000,"run_id":"` + runID + `","mode":"thumbnail_detect_preview","source":"` + srcDir + `"}`,
		`{"type":"thumb_candidate","ts":1700000001,"run_id":"` + runID + `","decision":"thumb_l1_review","path":"` + srcDir + `/orphanA.png","reason":"l1_only_suspect","width":200,"height":200,"size_bytes":1234}`,
		`{"type":"thumb_candidate","ts":1700000002,"run_id":"` + runID + `","decision":"thumb_l1_review","path":"` + srcDir + `/orphanB.png","reason":"l1_only_maybe","width":300,"height":300,"size_bytes":5678}`,
		`{"type":"run_end","ts":1700000003,"run_id":"` + runID + `","cancelled":false,"moved":0,"deleted":0,"restored":0}`,
	})
	r.Mode = "thumbnail_detect_preview"

	view, err := BuildResults(r)
	if err != nil {
		t.Fatalf("BuildResults: %v", err)
	}

	var l1 *ResultGroup
	for i := range view.Groups {
		if view.Groups[i].StringGroupID == "l1-suspects" {
			l1 = &view.Groups[i]
			break
		}
	}
	if l1 == nil {
		t.Fatalf("no l1-suspects group; groups=%+v", view.Groups)
	}
	if len(l1.Members) != 2 {
		t.Fatalf("expected 2 l1 members, got %d", len(l1.Members))
	}
	if l1.Members[0].Path != srcDir+"/orphanA.png" || l1.Members[0].Role != "suspect" {
		t.Errorf("l1 member 0 unexpected: %+v", l1.Members[0])
	}
	if l1.Members[0].Reason != "l1_only_suspect" {
		t.Errorf("l1 member 0 reason: got %q want %q", l1.Members[0].Reason, "l1_only_suspect")
	}
	if l1.Members[0].Decision != "thumb_confirmed" {
		t.Errorf("l1 member 0 decision: got %q want %q (apply TSV needs allow-listed value)", l1.Members[0].Decision, "thumb_confirmed")
	}
	if l1.Members[1].Reason != "l1_only_maybe" {
		t.Errorf("l1 member 1 reason: got %q want %q", l1.Members[1].Reason, "l1_only_maybe")
	}

	// srcDir was never created on disk — confirm BuildResults didn't try to read from it.
	if _, err := os.Stat(filepath.Join(srcDir, "_thumbnails")); err == nil {
		t.Errorf("BuildResults created/read source _thumbnails dir; should be event-only")
	}
}

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

func TestResultsTemplate_CrossCheckRendersRoleBadges(t *testing.T) {
	srv := newCrossCheckTestServer(t)
	view := ResultsView{
		RunID:    "test-x",
		ApplyURL: "/api/cross-check/apply",
		Groups: []ResultGroup{
			{
				GroupID:     1,
				MatchReason: "md5",
				Mode:        "cross_check",
				Keep:        ResultFile{Path: "/bk/a.jpg", SizeStr: "1.0 MB"},
				Remove:      []ResultFile{{Path: "/src/a.jpg", SizeStr: "1.0 MB"}},
			},
		},
		NumGroups: 1,
		NumFiles:  1,
	}
	var buf strings.Builder
	if err := srv.tmpl.ExecuteTemplate(&buf, "selfcheck_results.html", view); err != nil {
		t.Fatalf("execute: %v", err)
	}
	body := buf.String()
	for _, want := range []string{
		`hx-post="/api/cross-check/apply"`,
		`BACKUP · keep`,
		`SOURCE`,
		`/bk/a.jpg`,
		`/src/a.jpg`,
		`type="checkbox" name="quarantine"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestResultsTemplate_SelfCheckUsesSelfCheckApplyURL(t *testing.T) {
	srv := newCrossCheckTestServer(t)
	view := ResultsView{
		RunID:    "test-s",
		ApplyURL: "/api/self-check/apply",
		Groups: []ResultGroup{
			{
				GroupID:     1,
				MatchReason: "md5",
				Mode:        "self_check",
				Keep:        ResultFile{Path: "/p/a.jpg", SizeStr: "1.0 MB"},
				Remove:      []ResultFile{{Path: "/p/b.jpg", SizeStr: "1.0 MB"}},
			},
		},
		NumGroups: 1,
		NumFiles:  1,
	}
	var buf strings.Builder
	if err := srv.tmpl.ExecuteTemplate(&buf, "selfcheck_results.html", view); err != nil {
		t.Fatalf("execute: %v", err)
	}
	body := buf.String()
	if !strings.Contains(body, `hx-post="/api/self-check/apply"`) {
		t.Errorf("self-check apply URL not in body")
	}
	if strings.Contains(body, `BACKUP · keep`) || strings.Contains(body, `>SOURCE<`) {
		t.Errorf("self-check rendering leaked cross-check role badges:\n%s", body)
	}
}

func TestBuildResults_StampsGroupModeCrossCheck(t *testing.T) {
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"x","mode":"cross_check","source":"/src"}`,
		`{"type":"dup_group","ts":2,"run_id":"x","group_id":1,"match_reason":"md5","hash":"x","keep_path":"/bk/a.jpg","keep_size":100,"keep_mtime":1,"remove_path":"/src/a.jpg","remove_size":100,"remove_mtime":1}`,
		`{"type":"run_end","ts":3,"run_id":"x","total":1,"dupes":1,"moved":0,"cancelled":false}`,
	})
	// Simulate the Run.Mode being set to cross_check_preview (as StartOptions would set it)
	r.Mode = "cross_check_preview"
	view, err := BuildResults(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Groups) != 1 {
		t.Fatalf("want 1 group, got %d", len(view.Groups))
	}
	if view.Groups[0].Mode != "cross_check" {
		t.Errorf("group Mode = %q, want %q", view.Groups[0].Mode, "cross_check")
	}
	if view.ApplyURL != "/api/cross-check/apply" {
		t.Errorf("view ApplyURL = %q, want %q", view.ApplyURL, "/api/cross-check/apply")
	}
}

func TestBuildResults_StampsGroupModeSelfCheck(t *testing.T) {
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"x","mode":"self_check","source":"/p"}`,
		`{"type":"dup_group","ts":2,"run_id":"x","group_id":1,"match_reason":"md5","hash":"x","keep_path":"/p/a.jpg","keep_size":100,"keep_mtime":1,"remove":[{"path":"/p/b.jpg","size":100,"mtime":1}]}`,
		`{"type":"run_end","ts":3,"run_id":"x","total":2,"dupes":1,"moved":0,"cancelled":false}`,
	})
	// Simulate the Run.Mode being set to self_check_preview (as StartOptions would set it)
	r.Mode = "self_check_preview"
	view, err := BuildResults(r)
	if err != nil {
		t.Fatal(err)
	}
	if view.Groups[0].Mode != "self_check" {
		t.Errorf("group Mode = %q, want %q", view.Groups[0].Mode, "self_check")
	}
	if view.ApplyURL != "/api/self-check/apply" {
		t.Errorf("view ApplyURL = %q, want %q", view.ApplyURL, "/api/self-check/apply")
	}
}

func TestBuildResults_ThumbnailMode_L2Cluster(t *testing.T) {
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"x","mode":"thumbnail_detect_preview","source":"/photos"}`,
		`{"type":"thumb_candidate","ts":2,"run_id":"x","decision":"thumb_l2_exif","path":"/photos/small1.jpg","keeper":"/photos/big.jpg","group_id":"sha1abc","width":200,"height":150,"size_bytes":4096}`,
		`{"type":"thumb_candidate","ts":3,"run_id":"x","decision":"thumb_l2_exif","path":"/photos/small2.jpg","keeper":"/photos/big.jpg","group_id":"sha1abc","width":100,"height":75,"size_bytes":2048}`,
		`{"type":"run_end","ts":4,"run_id":"x","total":3,"dupes":0,"moved":0,"cancelled":false}`,
	})
	r.Mode = "thumbnail_detect_preview"
	view, err := BuildResults(r)
	if err != nil {
		t.Fatalf("BuildResults: %v", err)
	}
	if view.NumGroups != 1 {
		t.Fatalf("NumGroups = %d, want 1", view.NumGroups)
	}
	g := view.Groups[0]
	if g.StringGroupID != "sha1abc" {
		t.Errorf("StringGroupID = %q, want sha1abc", g.StringGroupID)
	}
	if len(g.Members) != 3 {
		t.Fatalf("len(Members) = %d, want 3 (1 keeper + 2 thumbnails)", len(g.Members))
	}
	var keepers, thumbs int
	for _, m := range g.Members {
		switch m.Role {
		case "keeper":
			keepers++
			if m.Path != "/photos/big.jpg" {
				t.Errorf("keeper Path = %q, want /photos/big.jpg", m.Path)
			}
		case "thumbnail":
			thumbs++
			if m.Decision != "thumb_l2_exif" {
				t.Errorf("thumbnail Decision = %q, want thumb_l2_exif", m.Decision)
			}
		}
	}
	if keepers != 1 {
		t.Errorf("keeper count = %d, want 1", keepers)
	}
	if thumbs != 2 {
		t.Errorf("thumbnail count = %d, want 2", thumbs)
	}
}

func TestBuildResults_ThumbnailMode_L3Pair(t *testing.T) {
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"x","mode":"thumbnail_detect_preview","source":"/photos"}`,
		`{"type":"thumb_candidate","ts":2,"run_id":"x","decision":"thumb_l3_embed","path":"/photos/embed_small.jpg","keeper":"/photos/big.jpg","group_id":"l3:keepersha1","width":160,"height":120,"size_bytes":1024}`,
		`{"type":"run_end","ts":3,"run_id":"x","total":2,"dupes":0,"moved":0,"cancelled":false}`,
	})
	r.Mode = "thumbnail_detect_preview"
	view, err := BuildResults(r)
	if err != nil {
		t.Fatalf("BuildResults: %v", err)
	}
	if view.NumGroups != 1 {
		t.Fatalf("NumGroups = %d, want 1", view.NumGroups)
	}
	g := view.Groups[0]
	if g.StringGroupID != "l3:keepersha1" {
		t.Errorf("StringGroupID = %q, want l3:keepersha1", g.StringGroupID)
	}
	if len(g.Members) != 2 {
		t.Fatalf("len(Members) = %d, want 2 (keeper + embed)", len(g.Members))
	}
	if g.Members[0].Role != "keeper" {
		t.Errorf("Members[0].Role = %q, want keeper", g.Members[0].Role)
	}
	if g.Members[1].Role != "thumbnail" || g.Members[1].Decision != "thumb_l3_embed" {
		t.Errorf("Members[1] = %+v, want role=thumbnail decision=thumb_l3_embed", g.Members[1])
	}
}

func TestBuildResults_ThumbnailMode_L1Group(t *testing.T) {
	tmp := t.TempDir()
	suspect1 := filepath.Join(tmp, "suspect1.jpg")
	suspect2 := filepath.Join(tmp, "suspect2.jpg")
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"x","mode":"thumbnail_detect_preview","source":"` + tmp + `"}`,
		`{"type":"thumb_candidate","ts":2,"run_id":"x","decision":"thumb_l1_review","path":"` + suspect1 + `","reason":"l1_only_thumb","width":80,"height":60,"size_bytes":1000}`,
		`{"type":"thumb_candidate","ts":3,"run_id":"x","decision":"thumb_l1_review","path":"` + suspect2 + `","reason":"l1_only_maybe","width":90,"height":70,"size_bytes":2000}`,
		`{"type":"run_end","ts":4,"run_id":"x","total":2,"dupes":0,"moved":0,"cancelled":false}`,
	})
	r.Mode = "thumbnail_detect_preview"
	view, err := BuildResults(r)
	if err != nil {
		t.Fatalf("BuildResults: %v", err)
	}
	if view.NumGroups != 1 {
		t.Fatalf("NumGroups = %d, want 1 (l1-suspects group)", view.NumGroups)
	}
	g := view.Groups[0]
	if g.StringGroupID != "l1-suspects" {
		t.Errorf("StringGroupID = %q, want l1-suspects", g.StringGroupID)
	}
	if len(g.Members) != 2 {
		t.Fatalf("len(Members) = %d, want 2", len(g.Members))
	}
	if g.Members[0].Reason != "l1_only_thumb" {
		t.Errorf("Members[0].Reason = %q, want l1_only_thumb", g.Members[0].Reason)
	}
	if g.Members[1].Reason != "l1_only_maybe" {
		t.Errorf("Members[1].Reason = %q, want l1_only_maybe", g.Members[1].Reason)
	}
	for _, m := range g.Members {
		if m.Role != "suspect" {
			t.Errorf("L1 member Role = %q, want suspect", m.Role)
		}
		if m.Decision != "thumb_confirmed" {
			t.Errorf("L1 member Decision = %q, want thumb_confirmed", m.Decision)
		}
	}
}

func TestBuildResults_ThumbnailMode_ApplyURL(t *testing.T) {
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"x","mode":"thumbnail_detect_preview","source":"/photos"}`,
		`{"type":"run_end","ts":2,"run_id":"x","total":0,"dupes":0,"moved":0,"cancelled":false}`,
	})
	r.Mode = "thumbnail_detect_preview"
	view, err := BuildResults(r)
	if err != nil {
		t.Fatalf("BuildResults: %v", err)
	}
	if view.ApplyURL != "/api/thumbnails/apply" {
		t.Errorf("ApplyURL = %q, want /api/thumbnails/apply", view.ApplyURL)
	}
}

func TestBuildResults_L1Phash_MatchedGoesToOwnGroup(t *testing.T) {
	tmp := t.TempDir()
	small := filepath.Join(tmp, "small.jpg")
	big := filepath.Join(tmp, "big.jpg")
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"x","mode":"thumbnail_detect_preview","source":"` + tmp + `"}`,
		`{"type":"thumb_candidate","ts":2,"run_id":"x","decision":"thumb_l1_review","path":"` + small + `","keeper":"` + big + `","group_id":"l1ph:deadbeefcafef00d","reason":"l1_phash_match","width":200,"height":150,"size_bytes":4096,"phash_distance":2}`,
		`{"type":"run_end","ts":3,"run_id":"x","total":1,"dupes":1,"moved":0,"cancelled":false}`,
	})
	r.Mode = "thumbnail_detect_preview"
	view, err := BuildResults(r)
	if err != nil {
		t.Fatalf("BuildResults: %v", err)
	}
	var matched *ResultGroup
	for i := range view.Groups {
		if view.Groups[i].StringGroupID == "l1ph:deadbeefcafef00d" {
			matched = &view.Groups[i]
			break
		}
	}
	if matched == nil {
		t.Fatalf("expected group l1ph:deadbeefcafef00d, groups=%+v", view.Groups)
	}
	if len(matched.Members) < 2 {
		t.Fatalf("len(Members) = %d, want >= 2 (keeper + thumb)", len(matched.Members))
	}
	var thumbMember *ResultMember
	for i := range matched.Members {
		if matched.Members[i].Path == small {
			thumbMember = &matched.Members[i]
		}
	}
	if thumbMember == nil {
		t.Fatalf("no member with path=%s", small)
	}
	if thumbMember.PhashDistance != 2 {
		t.Errorf("PhashDistance = %d, want 2", thumbMember.PhashDistance)
	}
	for _, g := range view.Groups {
		if g.StringGroupID == "l1-suspects" {
			t.Errorf("matched L1 unexpectedly created l1-suspects group: %+v", g)
		}
	}
}

func TestBuildResults_L1Phash_UnmatchedStaysInSyntheticGroup(t *testing.T) {
	tmp := t.TempDir()
	orphan := filepath.Join(tmp, "orphan.png")
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"x","mode":"thumbnail_detect_preview","source":"` + tmp + `"}`,
		`{"type":"thumb_candidate","ts":2,"run_id":"x","decision":"thumb_l1_review","path":"` + orphan + `","reason":"l1_only_thumb","width":100,"height":100,"size_bytes":2048}`,
		`{"type":"run_end","ts":3,"run_id":"x","total":1,"dupes":0,"moved":0,"cancelled":false}`,
	})
	r.Mode = "thumbnail_detect_preview"
	view, err := BuildResults(r)
	if err != nil {
		t.Fatalf("BuildResults: %v", err)
	}
	var synthetic *ResultGroup
	for i := range view.Groups {
		if view.Groups[i].StringGroupID == "l1-suspects" {
			synthetic = &view.Groups[i]
		}
	}
	if synthetic == nil {
		t.Fatalf("expected synthetic l1-suspects group; groups=%+v", view.Groups)
	}
	if len(synthetic.Members) != 1 || synthetic.Members[0].Path != orphan {
		t.Errorf("synthetic Members = %+v, want one orphan path=%s", synthetic.Members, orphan)
	}
}

func TestBuildResults_L1Phash_MultipleSuspectsShareKeeper(t *testing.T) {
	tmp := t.TempDir()
	thumb1 := filepath.Join(tmp, "thumb1.jpg")
	thumb2 := filepath.Join(tmp, "thumb2.jpg")
	big := filepath.Join(tmp, "big.jpg")
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"x","mode":"thumbnail_detect_preview","source":"` + tmp + `"}`,
		`{"type":"thumb_candidate","ts":2,"run_id":"x","decision":"thumb_l1_review","path":"` + thumb1 + `","keeper":"` + big + `","group_id":"l1ph:aaaa1111bbbb2222","reason":"l1_phash_match","width":300,"height":225,"size_bytes":3000,"phash_distance":1}`,
		`{"type":"thumb_candidate","ts":3,"run_id":"x","decision":"thumb_l1_review","path":"` + thumb2 + `","keeper":"` + big + `","group_id":"l1ph:aaaa1111bbbb2222","reason":"l1_phash_match","width":150,"height":113,"size_bytes":1500,"phash_distance":2}`,
		`{"type":"run_end","ts":4,"run_id":"x","total":2,"dupes":2,"moved":0,"cancelled":false}`,
	})
	r.Mode = "thumbnail_detect_preview"
	view, err := BuildResults(r)
	if err != nil {
		t.Fatalf("BuildResults: %v", err)
	}
	var g *ResultGroup
	for i := range view.Groups {
		if view.Groups[i].StringGroupID == "l1ph:aaaa1111bbbb2222" {
			g = &view.Groups[i]
		}
	}
	if g == nil {
		t.Fatalf("expected merged l1ph group; groups=%+v", view.Groups)
	}
	if len(g.Members) != 3 {
		t.Fatalf("len(Members) = %d, want 3 (keeper + 2 thumbs)", len(g.Members))
	}
}
