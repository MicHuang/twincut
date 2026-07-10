package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseEvent_Valid(t *testing.T) {
	cases := []struct {
		name string
		line string
		want EventType
	}{
		{"run_start", `{"type":"run_start","ts":1700000000,"run_id":"abc","mode":"self_check","source":"/x"}`, EventRunStart},
		{"progress", `{"type":"progress","ts":1700000001,"run_id":"abc","phase":"hash","done":10,"total":100}`, EventProgress},
		{"dup_group", `{"type":"dup_group","ts":1700000002,"run_id":"abc","group_id":1,"match_reason":"md5","keep_path":"/x/a","remove":[{"path":"/x/b"}]}`, EventDupGroup},
		{"action", `{"type":"action","ts":1700000003,"run_id":"abc","kind":"move","src":"/x/a","dst":"/q/a"}`, EventAction},
		{"warn", `{"type":"warn","ts":1700000004,"run_id":"abc","code":"bad_video","path":"/x/v.mp4"}`, EventWarn},
		{"error", `{"type":"error","ts":1700000005,"run_id":"abc","code":"missing_dep","detail":"no ffmpeg"}`, EventError},
		{"run_end", `{"type":"run_end","ts":1700000006,"run_id":"abc","total":1,"dupes":0,"moved":0,"cancelled":false}`, EventRunEnd},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ev, err := ParseEvent([]byte(c.line))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ev.Type != c.want {
				t.Errorf("type = %q, want %q", ev.Type, c.want)
			}
			if ev.RunID != "abc" {
				t.Errorf("run_id = %q, want abc", ev.RunID)
			}
			if string(ev.Raw) != c.line {
				t.Errorf("raw = %q, want %q", ev.Raw, c.line)
			}
		})
	}
}

func TestParseEvent_Invalid(t *testing.T) {
	cases := []struct {
		name    string
		line    string
		errPart string
	}{
		{"malformed json", `not json`, "invalid JSON"},
		{"missing type", `{"ts":1,"run_id":"x"}`, "missing 'type'"},
		{"unknown type", `{"type":"weird","ts":1,"run_id":"x"}`, "unknown event type"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseEvent([]byte(c.line))
			if err == nil {
				t.Fatalf("want error, got nil")
			}
			if !strings.Contains(err.Error(), c.errPart) {
				t.Errorf("err = %v, want substring %q", err, c.errPart)
			}
		})
	}
}

func TestParseThumbCandidate_L2(t *testing.T) {
	line := `{"type":"thumb_candidate","ts":1700000010,"run_id":"r1","decision":"thumb_l2_exif","path":"/src/small.jpg","keeper":"/src/big.jpg","group_id":"aabbccdd","width":200,"height":150,"size_bytes":4096}`
	ev, err := ParseEvent([]byte(line))
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if ev.Type != EventThumbCandidate {
		t.Errorf("Type = %q, want %q", ev.Type, EventThumbCandidate)
	}
	var tc ThumbCandidate
	if err := UnmarshalThumbCandidate(ev, &tc); err != nil {
		t.Fatalf("UnmarshalThumbCandidate: %v", err)
	}
	if tc.Decision != "thumb_l2_exif" {
		t.Errorf("Decision = %q, want thumb_l2_exif", tc.Decision)
	}
	if tc.Path != "/src/small.jpg" {
		t.Errorf("Path = %q, want /src/small.jpg", tc.Path)
	}
	if tc.Keeper != "/src/big.jpg" {
		t.Errorf("Keeper = %q, want /src/big.jpg", tc.Keeper)
	}
	if tc.GroupID != "aabbccdd" {
		t.Errorf("GroupID = %q, want aabbccdd", tc.GroupID)
	}
	if tc.Width != 200 || tc.Height != 150 {
		t.Errorf("Width/Height = %d/%d, want 200/150", tc.Width, tc.Height)
	}
	if tc.SizeBytes != 4096 {
		t.Errorf("SizeBytes = %d, want 4096", tc.SizeBytes)
	}
}

