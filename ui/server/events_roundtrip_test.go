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
