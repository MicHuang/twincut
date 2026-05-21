package server

import (
	"net/url"
	"os"
	"reflect"
	"strings"
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
	rows := composeApplyList(pgroups(), form, "self_check")
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
	rows := composeApplyList(pgroups()[1:], form, "self_check") // only the video group
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
	rows := composeApplyList(pgroups()[:1], form, "self_check")
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
	rows := composeApplyList(pgroups()[:1], form, "self_check")
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
	rows := composeApplyList(pgroups()[1:], form, "self_check")
	want := [][]string{
		{"/p/keep_v.mp4", "/p/dup_v.mp4", "2", "video_fast", ""},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("keeper-in-quarantine rows mismatch:\n got %v\nwant %v", rows, want)
	}
}

func TestComposeApplyList_NothingCheckedYieldsEmpty(t *testing.T) {
	form := url.Values{}
	rows := composeApplyList(pgroups(), form, "self_check")
	if len(rows) != 0 {
		t.Errorf("expected empty rows; got %v", rows)
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

func TestComposeApplyList_CrossCheckPrefixesReason(t *testing.T) {
	groups := []ResultGroup{
		{
			GroupID:     1,
			MatchReason: "md5",
			Hash:        "deadbeef",
			Keep:        ResultFile{Path: "/bk/keep.jpg"},
			Remove:      []ResultFile{{Path: "/src/dup.jpg"}},
		},
		{
			GroupID:     2,
			MatchReason: "video_fast",
			Hash:        "",
			Keep:        ResultFile{Path: "/bk/keep.mp4"},
			Remove:      []ResultFile{{Path: "/src/dup.mp4"}},
		},
		{
			GroupID:     3,
			MatchReason: "video_strict",
			Hash:        "",
			Keep:        ResultFile{Path: "/bk/keep.mov"},
			Remove:      []ResultFile{{Path: "/src/dup.mov"}},
		},
	}
	form := url.Values{
		"quarantine": {"/src/dup.jpg", "/src/dup.mp4", "/src/dup.mov"},
	}
	got := composeApplyList(groups, form, "cross_check")
	want := [][]string{
		{"/src/dup.jpg", "/bk/keep.jpg", "1", "cross_hash", "deadbeef"},
		{"/src/dup.mp4", "/bk/keep.mp4", "2", "cross_video_fast", ""},
		{"/src/dup.mov", "/bk/keep.mov", "3", "cross_video_strict", ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("cross-check rows mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestComposeApplyList_SelfCheckLeavesReasonUntouched(t *testing.T) {
	groups := []ResultGroup{
		{
			GroupID:     1,
			MatchReason: "md5",
			Hash:        "abc",
			Keep:        ResultFile{Path: "/p/keep.jpg"},
			Remove:      []ResultFile{{Path: "/p/dup.jpg"}},
		},
	}
	form := url.Values{"quarantine": {"/p/dup.jpg"}}
	got := composeApplyList(groups, form, "self_check")
	want := [][]string{{"/p/dup.jpg", "/p/keep.jpg", "1", "md5", "abc"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("self-check rows mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestMapReason(t *testing.T) {
	cases := []struct {
		mode, in, want string
	}{
		{"self_check", "md5", "md5"},
		{"self_check", "video_fast", "video_fast"},
		{"cross_check", "md5", "cross_hash"},
		{"cross_check", "video_fast", "cross_video_fast"},
		{"cross_check", "video_strict", "cross_video_strict"},
		{"cross_check", "unknown", "unknown"},
	}
	for _, c := range cases {
		if got := mapReason(c.mode, c.in); got != c.want {
			t.Errorf("mapReason(%q, %q) = %q, want %q", c.mode, c.in, got, c.want)
		}
	}
}

// thumbnailGroups returns synthetic ResultGroups for composeThumbnailConfirmTSV tests.
func thumbnailGroups() []ResultGroup {
	return []ResultGroup{
		{
			StringGroupID: "exifsha1abc",
			Members: []ResultMember{
				{Path: "/src/big.jpg", Role: "keeper", Decision: ""},
				{Path: "/src/small1.jpg", Role: "thumbnail", Decision: "thumb_l2_exif", Width: 200, Height: 150, SizeBytes: 4096},
				{Path: "/src/small2.jpg", Role: "thumbnail", Decision: "thumb_l2_exif", Width: 100, Height: 75, SizeBytes: 2048},
			},
		},
		{
			StringGroupID: "l3:keepersha1",
			Members: []ResultMember{
				{Path: "/src/bigvid.jpg", Role: "keeper", Decision: ""},
				{Path: "/src/embed.jpg", Role: "thumbnail", Decision: "thumb_l3_embed", Width: 160, Height: 120, SizeBytes: 1024},
			},
		},
		{
			StringGroupID: "l1-suspects",
			Members: []ResultMember{
				{Path: "/src/suspect1.jpg", Role: "suspect", Decision: "thumb_confirmed", Reason: "l1_only_thumb", Width: 80, Height: 60, SizeBytes: 512},
				{Path: "/src/suspect2.jpg", Role: "suspect", Decision: "thumb_confirmed", Reason: "l1_only_maybe", Width: 90, Height: 70, SizeBytes: 640},
			},
		},
	}
}

func TestComposeThumbnailConfirmTSV_ChecksFiltered(t *testing.T) {
	form := url.Values{
		"group:exifsha1abc.member1": {"on"},
	}
	data, err := composeThumbnailConfirmTSV(thumbnailGroups(), form)
	if err != nil {
		t.Fatalf("composeThumbnailConfirmTSV: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "/src/small1.jpg") {
		t.Errorf("small1.jpg not in TSV output:\n%s", body)
	}
	if strings.Contains(body, "/src/small2.jpg") {
		t.Errorf("small2.jpg unexpectedly in TSV output:\n%s", body)
	}
	if strings.Contains(body, "/src/big.jpg") {
		t.Errorf("keeper big.jpg unexpectedly in TSV output:\n%s", body)
	}
}

func TestComposeThumbnailConfirmTSV_DecisionPropagation(t *testing.T) {
	form := url.Values{
		"group:exifsha1abc.member1":   {"on"},
		"group:l3:keepersha1.member1": {"on"},
		"group:l1-suspects.member0":   {"on"},
	}
	data, err := composeThumbnailConfirmTSV(thumbnailGroups(), form)
	if err != nil {
		t.Fatalf("composeThumbnailConfirmTSV: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "thumb_l2_exif") {
		t.Errorf("thumb_l2_exif not in TSV:\n%s", body)
	}
	if !strings.Contains(body, "thumb_l3_embed") {
		t.Errorf("thumb_l3_embed not in TSV:\n%s", body)
	}
	if !strings.Contains(body, "thumb_confirmed") {
		t.Errorf("thumb_confirmed not in TSV:\n%s", body)
	}
}

func TestComposeThumbnailConfirmTSV_AllowsCommasAndQuotesUnescaped(t *testing.T) {
	// TSV does not quote — paths with commas and double-quotes must appear verbatim.
	path := `/src/file with "quotes" and,comma.jpg`
	groups := []ResultGroup{
		{
			StringGroupID: "g1",
			Members: []ResultMember{
				{Path: path, Role: "thumbnail", Decision: "thumb_l2_exif", Width: 100, Height: 80, SizeBytes: 512},
			},
		},
	}
	form := url.Values{"group:g1.member0": {"on"}}
	data, err := composeThumbnailConfirmTSV(groups, form)
	if err != nil {
		t.Fatalf("composeThumbnailConfirmTSV: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	// lines[0] = header, lines[1] = data row
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines, got %d:\n%s", len(lines), data)
	}
	fields := strings.Split(lines[1], "\t")
	if fields[0] != path {
		t.Errorf("path field = %q, want %q (no escaping expected in TSV)", fields[0], path)
	}
	if fields[5] != "thumb_l2_exif" {
		t.Errorf("decision col = %q, want thumb_l2_exif", fields[5])
	}
}

func TestComposeThumbnailConfirmTSV_UnicodePaths(t *testing.T) {
	path := `/src/照片/小缩略图.jpg`
	groups := []ResultGroup{
		{
			StringGroupID: "g2",
			Members: []ResultMember{
				{Path: path, Role: "thumbnail", Decision: "thumb_l3_embed", Width: 80, Height: 60, SizeBytes: 256},
			},
		},
	}
	form := url.Values{"group:g2.member0": {"on"}}
	data, err := composeThumbnailConfirmTSV(groups, form)
	if err != nil {
		t.Fatalf("composeThumbnailConfirmTSV: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	found := false
	for _, line := range lines[1:] {
		fields := strings.Split(line, "\t")
		if fields[0] == path {
			found = true
		}
	}
	if !found {
		t.Errorf("unicode path not round-tripped; output:\n%s", data)
	}
}

func TestComposeThumbnailConfirmTSV_RejectsTabInPath(t *testing.T) {
	groups := []ResultGroup{
		{
			StringGroupID: "g3",
			Members: []ResultMember{
				{Path: "/src/file\twith_tab.jpg", Role: "thumbnail", Decision: "thumb_l2_exif", Width: 100, Height: 80, SizeBytes: 512},
			},
		},
	}
	form := url.Values{"group:g3.member0": {"on"}}
	_, err := composeThumbnailConfirmTSV(groups, form)
	if err == nil {
		t.Fatal("expected error for path containing tab, got nil")
	}
	if !strings.Contains(err.Error(), "forbidden character") {
		t.Errorf("error = %q, want it to contain 'forbidden character'", err.Error())
	}
}

func TestComposeThumbnailConfirmTSV_RejectsNewlineInPath(t *testing.T) {
	groups := []ResultGroup{
		{
			StringGroupID: "g4",
			Members: []ResultMember{
				{Path: "/src/file\nwith_newline.jpg", Role: "thumbnail", Decision: "thumb_l2_exif", Width: 100, Height: 80, SizeBytes: 512},
			},
		},
	}
	form := url.Values{"group:g4.member0": {"on"}}
	_, err := composeThumbnailConfirmTSV(groups, form)
	if err == nil {
		t.Fatal("expected error for path containing newline, got nil")
	}
	if !strings.Contains(err.Error(), "forbidden character") {
		t.Errorf("error = %q, want it to contain 'forbidden character'", err.Error())
	}
}
