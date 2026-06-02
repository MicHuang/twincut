package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fixtureCase pairs a fixture file with its expected typed payload.
// Unknown types or unknown fields fail the test — this is the drift
// catch in either direction (bash adds field Go doesn't have, or vice versa).
type fixtureCase struct {
	file     string
	wantType EventType
	want     interface{} // expected typed payload (RunStart, ThumbCandidate, ...)
}

// roundtripFixtures lists every (lib/events.sh helper, case) pair.
// Add an entry whenever a new helper or case is introduced.
func roundtripFixtures() []fixtureCase {
	trueBool := true
	falseBool := false
	return []fixtureCase{
		{
			file:     "run_start__basic.ndjson",
			wantType: EventRunStart,
			want: RunStart{
				EventEnvelope: EventEnvelope{Type: EventRunStart, TS: 1747934400, RunID: "r_test"},
				Mode:          "thumbnail_detect_preview",
				Source:        "/img",
			},
		},
		{
			file:     "run_start__crosscheck.ndjson",
			wantType: EventRunStart,
			want: RunStart{
				EventEnvelope: EventEnvelope{Type: EventRunStart, TS: 1747934400, RunID: "r_test"},
				Mode:          "cross_check",
				Source:        "/src",
				DryRun:        true,
			},
		},
		{
			file:     "run_end__succeeded.ndjson",
			wantType: EventRunEnd,
			want: RunEnd{
				EventEnvelope: EventEnvelope{Type: EventRunEnd, TS: 1747934400, RunID: "r_test"},
				Status:        "succeeded",
				DurationMs:    1234,
				Total:         42,
				Applied:       30,
				Skipped:       12,
			},
		},
		{
			file:     "run_end__restore.ndjson",
			wantType: EventRunEnd,
			want: RunEnd{
				EventEnvelope: EventEnvelope{Type: EventRunEnd, TS: 1747934400, RunID: "r_test"},
				Status:        "succeeded",
				Restored:      5,
				Missing:       1,
			},
		},
		{
			file:     "run_end__restore_failed.ndjson",
			wantType: EventRunEnd,
			want: RunEnd{
				EventEnvelope: EventEnvelope{Type: EventRunEnd, TS: 1747934400, RunID: "r_test"},
				Status:        "failed",
				Restored:      3,
				Missing:       1,
				Errors:        2,
			},
		},
		{
			file:     "run_end__crosscheck.ndjson",
			wantType: EventRunEnd,
			want: RunEnd{
				EventEnvelope: EventEnvelope{Type: EventRunEnd, TS: 1747934400, RunID: "r_test"},
				Status:        "succeeded",
				Total:         42,
				Moved:         3,
				ManifestPath:  "/q/_manifest.tsv",
			},
		},
		{
			file:     "warn__io_error.ndjson",
			wantType: EventWarn,
			want: Warn{
				EventEnvelope: EventEnvelope{Type: EventWarn, TS: 1747934400, RunID: "r_test"},
				Code:          "io_error",
				Path:          "/img/IMG.JPG",
				Detail:        "mv failed",
			},
		},
		{
			file:     "error__usage.ndjson",
			wantType: EventError,
			want: ErrorEvent{
				EventEnvelope: EventEnvelope{Type: EventError, TS: 1747934400, RunID: "r_test"},
				Code:          "usage_error",
				Detail:        "missing --source",
			},
		},
		{
			file:     "progress__scan.ndjson",
			wantType: EventProgress,
			want: Progress{
				EventEnvelope: EventEnvelope{Type: EventProgress, TS: 1747934400, RunID: "r_test"},
				Phase:         "scan",
				Done:          10,
				Total:         100,
				CurrentPath:   "/img/IMG.JPG",
			},
		},
		{
			file:     "thumb_candidate__l2_exif.ndjson",
			wantType: EventThumbCandidate,
			want: ThumbCandidate{
				EventEnvelope: EventEnvelope{Type: EventThumbCandidate, TS: 1747934400, RunID: "r_test"},
				Decision:      "thumb_l2_exif",
				Path:          "/img/IMG_0010.JPG",
				Keeper:        "/img/IMG_0010.HEIC",
				GroupID:       "2025-04-01T12:00:00_3024x4032",
				Width:         320,
				Height:        240,
				SizeBytes:     18432,
			},
		},
		{
			file:     "thumb_candidate__l3_embed.ndjson",
			wantType: EventThumbCandidate,
			want: ThumbCandidate{
				EventEnvelope: EventEnvelope{Type: EventThumbCandidate, TS: 1747934400, RunID: "r_test"},
				Decision:      "thumb_l3_embed",
				Path:          "/img/IMG_0011.JPG",
				Keeper:        "/img/IMG_0011.HEIC",
				GroupID:       "l3:abc123",
				Width:         160,
				Height:        120,
				SizeBytes:     9216,
			},
		},
		{
			file:     "thumb_candidate__l1_phash.ndjson",
			wantType: EventThumbCandidate,
			want: ThumbCandidate{
				EventEnvelope: EventEnvelope{Type: EventThumbCandidate, TS: 1747934400, RunID: "r_test"},
				Decision:      "thumb_l1_review",
				Path:          "/img/IMG_0012.JPG",
				Keeper:        "/img/IMG_0012.HEIC",
				GroupID:       "l1ph:abcd1234deadbeef",
				Width:         320,
				Height:        240,
				SizeBytes:     18432,
				PhashDistance: 3,
				Reason:        "l1_phash_match",
			},
		},
		{
			file:     "action_move__dry.ndjson",
			wantType: EventAction,
			want: Action{
				EventEnvelope: EventEnvelope{Type: EventAction, TS: 1747934400, RunID: "r_test"},
				Kind:          "move",
				Src:           "/img/a.jpg",
				Dst:           "/img/_Q/a.jpg",
				Matched:       "/img/a.heic",
				Decision:      "thumb_l2_exif",
				DryRun:        &trueBool,
			},
		},
		{
			file:     "action_skip__hardlink.ndjson",
			wantType: EventAction,
			want: Action{
				EventEnvelope: EventEnvelope{Type: EventAction, TS: 1747934400, RunID: "r_test"},
				Kind:          "skip",
				Src:           "/img/a.jpg",
				Matched:       "/img/a.heic",
				Reason:        "hardlink",
				Decision:      "thumb_l2_exif",
			},
		},
		{
			file:     "action_delete__wet.ndjson",
			wantType: EventAction,
			want: Action{
				EventEnvelope: EventEnvelope{Type: EventAction, TS: 1747934400, RunID: "r_test"},
				Kind:          "delete",
				Src:           "/img/b.jpg",
				Matched:       "/img/b.heic",
				Decision:      "thumb_confirmed",
				DryRun:        &falseBool,
			},
		},
		{
			file:     "action_restore__ok.ndjson",
			wantType: EventAction,
			want: Action{
				EventEnvelope: EventEnvelope{Type: EventAction, TS: 1747934400, RunID: "r_test"},
				Kind:          "restore",
				Src:           "/q/a.jpg",
				Dst:           "/img/a.jpg",
				DryRun:        &falseBool,
			},
		},
		{
			file:     "dup_group__cross_md5.ndjson",
			wantType: EventDupGroup,
			want: DupGroup{
				EventEnvelope: EventEnvelope{Type: EventDupGroup, TS: 1747934400, RunID: "r_test"},
				GroupID:       1,
				MatchReason:   "md5",
				Hash:          "deadbeef",
				KeepPath:      "/bk/a.jpg",
				KeepSize:      1024,
				KeepMTime:     100,
				Remove:        []DupRemoveEntry{{Path: "/src/a.jpg", Size: 1024, MTime: 200}},
			},
		},
		{
			file:     "dup_group__self_md5_multi.ndjson",
			wantType: EventDupGroup,
			want: DupGroup{
				EventEnvelope: EventEnvelope{Type: EventDupGroup, TS: 1747934400, RunID: "r_test"},
				GroupID:       1,
				MatchReason:   "md5",
				Hash:          "cafe",
				KeepPath:      "/p/a.jpg",
				KeepSize:      2048,
				KeepMTime:     100,
				Remove: []DupRemoveEntry{
					{Path: "/p/b.jpg", Size: 2048, MTime: 200},
					{Path: "/p/c.jpg", Size: 2048, MTime: 300},
				},
			},
		},
		{
			file:     "dup_group__similar_video.ndjson",
			wantType: EventDupGroup,
			want: DupGroup{
				EventEnvelope: EventEnvelope{Type: EventDupGroup, TS: 1747934400, RunID: "r_test"},
				GroupID:       1,
				MatchReason:   "video_fast",
				KeepPath:      "/v/a.mp4",
				KeepSize:      4200000,
				KeepMTime:     100,
				KeepDuration:  45.5,
				KeepWidth:     1920,
				KeepHeight:    1080,
				KeepFPS:       29.97,
				KeepBitrate:   5000000,
				Remove: []DupRemoveEntry{{
					Path: "/v/b.mp4", Size: 3900000, MTime: 200,
					Duration: 45.5, Width: 1920, Height: 1080, FPS: 29.97, Bitrate: 4700000,
				}},
			},
		},
	}
}