func TestParseThumbCandidate_L3(t *testing.T) {
	line := `{"type":"thumb_candidate","ts":1700000011,"run_id":"r1","decision":"thumb_l3_embed","path":"/src/embed_small.jpg","keeper":"/src/big.jpg","group_id":"l3:deadbeef","width":160,"height":120,"size_bytes":2048}`
	ev, err := ParseEvent([]byte(line))
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	var tc ThumbCandidate
	if err := UnmarshalThumbCandidate(ev, &tc); err != nil {
		t.Fatalf("UnmarshalThumbCandidate: %v", err)
	}
	if tc.Decision != "thumb_l3_embed" {
		t.Errorf("Decision = %q, want thumb_l3_embed", tc.Decision)
	}
	if tc.GroupID != "l3:deadbeef" {
		t.Errorf("GroupID = %q, want l3:deadbeef", tc.GroupID)
	}
}

func TestParseThumbCandidate_MissingDecision(t *testing.T) {
	line := `{"type":"thumb_candidate","ts":1700000012,"run_id":"r1","path":"/src/x.jpg","keeper":"/src/big.jpg","group_id":"g1","width":100,"height":100,"size_bytes":1024}`
	ev, err := ParseEvent([]byte(line))
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	var tc ThumbCandidate
	if err := UnmarshalThumbCandidate(ev, &tc); err == nil {
		t.Fatal("expected error for missing decision field, got nil")
	} else if !strings.Contains(err.Error(), "missing decision") {
		t.Errorf("error = %v; want substring 'missing decision'", err)
	}
}

func TestParseThumbCandidate_MalformedJSON(t *testing.T) {
	// ParseEvent itself should fail before we even reach UnmarshalThumbCandidate.
	_, err := ParseEvent([]byte(`not json at all`))
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
	if !strings.Contains(err.Error(), "invalid JSON") {
		t.Errorf("error = %v; want substring 'invalid JSON'", err)
	}
}

func TestUnmarshalThumbCandidate_L1WithPhashFields(t *testing.T) {
	line := `{"type":"thumb_candidate","ts":1700000020,"run_id":"r1","decision":"thumb_l1_review","path":"/src/small.jpg","keeper":"/src/big.jpg","group_id":"l1ph:abcdef0123456789","reason":"l1_phash_match","width":200,"height":150,"size_bytes":4096,"phash_distance":3}`
	ev, err := ParseEvent([]byte(line))
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if ev.Type != EventThumbCandidate {
		t.Fatalf("Type = %q, want %q", ev.Type, EventThumbCandidate)
	}
	var tc ThumbCandidate
	if err := UnmarshalThumbCandidate(ev, &tc); err != nil {
		t.Fatalf("UnmarshalThumbCandidate: %v", err)
	}
	if tc.Decision != "thumb_l1_review" {
		t.Errorf("Decision = %q, want thumb_l1_review", tc.Decision)
	}
	if tc.Keeper != "/src/big.jpg" {
		t.Errorf("Keeper = %q, want /src/big.jpg", tc.Keeper)
	}
	if tc.GroupID != "l1ph:abcdef0123456789" {
		t.Errorf("GroupID = %q, want l1ph:abcdef0123456789", tc.GroupID)
	}
	if tc.Reason != "l1_phash_match" {
		t.Errorf("Reason = %q, want l1_phash_match", tc.Reason)
	}
	if tc.PhashDistance != 3 {
		t.Errorf("PhashDistance = %d, want 3", tc.PhashDistance)
	}
}

func TestApplyCommand_MarshalApplyMove(t *testing.T) {
	cmd := ApplyCommand{
		Type: "apply_move", Src: "/img/IMG.JPG",
		DstDir: "/img/_Q/_thumbs", Keeper: "/img/IMG.HEIC",
		Decision: "thumb_l2_exif",
	}
	got, _ := json.Marshal(cmd)
	want := `{"type":"apply_move","src":"/img/IMG.JPG","dst_dir":"/img/_Q/_thumbs","keeper":"/img/IMG.HEIC","decision":"thumb_l2_exif"}`
	if string(got) != want {
		t.Errorf("got=%s want=%s", got, want)
	}
}

func TestApplyCommand_MarshalApplySkipOmitsKeeper(t *testing.T) {
	cmd := ApplyCommand{Type: "apply_skip", Src: "/img/IMG.JPG", Decision: "keep_user_override"}
	got, _ := json.Marshal(cmd)
	want := `{"type":"apply_skip","src":"/img/IMG.JPG","decision":"keep_user_override"}`
	if string(got) != want {
		t.Errorf("got=%s want=%s", got, want)
	}
}
