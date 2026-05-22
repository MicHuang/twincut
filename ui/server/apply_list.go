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
	"encoding/json"
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

// composeApplyCommands converts thumbnail-detect ResultGroups into an NDJSON
// byte stream consumed by twincut.sh --thumbnail-detect-apply --json-in.
// One ApplyCommand line is emitted per non-keeper member:
//   - Decision has prefix "keep_" → apply_skip (file stays in place).
//   - Otherwise → apply_move (file is moved to dstDir, keeper recorded).
//
// Role=="keeper" members are always skipped — they represent the original file
// being kept and are not acted on by the apply step.
func composeApplyCommands(groups []ResultGroup, dstDir string) []byte {
	var buf bytes.Buffer
	for _, g := range groups {
		for _, m := range g.Members {
			if m.Role == "keeper" {
				continue
			}
			var cmd ApplyCommand
			if strings.HasPrefix(m.Decision, "keep_") {
				cmd = ApplyCommand{
					Type:     "apply_skip",
					Src:      m.Path,
					Decision: m.Decision,
				}
			} else {
				cmd = ApplyCommand{
					Type:     "apply_move",
					Src:      m.Path,
					DstDir:   dstDir,
					Keeper:   m.Keeper,
					Decision: m.Decision,
				}
			}
			line, _ := json.Marshal(cmd)
			buf.Write(line)
			buf.WriteByte('\n')
		}
	}
	return buf.Bytes()
}

// composeThumbnailConfirmTSV walks thumbnail ResultGroups and the apply form
// to produce the seven-column enhanced review TSV consumed by --thumb-confirm.
// Only checked members (form key "group:<gid>.member<i>=on") are included.
// Keeper-role members are never included regardless of form state.
//
// TSV columns (tab-separated, no quoting):
//
//	path  reason  width  height  note  decision  keeper
//
// Keeper is hydrated from m.Keeper (populated from thumb_candidate events
// for L2/L3). L1 members have m.Keeper == "" (intentional — no paired keeper).
func composeThumbnailConfirmTSV(groups []ResultGroup, form url.Values) ([]byte, error) {
	var buf bytes.Buffer

	header := []string{"path", "reason", "width", "height", "note", "decision", "keeper"}
	fmt.Fprintln(&buf, strings.Join(header, "\t"))

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
				m.Keeper,
			}
			for _, field := range row {
				if strings.ContainsAny(field, "\t\n") {
					return nil, fmt.Errorf("field contains forbidden character (tab or newline): %q", field)
				}
			}
			fmt.Fprintln(&buf, strings.Join(row, "\t"))
		}
	}

	return buf.Bytes(), nil
}
