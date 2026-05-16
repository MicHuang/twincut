package server

import (
	"net/url"
	"os"
	"reflect"
	"testing"
)

// pgroups returns synthetic preview groups for composeApplyList tests.
func pgroups() []ResultGroup {
	return []ResultGroup{
		{
			GroupID:     1,
			MatchReason: "md5",
			Hash:        "deadbeef",
			Keep:        ResultFile{Path: "/p/keep_a.jpg"},
			Remove: []ResultFile{
				{Path: "/p/dup_a1.jpg"},
				{Path: "/p/dup_a2.jpg"},
			},
		},
		{
			GroupID:     2,
			MatchReason: "video_fast",
			Keep:        ResultFile{Path: "/p/keep_v.mp4"},
			Remove: []ResultFile{
				{Path: "/p/dup_v.mp4"},
			},
		},
	}
}

func TestComposeApplyList_DefaultsKeepAllRemoveChecked(t *testing.T) {
	form := url.Values{
		"quarantine": {"/p/dup_a1.jpg", "/p/dup_a2.jpg", "/p/dup_v.mp4"},
	}
	rows := composeApplyList(pgroups(), form)
	want := [][]string{
		{"/p/dup_a1.jpg", "/p/keep_a.jpg", "1", "md5", "deadbeef"},
		{"/p/dup_a2.jpg", "/p/keep_a.jpg", "1", "md5", "deadbeef"},
		{"/p/dup_v.mp4", "/p/keep_v.mp4", "2", "video_fast", ""},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("default rows mismatch:\n got %v\nwant %v", rows, want)
	}
}

func TestComposeApplyList_SwapKeeperOnSimilarVideo(t *testing.T) {
	// User picked the original "remove" as the keeper. The original keeper
	// must now go to quarantine; the original remove path stays.
	form := url.Values{
		"keep_2":     {"/p/dup_v.mp4"},
		"quarantine": {"/p/keep_v.mp4"},
	}
	rows := composeApplyList(pgroups()[1:], form) // only the video group
	want := [][]string{
		{"/p/keep_v.mp4", "/p/dup_v.mp4", "2", "video_fast", ""},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("swap rows mismatch:\n got %v\nwant %v", rows, want)
	}
}

func TestComposeApplyList_UncheckOneOfMultipleRemoves(t *testing.T) {
	// User unchecked dup_a2 → only dup_a1 should be moved.
	form := url.Values{
		"quarantine": {"/p/dup_a1.jpg"},
	}
	rows := composeApplyList(pgroups()[:1], form)
	want := [][]string{
		{"/p/dup_a1.jpg", "/p/keep_a.jpg", "1", "md5", "deadbeef"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("partial rows mismatch:\n got %v\nwant %v", rows, want)
	}
}

func TestComposeApplyList_RejectsKeepNotInCluster(t *testing.T) {
	// keep_1 names a path that isn't in the cluster — server falls back to
	// the preview's keeper rather than trusting the form blindly.
	form := url.Values{
		"keep_1":     {"/etc/passwd"},
		"quarantine": {"/p/dup_a1.jpg"},
	}
	rows := composeApplyList(pgroups()[:1], form)
	want := [][]string{
		{"/p/dup_a1.jpg", "/p/keep_a.jpg", "1", "md5", "deadbeef"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("malicious-form rows mismatch:\n got %v\nwant %v", rows, want)
	}
}

func TestComposeApplyList_KeeperPathInQuarantineIsIgnored(t *testing.T) {
	// If the user-chosen keeper somehow appears in quarantine[], we must
	// not generate a self-move row.
	form := url.Values{
		"keep_2":     {"/p/dup_v.mp4"},
		"quarantine": {"/p/dup_v.mp4", "/p/keep_v.mp4"},
	}
	rows := composeApplyList(pgroups()[1:], form)
	want := [][]string{
		{"/p/keep_v.mp4", "/p/dup_v.mp4", "2", "video_fast", ""},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("keeper-in-quarantine rows mismatch:\n got %v\nwant %v", rows, want)
	}
}

func TestComposeApplyList_NothingCheckedYieldsEmpty(t *testing.T) {
	form := url.Values{}
	rows := composeApplyList(pgroups(), form)
	if len(rows) != 0 {
		t.Errorf("expected empty rows; got %v", rows)
	}
}

func TestResolveSimilarVideo(t *testing.T) {
	cases := []struct {
		name       string
		form       url.Values
		hasVideos  bool
		wantOn     bool
		wantSizePct string
	}{
		{
			name:       "auto + has videos defaults on with 5%",
			form:       url.Values{"include_similar_video": {"auto"}},
			hasVideos:  true,
			wantOn:     true,
			wantSizePct: "5",
		},
		{
			name:       "auto + no videos stays off",
			form:       url.Values{"include_similar_video": {"auto"}},
			hasVideos:  false,
			wantOn:     false,
			wantSizePct: "",
		},
		{
			name:       "explicit on overrides empty folder",
			form:       url.Values{"include_similar_video": {"on"}},
			hasVideos:  false,
			wantOn:     true,
			wantSizePct: "5",
		},
		{
			name:       "explicit off overrides video presence",
			form:       url.Values{"include_similar_video": {"off"}},
			hasVideos:  true,
			wantOn:     false,
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
			name:       "blank mode treated as auto (legacy form support)",
			form:       url.Values{},
			hasVideos:  true,
			wantOn:     true,
			wantSizePct: "5",
		},
		{
			name:       "legacy mode=1 treated as on",
			form:       url.Values{"include_similar_video": {"1"}},
			hasVideos:  false,
			wantOn:     true,
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

func TestWriteApplyList_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	rows := [][]string{
		{"/p/x", "/p/y", "1", "md5", "abc"},
		{"/p/v", "/p/w", "2", "video_fast", ""},
	}
	path, err := writeApplyList(dir, rows)
	if err != nil {
		t.Fatalf("writeApplyList: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "/p/x\t/p/y\t1\tmd5\tabc\n/p/v\t/p/w\t2\tvideo_fast\t\n"
	if string(body) != want {
		t.Errorf("file body mismatch:\n got %q\nwant %q", body, want)
	}
}
