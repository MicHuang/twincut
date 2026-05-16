package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// historyView is the template payload for history_list.html.
type historyView struct {
	Entries []HistoryEntry
}

func (s *Server) handleHistoryTab(w http.ResponseWriter, _ *http.Request) {
	entries, err := collectHistory(s.opts.StateDir)
	if err != nil {
		http.Error(w, "collect history: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "history_list.html", historyView{Entries: entries}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// HistoryEntry summarizes one past UI-originated self-check Apply for the
// History list. Built from the run's NDJSON header + footer events.
type HistoryEntry struct {
	RunID        string
	Timestamp    int64
	Mode         string // canonical workflow: "self_check" or "cross_check"
	Folder       string // run_start.source
	ManifestPath string // run_end.manifest_path
	MovedCount   int
	Cancelled    bool
	Status       string // "success" | "cancelled-partial" | "failed"
	Restored     bool   // <ManifestPath>.restored sidecar exists
}

// collectHistory walks <stateDir>/runs/*.ndjson and returns one entry per
// completed self-check apply that produced at least one move. Results are
// sorted by timestamp descending. Runs with no run_end, no manifest, or
// zero moves are silently dropped.
func collectHistory(stateDir string) ([]HistoryEntry, error) {
	runsDir := filepath.Join(stateDir, "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read runs dir: %w", err)
	}

	var out []HistoryEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".ndjson") {
			continue
		}
		entry, ok, err := loadHistoryEntry(filepath.Join(runsDir, e.Name()))
		if err != nil {
			// Skip unreadable / malformed runs rather than failing the whole list.
			continue
		}
		if !ok {
			continue
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp > out[j].Timestamp })
	return out, nil
}

// loadHistoryEntry reads one NDJSON file and constructs an entry if the
// run is a self-check apply with at least one move. ok=false means "skip
// this run, it doesn't belong in the History list."
func loadHistoryEntry(path string) (HistoryEntry, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return HistoryEntry{}, false, err
	}
	defer f.Close()

	var start map[string]any
	var end map[string]any

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var ev map[string]any
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		switch ev["type"] {
		case "run_start":
			if start == nil {
				start = ev
			}
		case "run_end":
			end = ev
		}
	}
	if err := sc.Err(); err != nil {
		return HistoryEntry{}, false, err
	}

	if start == nil {
		return HistoryEntry{}, false, nil
	}
	if end == nil {
		// Run was killed/crashed mid-execution. We can't surface it reliably
		// (no manifest_path, no moved count) but log so it's at least
		// discoverable in the server log.
		runID, _ := start["run_id"].(string)
		log.Printf("history: dropping run %s — no run_end event (process killed?)", runID)
		return HistoryEntry{}, false, nil
	}
	mode, _ := start["mode"].(string)
	// Only surface self-check and cross-check apply runs.
	// Bash emits mode="self_check" or "cross_check" for both preview and
	// apply; the dry_run flag discriminates. Restore runs (mode="restore")
	// are filtered too — they have nothing further to restore.
	if mode != "self_check" && mode != "cross_check" {
		return HistoryEntry{}, false, nil
	}
	if dry, _ := start["dry_run"].(bool); dry {
		return HistoryEntry{}, false, nil
	}
	moved := jsonInt(end["moved"])
	manifest, _ := end["manifest_path"].(string)
	// Runs with zero moves or no manifest have nothing to restore — skip.
	if moved == 0 || manifest == "" {
		return HistoryEntry{}, false, nil
	}

	cancelled, _ := end["cancelled"].(bool)
	status := "success"
	if cancelled {
		status = "cancelled-partial"
	} else if jsonInt(end["errors"]) > 0 {
		status = "failed"
	}

	folder, _ := start["source"].(string)
	runID, _ := start["run_id"].(string)
	ts := jsonInt64(start["ts"])

	// Check for .restored sidecar that twincut.sh writes after a restore
	// so the UI can badge already-restored runs.
	_, sidecarErr := os.Stat(manifest + ".restored")
	return HistoryEntry{
		RunID:        runID,
		Timestamp:    ts,
		Mode:         mode,
		Folder:       folder,
		ManifestPath: manifest,
		MovedCount:   moved,
		Cancelled:    cancelled,
		Status:       status,
		Restored:     sidecarErr == nil,
	}, true, nil
}

// resolveManifest returns the absolute path to the manifest of a past run,
// verifying the file still exists on disk. Errors if the run isn't found
// or the manifest has been deleted/moved.
func resolveManifest(stateDir, runID string) (string, error) {
	path := filepath.Join(stateDir, "runs", runID+".ndjson")
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("run not found: %w", err)
	}
	defer f.Close()

	var manifest string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var ev map[string]any
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		if ev["type"] == "run_end" {
			if mp, ok := ev["manifest_path"].(string); ok {
				manifest = mp
			}
		}
	}
	if manifest == "" {
		return "", fmt.Errorf("run %s has no manifest", runID)
	}
	if _, err := os.Stat(manifest); err != nil {
		return "", fmt.Errorf("manifest gone: %w", err)
	}
	realManifest, err := filepath.EvalSymlinks(manifest)
	if err != nil {
		return "", fmt.Errorf("resolve manifest symlinks: %w", err)
	}
	ok, err := IsAllowedPath(realManifest)
	if err != nil {
		return "", fmt.Errorf("validate manifest: %w", err)
	}
	if !ok {
		return "", fmt.Errorf("manifest path resolves outside allowlist: %s -> %s", manifest, realManifest)
	}
	return realManifest, nil
}

// jsonInt / jsonInt64 unbox JSON numbers (which come through as float64).
func jsonInt(v any) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 0
}

func jsonInt64(v any) int64 {
	if f, ok := v.(float64); ok {
		return int64(f)
	}
	return 0
}
