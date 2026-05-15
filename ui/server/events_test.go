package server

import (
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

func TestEvent_IsTerminal(t *testing.T) {
	cases := map[EventType]bool{
		EventRunStart: false,
		EventProgress: false,
		EventDupGroup: false,
		EventAction:   false,
		EventWarn:     false,
		EventError:    true,
		EventRunEnd:   true,
	}
	for typ, want := range cases {
		ev := Event{Type: typ}
		if got := ev.IsTerminal(); got != want {
			t.Errorf("%s: IsTerminal() = %v, want %v", typ, got, want)
		}
	}
}
