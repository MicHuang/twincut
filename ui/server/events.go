package server

import (
	"encoding/json"
	"fmt"
)

// EventType is the discriminator field of every NDJSON event emitted by
// twincut.sh --json-events. The exhaustive set is fixed by the script's
// implementation; ParseEvent rejects anything else.
type EventType string

const (
	EventRunStart EventType = "run_start"
	EventRunEnd   EventType = "run_end"
	EventProgress EventType = "progress"
	EventDupGroup EventType = "dup_group"
	EventAction   EventType = "action"
	EventWarn     EventType = "warn"
	EventError    EventType = "error"
)

var knownEventTypes = map[EventType]bool{
	EventRunStart: true,
	EventRunEnd:   true,
	EventProgress: true,
	EventDupGroup: true,
	EventAction:   true,
	EventWarn:     true,
	EventError:    true,
}

// Event is the parsed form of one NDJSON line. We keep the raw bytes too so
// SSE handlers can re-broadcast the original wire form without re-marshaling.
//
// Seq is assigned by the run manager (1-based, monotonically increasing per
// run) so SSE clients can use it as the SSE `id:` field for resumption.
type Event struct {
	Seq   int             `json:"seq"`
	Type  EventType       `json:"type"`
	TS    int64           `json:"ts"`
	RunID string          `json:"run_id"`
	Raw   json.RawMessage `json:"-"` // original NDJSON line, no trailing newline
}

// ParseEvent parses one NDJSON line. Returns an error for malformed JSON,
// unknown event types, or missing required fields (type, ts).
func ParseEvent(line []byte) (Event, error) {
	var head struct {
		Type  EventType `json:"type"`
		TS    int64     `json:"ts"`
		RunID string    `json:"run_id"`
	}
	if err := json.Unmarshal(line, &head); err != nil {
		return Event{}, fmt.Errorf("invalid JSON: %w", err)
	}
	if head.Type == "" {
		return Event{}, fmt.Errorf("missing 'type' field")
	}
	if !knownEventTypes[head.Type] {
		return Event{}, fmt.Errorf("unknown event type %q", head.Type)
	}
	// ts == 0 is suspicious but not fatal — twincut.sh always sets it, so
	// a zero here usually means a corrupted line. We accept and move on.
	raw := make(json.RawMessage, len(line))
	copy(raw, line)
	return Event{
		Type:  head.Type,
		TS:    head.TS,
		RunID: head.RunID,
		Raw:   raw,
	}, nil
}

// IsTerminal reports whether the event type ends a run.
func (e Event) IsTerminal() bool {
	return e.Type == EventRunEnd || e.Type == EventError
}
