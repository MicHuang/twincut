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
	EventRunStart       EventType = "run_start"
	EventRunEnd         EventType = "run_end"
	EventProgress       EventType = "progress"
	EventDupGroup       EventType = "dup_group"
	EventAction         EventType = "action"
	EventWarn           EventType = "warn"
	EventError          EventType = "error"
	EventThumbCandidate EventType = "thumb_candidate"
)

var knownEventTypes = map[EventType]bool{
	EventRunStart:       true,
	EventRunEnd:         true,
	EventProgress:       true,
	EventDupGroup:       true,
	EventAction:         true,
	EventWarn:           true,
	EventError:          true,
	EventThumbCandidate: true,
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

// EventEnvelope holds the common fields present on every NDJSON event.
// Typed payload structs (RunStart, etc.) embed this so strict JSON decoding
// succeeds on a full event line without "unknown field" errors.
type EventEnvelope struct {
	Type  EventType `json:"type"`
	TS    int64     `json:"ts"`
	RunID string    `json:"run_id"`
}

// RunStart is the typed payload of a "run_start" event. Twincut emits exactly
// one per run, before any other event.
type RunStart struct {
	EventEnvelope
	Mode   string `json:"mode"`
	Source string `json:"source"`
	DryRun bool   `json:"dry_run,omitempty"`
}

// RunEnd is the typed payload of a "run_end" event. Twincut emits exactly
// one per run, after all other events.
type RunEnd struct {
	EventEnvelope
	Status        string `json:"status"`
	DurationMs    int64  `json:"duration_ms,omitempty"`
	Total         int64  `json:"total,omitempty"`
	Applied       int64  `json:"applied,omitempty"`
	Skipped       int64  `json:"skipped,omitempty"`
	Restored      int64  `json:"restored,omitempty"`
	Missing       int64  `json:"missing,omitempty"`
	Unrecoverable int64  `json:"unrecoverable,omitempty"`
	Errors        int64  `json:"errors,omitempty"`
	Moved         int64  `json:"moved,omitempty"`
	Deleted       int64  `json:"deleted,omitempty"`
	Cancelled     bool   `json:"cancelled,omitempty"`
	ManifestPath  string `json:"manifest_path,omitempty"`
}

// Warn is the typed payload of a "warn" event (non-fatal warning).
type Warn struct {
	EventEnvelope
	Code   string `json:"code"`
	Path   string `json:"path,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// ErrorEvent is the typed payload of an "error" event. Named ErrorEvent to
// avoid collision with Go's built-in error interface.
type ErrorEvent struct {
	EventEnvelope
	Code   string `json:"code"`
	Path   string `json:"path,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// Progress is the typed payload of a "progress" event emitted during long phases.
type Progress struct {
	EventEnvelope
	Phase       string `json:"phase"`
	Done        int64  `json:"done,omitempty"`
	Total       int64  `json:"total,omitempty"`
	CurrentPath string `json:"current_path,omitempty"`
}

// ThumbCandidate is the parsed payload of a "thumb_candidate" event emitted
// by lib/thumb.sh during --dry-run --json-events. One event per candidate file.
type ThumbCandidate struct {
	EventEnvelope
	Decision      string `json:"decision"`                 // thumb_l2_exif | thumb_l3_embed | thumb_l1_review
	Path          string `json:"path"`                     // absolute path of the candidate thumbnail
	Keeper        string `json:"keeper,omitempty"`         // absolute path of the file being kept (L2/L3 always; L1 only when pHash matched)
	GroupID       string `json:"group_id,omitempty"`       // L2: EXIF SHA1; L3: "l3:<sha1>"; L1 matched: "l1ph:<sha1>"; absent for L1 unmatched
	Width         int    `json:"width,omitempty"`
	Height        int    `json:"height,omitempty"`
	SizeBytes     int64  `json:"size_bytes,omitempty"`
	PhashDistance int    `json:"phash_distance,omitempty"` // L1 matched only: Hamming distance to keeper (0..64 for hash_size=8)
	Reason        string `json:"reason,omitempty"`         // L1 unmatched: "l1_only_thumb"|"l1_only_maybe"; L1 matched: "l1_phash_match"; empty for L2/L3
}

// Action is the typed payload of an "action" event emitted when twincut
// moves, skips, deletes, or restores a file. Kind discriminates the variant.
type Action struct {
	EventEnvelope
	Kind     string `json:"kind"`
	Src      string `json:"src"`
	Dst      string `json:"dst,omitempty"`
	Matched  string `json:"matched,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Decision string `json:"decision,omitempty"`
	DryRun   *bool  `json:"dry_run,omitempty"`
}

// DupRemoveEntry is one element of the DupGroup.Remove list.
type DupRemoveEntry struct {
	Path string `json:"path"`
}

// DupGroup is the typed payload of a "dup_group" event emitted when a pair
// of duplicates is identified during cross-check or self-check.
type DupGroup struct {
	EventEnvelope
	GroupID     int64            `json:"group_id"`
	MatchReason string           `json:"match_reason"`
	KeepPath    string           `json:"keep_path"`
	Remove      []DupRemoveEntry `json:"remove"`
}

// UnmarshalThumbCandidate decodes the raw payload of a thumb_candidate event
// into tc. Returns an error if Decision is empty (malformed event).
func UnmarshalThumbCandidate(ev Event, tc *ThumbCandidate) error {
	if err := json.Unmarshal(ev.Raw, tc); err != nil {
		return fmt.Errorf("unmarshal thumb_candidate: %w", err)
	}
	if tc.Decision == "" {
		return fmt.Errorf("thumb_candidate seq=%d: missing decision field", ev.Seq)
	}
	return nil
}

// ApplyCommand is one NDJSON line emitted by composeApplyCommands and consumed
// by twincut.sh --thumbnail-detect-apply --json-in. Each line tells bash to
// move one file to a quarantine subdir (apply_move) or leave it in place
// (apply_skip).
//
// Field declaration order matches the desired JSON output order (Go's
// encoding/json emits fields in declaration order). DstDir and Keeper use
// omitempty so apply_skip lines are compact.
type ApplyCommand struct {
	Type     string `json:"type"`
	Src      string `json:"src"`
	DstDir   string `json:"dst_dir,omitempty"`
	Keeper   string `json:"keeper,omitempty"`
	Decision string `json:"decision"`
}

// IsTerminal reports whether the event type ends a run.
func (e Event) IsTerminal() bool {
	return e.Type == EventRunEnd || e.Type == EventError
}
