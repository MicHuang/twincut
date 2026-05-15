package server

import (
	"fmt"
	"net/http"
	"strconv"
)

// handleSSE streams the named run's event history followed by a live tail.
// Wire format is standard SSE: each event becomes
//
//	id: <seq>
//	event: <type>
//	data: <ndjson-line>
//
// Browser EventSource auto-reconnects on disconnect. On reconnect the
// browser sends Last-Event-ID; the handler reads it (or the ?since query
// param as a fallback) and replays only events the client has not seen.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run := s.runs.Get(id)
	if run == nil {
		http.Error(w, "unknown run", http.StatusNotFound)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported on this server", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // some proxies buffer SSE otherwise

	since := parseSince(r)

	subID, sub, unsub := run.Subscribe()
	_ = subID
	defer unsub()

	// Replay any events the client hasn't seen yet (history first, then live).
	// Subscribe was called BEFORE this read, so any event newly appended after
	// the snapshot will arrive on `sub`. We dedup via Seq on the live side.
	history := run.EventsSince(since)
	for _, ev := range history {
		if !writeSSE(w, ev) {
			return
		}
	}
	flusher.Flush()
	lastSent := since
	if n := len(history); n > 0 {
		lastSent = history[n-1].Seq
	}

	ctx := r.Context()
	for {
		select {
		case ev, ok := <-sub:
			if !ok {
				// Run finished; subscriber channel is closed. Drain
				// anything we may have missed via the snapshot path
				// (rare; covers a race between subscribe and run end).
				for _, ev := range run.EventsSince(lastSent) {
					if !writeSSE(w, ev) {
						return
					}
				}
				flusher.Flush()
				return
			}
			if ev.Seq <= lastSent {
				continue
			}
			if !writeSSE(w, ev) {
				return
			}
			lastSent = ev.Seq
			flusher.Flush()
		case <-ctx.Done():
			return
		}
	}
}

func parseSince(r *http.Request) int {
	// Last-Event-ID is set by EventSource on auto-reconnect.
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	if v := r.URL.Query().Get("since"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return 0
}

// writeSSE writes one SSE record for the event. Returns false if the write
// fails (typically: client disconnected) so the caller can exit.
func writeSSE(w http.ResponseWriter, ev Event) bool {
	if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.Seq, ev.Type, ev.Raw); err != nil {
		return false
	}
	return true
}
