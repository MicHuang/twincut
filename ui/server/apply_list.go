package server

// Package server — shared apply-list TSV construction.
//
// The TSV is the contract between the Web UI and twincut.sh's --apply-list
// short-circuit: each row tells bash exactly one file to quarantine, with
// the keep target and a reason that selects the quarantine subdir layout.
//
// Self-check rows carry reasons md5 / video_fast / video_strict.
// Cross-check rows carry reasons cross_hash / cross_video_fast /
// cross_video_strict; process_apply_list in bin/twincut.sh routes the
// cross_* family directly into $QUAR_DIR (no subdir), matching cross-check
// scan-mode behavior.

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// extractArgValue returns the value following the first occurrence of flag
// in args (e.g. extractArgValue(args, "--source") returns the next token).
// Returns "", false if flag is absent or has no following value.
func extractArgValue(args []string, flag string) (string, bool) {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			return args[i+1], true
		}
	}
	return "", false
}

// extractArgValues returns every value following each occurrence of flag.
// Used for repeated flags like --backup.
func extractArgValues(args []string, flag string) []string {
	var vs []string
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			vs = append(vs, args[i+1])
		}
	}
	return vs
}

// composeApplyList walks the preview's groups and the form's selections to
// produce the rows that twincut --apply-list will execute. Each row:
//
//	move_path \t keep_path \t group_id \t match_reason \t hash
//
// Form contract:
//   - "quarantine" values list every path the user wants moved.
//   - "keep_<group_id>" identifies the user-chosen keeper per cluster
//     (defaults to the preview's keeper when absent).
//
// Selections are validated against each cluster's known paths so a malicious
// or stale form can't cause moves outside the preview's scope.
//
// mode is "self_check" or "cross_check" and controls the reason column:
// cross_check prefixes md5→cross_hash, video_fast→cross_video_fast, etc.
func composeApplyList(groups []ResultGroup, form url.Values, mode string) [][]string {
	wanted := map[string]bool{}
	for _, p := range form["quarantine"] {
		wanted[p] = true
	}
	var rows [][]string
	for _, g := range groups {
		clusterOrder := []string{g.Keep.Path}
		clusterSet := map[string]bool{g.Keep.Path: true}
		for _, rm := range g.Remove {
			clusterOrder = append(clusterOrder, rm.Path)
			clusterSet[rm.Path] = true
		}

		chosenKeep := form.Get("keep_" + strconv.Itoa(g.GroupID))
		if !clusterSet[chosenKeep] {
			chosenKeep = g.Keep.Path
		}

		reason := mapReason(mode, g.MatchReason)

		for _, path := range clusterOrder {
			if path == chosenKeep {
				continue
			}
			if !wanted[path] {
				continue
			}
			rows = append(rows, []string{
				path,
				chosenKeep,
				strconv.Itoa(g.GroupID),
				reason,
				g.Hash,
			})
		}
	}
	return rows
}

// mapReason rewrites a group's match_reason into the per-mode reason that
// bash's process_apply_list switches on. Self-check passes match_reason
// through unchanged; cross-check prefixes with "cross_".
func mapReason(mode, matchReason string) string {
	if mode != "cross_check" {
		return matchReason
	}
	switch matchReason {
	case "md5":
		return "cross_hash"
	case "video_fast":
		return "cross_video_fast"
	case "video_strict":
		return "cross_video_strict"
	}
	return matchReason
}

// writeApplyList serializes rows to a stable TSV file under
// <stateDir>/applylists/. Returns the absolute path. Each row's columns are
// already absolute paths and short identifiers — no escaping required for
// TSV (twincut splits on TAB and tolerates anything else inside a column).
func writeApplyList(stateDir string, rows [][]string) (string, error) {
	dir := filepath.Join(stateDir, "applylists")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "apply-*.tsv")
	if err != nil {
		return "", err
	}
	defer f.Close()
	for _, row := range rows {
		if _, err := fmt.Fprintln(f, strings.Join(row, "\t")); err != nil {
			return "", err
		}
	}
	return f.Name(), nil
}

// composeThumbnailConfirmCSV walks thumbnail ResultGroups and the apply form
// to produce the six-column enhanced review CSV consumed by --thumb-confirm.
// Only checked members (form key "group:<gid>.member<i>=on") are included.
// Keeper-role members are never included regardless of form state.
//
// CSV columns: path,reason,width,height,note,decision
func composeThumbnailConfirmCSV(groups []ResultGroup, form url.Values) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)

	if err := w.Write([]string{"path", "reason", "width", "height", "note", "decision"}); err != nil {
		return nil, fmt.Errorf("write CSV header: %w", err)
	}

	for _, g := range groups {
		for i, m := range g.Members {
			if m.Role == "keeper" {
				continue
			}
			key := "group:" + g.StringGroupID + ".member" + strconv.Itoa(i)
			if form.Get(key) != "on" {
				continue
			}
			row := []string{
				m.Path,
				m.Reason,
				strconv.Itoa(m.Width),
				strconv.Itoa(m.Height),
				"",
				m.Decision,
			}
			if err := w.Write(row); err != nil {
				return nil, fmt.Errorf("write CSV row for %s: %w", m.Path, err)
			}
		}
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("flush CSV: %w", err)
	}
	return buf.Bytes(), nil
}
