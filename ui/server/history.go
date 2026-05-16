package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// HistoryEntry summarizes one past UI-originated self-check Apply for the
// History list. Built from the run's NDJSON header + footer events.
type HistoryEntry struct {
	RunID        string
	Timestamp    int64
	Mode         string // run_start.mode (always "self_check_apply" in v1)
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

	if start == nil || end == nil {
		return HistoryEntry{}, false, nil
	}
	mode, _ := start["mode"].(string)
	// Only surface apply runs; preview runs have nothing to restore.
	if mode != "self_check_apply" {
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
	return manifest, nil
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