func TestEventsRoundtrip(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	dir := filepath.Join(root, "tests", "fixtures", "events")

	for _, c := range roundtripFixtures() {
		c := c
		t.Run(c.file, func(t *testing.T) {
			path := filepath.Join(dir, c.file)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			scanner := bufio.NewScanner(bytes.NewReader(raw))
			scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
			lineNum := 0
			for scanner.Scan() {
				lineNum++
				line := scanner.Bytes()
				if len(bytes.TrimSpace(line)) == 0 {
					continue
				}
				env, payload, err := strictDecodeEvent(line, c.want)
				if err != nil {
					t.Fatalf("line %d: decode: %v", lineNum, err)
				}
				if env.Type != c.wantType {
					t.Fatalf("line %d: type=%q want=%q", lineNum, env.Type, c.wantType)
				}
				if !reflect.DeepEqual(payload, c.want) {
					t.Fatalf("line %d: payload mismatch:\n got = %+v\nwant = %+v", lineNum, payload, c.want)
				}
			}
			if err := scanner.Err(); err != nil {
				t.Fatalf("scan: %v", err)
			}
		})
	}
}

// repoRoot walks up from the test binary's working directory until it finds
// the twincut repo root (parent of ui/server).
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	// ui/server -> twincut
	return filepath.Clean(filepath.Join(dir, "..", "..")), nil
}

// strictDecodeEvent decodes a single NDJSON line and returns the envelope
// plus a typed payload of the same dynamic type as wantPrototype, with
// DisallowUnknownFields enabled (so bash emitting an unmodeled field is fatal).
func strictDecodeEvent(line []byte, wantPrototype interface{}) (EventEnvelope, interface{}, error) {
	var env EventEnvelope
	if err := json.Unmarshal(line, &env); err != nil {
		return env, nil, err
	}
	// Re-decode into the typed payload type with strict field policy.
	payloadType := reflect.TypeOf(wantPrototype)
	payloadPtr := reflect.New(payloadType).Interface()
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(payloadPtr); err != nil {
		return env, nil, err
	}
	// Drop the envelope-level fields we don't compare (type, ts, run_id) by
	// returning a dereferenced copy of the payload zeroed for those fields,
	// using the comparison strategy: the typed payload structs do NOT include
	// type/ts/run_id (they're only on EventEnvelope), so reflect.DeepEqual on
	// the payload struct already excludes them.
	_ = strings.TrimSpace // (silences linter; keep for future use)
	return env, reflect.ValueOf(payloadPtr).Elem().Interface(), nil
}
