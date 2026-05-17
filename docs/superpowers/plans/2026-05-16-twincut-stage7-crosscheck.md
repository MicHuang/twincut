# Stage 7 — Cross-check Web UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the `/tab/cross-check` placeholder with a working four-state flow (Form → Running → Results → Done), reusing self-check's run pipeline, with cross-check apply runs entering History → Restore. Also fix a pre-existing bug in the History filter that prevented real apply runs from showing.

**Architecture:** Mirror self-check structure — new `crosscheck.go` handlers + `crosscheck_form.html` template + HTMX add-backup-row partial; reuse generic run manager, SSE, results pipeline, history/restore. Extend `--apply-list` in `twincut.sh` to recognize cross-check reasons. Discriminate cross-check vs self-check at the template layer via a new `ResultGroup.Mode` field.

**Tech Stack:** Go (`net/http`, html/templates, htmx 2.x already in use), bash 3.2 (system bash on macOS), Python 3 for `tests/json_events/`.

**Spec:** `docs/superpowers/specs/2026-05-16-twincut-stage7-crosscheck-design.md`

---

## Pre-flight

- [ ] **Confirm clean state:** `git status` shows working tree clean on `main`.
- [ ] **Create feature branch:**

```bash
git checkout -b feature/stage-7-crosscheck
```

- [ ] **Run baseline tests** so any regression is provable:

```bash
cd ui && go test ./... && cd ..
python3 tests/json_events/run_tests.py
```

Expected: all green.

---

## File Structure

**New files:**
- `ui/server/crosscheck.go` — handlers for `/tab/cross-check`, `/api/cross-check/preview`, `/api/cross-check/apply`, `/api/cross-check/results/{id}`, `/api/cross-check/done/{id}`, `/api/cross-check/add-backup-row`.
- `ui/server/crosscheck_test.go` — unit tests for form parsing + HTTP smoke for each handler.
- `ui/server/apply_list.go` — extracted `composeApplyList` and `writeApplyList`, both gain a `mode string` parameter.
- `ui/server/apply_list_test.go` — moved unit tests from `selfcheck_test.go` + new cross-check mode tests.
- `ui/templates/crosscheck_form.html` — source picker + dynamic backup list + advanced options.
- `ui/templates/crosscheck_backup_row.html` — single-row fragment returned by the HTMX add-backup endpoint.
- `ui/templates/crosscheck_results.html` — thin wrapper that includes `selfcheck_results.html` partial logic but points the apply form at `/api/cross-check/apply`. (See Task 6 — actual implementation parameterizes the existing template.)

**Modified files:**
- `bin/twincut.sh` — extend the `case "$_reason"` arm in `process_apply_list` (line ~339) for cross-check reasons.
- `ui/server/results.go` — add `Mode string` to `ResultGroup`; stamp in `BuildResults` from `Run.Mode`; add `ApplyURL string` to `ResultsView`.
- `ui/server/results_test.go` — new test for Mode stamping.
- `ui/server/selfcheck.go` — remove `composeApplyList` / `writeApplyList` (moved to `apply_list.go`); call new versions with `mode="self_check"`; set `ApplyURL` on the results view.
- `ui/server/selfcheck_test.go` — drop the moved unit tests.
- `ui/server/history.go` — fix pre-existing bug: filter on `mode in {self_check, cross_check} && dry_run==false` (was `mode != "self_check_apply"`, which never matched real bash runs); update `HistoryEntry.Mode` doc comment.
- `ui/server/history_test.go` — update fixtures to use real bash mode values + `dry_run` field; add cross-check apply test cases.
- `ui/server/http.go` — replace placeholder route at line 69; add 5 cross-check routes.
- `ui/templates/selfcheck_results.html` — branch on `.Mode` to render cross-check role badges (`[SOURCE]` checkbox / `[BACKUP · keep]` read-only); use `.ApplyURL` for the form action.
- `ui/templates/history_list.html` — add mode badge column (`self-check` / `cross-check`).
- `tests/json_events/run_tests.py` — add cross-check `--apply-list` test case.

---

## Task 1: Bash — extend `process_apply_list` for cross-check reasons

**Files:**
- Modify: `bin/twincut.sh:339-342`
- Test: `tests/json_events/run_tests.py`

The `process_apply_list` short-circuit (lines 1003–1009) is already mode-agnostic. The only change needed is the `case "$_reason"` arm at line 339 to route cross-check rows directly into `$QUAR_DIR` rather than into self-check subdirs.

Cross-check's scan-mode path (`bin/twincut.sh:1307`) writes directly to `$QUAR_DIR` with decision `"cross_hash"`. Mirror that for apply-list rows whose reason field is `cross_hash` / `cross_video_fast` / `cross_video_strict`.

- [ ] **Step 1: Write the failing JSON-events test.**

Append to `tests/json_events/run_tests.py` (just before `if __name__ == "__main__":`):

```python
def test_cross_check_apply_list_short_circuit_routes_to_quar_root(tmp: Path) -> None:
    """Cross-check apply via --apply-list should move source files directly
    into $QUAR_DIR (matching scan-mode behavior at twincut.sh:1307), not
    into the self-check _self_dupes/ subdir."""
    src = tmp / "source"
    bk = tmp / "backup"
    quar = tmp / "myquar"
    write_file(src / "a.jpg", b"same-content")
    write_file(bk / "a.jpg", b"same-content")

    # Apply-list TSV: one row instructing the move.
    # Columns: move, keep, group_id, reason, hash
    applylist = tmp / "apply.tsv"
    applylist.write_text(
        f"{src/'a.jpg'}\t{bk/'a.jpg'}\t1\tcross_hash\tabc123\n"
    )

    events, _, ec = run_twincut([
        "--source", str(src),
        "--backup", str(bk),
        "--quarantine", str(quar),
        "--apply-list", str(applylist),
        "--assume-yes",
    ])
    assert ec == 0, f"exit code {ec}"
    validate_structure(events)

    starts = [e for e in events if e["type"] == "run_start"]
    assert len(starts) == 1
    assert starts[0]["mode"] == "cross_check"

    actions = [e for e in events if e["type"] == "action" and e.get("kind") not in ("skip", None)]
    moved_dsts = [a["dst"] for a in actions if a.get("dst")]
    assert moved_dsts, f"no move actions emitted; got {actions}"
    # The moved file must land directly in $QUAR_DIR/a.jpg, NOT in
    # $QUAR_DIR/_self_dupes/a.jpg (which is the self-check convention).
    assert (quar / "a.jpg").exists(), f"file not moved into {quar}; tree: {list(quar.rglob('*'))}"
    assert not (quar / "_self_dupes").exists(), \
        f"cross-check apply must not create _self_dupes/ subdir; tree: {list(quar.rglob('*'))}"
```

- [ ] **Step 2: Run the test to verify it fails.**

```bash
python3 tests/json_events/run_tests.py 2>&1 | tail -20
```

Expected: `AssertionError: cross-check apply must not create _self_dupes/ subdir`. The file currently lands in `<quar>/_self_dupes/a.jpg` because the case arm at line 339 defaults to `_self_dupes`.

- [ ] **Step 3: Edit `bin/twincut.sh` to add the cross-check case arm.**

Find `process_apply_list` around line 324. Locate the `case "$_reason"` block at lines 338–341:

```bash
    case "$_reason" in
      video_fast|video_strict) _sub="$_sim_dir"; _dec="apply_list_${_reason}" ;;
      *)                       _sub="$_md5_dir"; _dec="apply_list_${_reason:-md5}" ;;
    esac
```

Replace with:

```bash
    case "$_reason" in
      cross_hash|cross_video_fast|cross_video_strict)
        _sub="$QUAR_DIR"; _dec="apply_list_${_reason}" ;;
      video_fast|video_strict)
        _sub="$_sim_dir"; _dec="apply_list_${_reason}" ;;
      *)
        _sub="$_md5_dir"; _dec="apply_list_${_reason:-md5}" ;;
    esac
```

The cross-check arm uses `$QUAR_DIR` directly (no subdir), matching scan-mode behavior at line 1307.

- [ ] **Step 4: Run the new test + the existing tests.**

```bash
python3 tests/json_events/run_tests.py 2>&1 | tail -10
```

Expected: all tests pass, including the existing self-check tests (regression check — `reason=md5` / `video_*` rows must still route to `_self_dupes/` / `_similar_video_source/`).

- [ ] **Step 5: Commit.**

```bash
git add bin/twincut.sh tests/json_events/run_tests.py
git commit -m "$(cat <<'EOF'
feat(bash): route cross-check apply-list rows to $QUAR_DIR root

process_apply_list previously assumed all rows were self-check, routing
moves into _self_dupes/ or _similar_video_source/. Cross-check's scan
mode (line 1307) writes source-side dupes directly into $QUAR_DIR with
no subdir — apply-list now mirrors that for cross_hash /
cross_video_fast / cross_video_strict reasons.

Bash 3.2 compatible: just an extra case arm.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Go — extract apply-list helpers to shared file with mode param

**Files:**
- Create: `ui/server/apply_list.go`
- Create: `ui/server/apply_list_test.go`
- Modify: `ui/server/selfcheck.go` (remove `composeApplyList`/`writeApplyList`, update caller)
- Modify: `ui/server/selfcheck_test.go` (drop moved tests)

The existing `composeApplyList` writes rows like `{path, chosenKeep, groupID, MatchReason, Hash}`. For cross-check, the `MatchReason` field needs prefixing (`md5` → `cross_hash`, `video_fast` → `cross_video_fast`, `video_strict` → `cross_video_strict`). Add a `mode string` parameter that controls the rewrite.

- [ ] **Step 1: Find the current `composeApplyList` and `writeApplyList`.**

```bash
grep -n -E "^func composeApplyList|^func writeApplyList" ui/server/selfcheck.go
```

Expected output: line numbers around 198 and 240 respectively.

- [ ] **Step 2: Read the current implementations** so the move preserves behavior exactly.

```bash
sed -n '186,265p' ui/server/selfcheck.go
```

- [ ] **Step 3: Create `ui/server/apply_list.go` with the moved helpers + mode param.**

```go
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
package server

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

// composeApplyList walks the preview groups and the form's quarantine[]
// selections, returning TSV rows. mode is "self_check" or "cross_check" and
// controls the reason column's prefix.
//
// Row columns: move, keep, group_id, reason, hash.
func composeApplyList(groups []ResultGroup, form url.Values, mode string) [][]string {
	wanted := make(map[string]bool, len(form["quarantine"]))
	for _, p := range form["quarantine"] {
		wanted[p] = true
	}

	var rows [][]string
	for _, g := range groups {
		keepOverride := form.Get(fmt.Sprintf("keep_%d", g.GroupID))
		chosenKeep := g.Keep.Path
		if keepOverride != "" {
			chosenKeep = keepOverride
		}
		reason := mapReason(mode, g.MatchReason)
		for _, rm := range g.Remove {
			if !wanted[rm.Path] {
				continue
			}
			rows = append(rows, []string{
				rm.Path,
				chosenKeep,
				fmt.Sprintf("%d", g.GroupID),
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
// <stateDir>/applylists/. Returns the absolute path. Caller is responsible
// for cleanup; twincut.sh treats the file as read-only.
func writeApplyList(stateDir string, rows [][]string) (string, error) {
	dir := filepath.Join(stateDir, "applylists")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir applylists: %w", err)
	}
	f, err := os.CreateTemp(dir, "apply-*.tsv")
	if err != nil {
		return "", fmt.Errorf("create apply-list: %w", err)
	}
	defer f.Close()
	for _, row := range rows {
		line := ""
		for i, col := range row {
			if i > 0 {
				line += "\t"
			}
			line += col
		}
		line += "\n"
		if _, werr := f.WriteString(line); werr != nil {
			return "", fmt.Errorf("write apply-list row: %w", werr)
		}
	}
	return f.Name(), nil
}
```

- [ ] **Step 4: Remove the old definitions from `ui/server/selfcheck.go`.**

Delete the `composeApplyList` and `writeApplyList` function bodies (lines roughly 186–256, depending on stage 6's exact line offsets). Also delete the leading docstring comments specific to those functions. Verify with:

```bash
grep -n -E "^func composeApplyList|^func writeApplyList" ui/server/selfcheck.go
```

Expected output: empty (functions are gone).

- [ ] **Step 5: Update `handleSelfCheckApply` to pass `mode="self_check"`.**

Find the call site (around `selfcheck.go:157`):

```go
rows := composeApplyList(view.Groups, r.Form)
```

Replace with:

```go
rows := composeApplyList(view.Groups, r.Form, "self_check")
```

- [ ] **Step 6: Move existing unit tests from `selfcheck_test.go` to `apply_list_test.go`.**

```bash
grep -n -E "^func TestComposeApplyList|^func TestWriteApplyList" ui/server/selfcheck_test.go
```

Cut those test functions plus any nearby helpers (`pgroups()`, etc.) and paste into a new `ui/server/apply_list_test.go`. Update the test function signatures: each `composeApplyList(groups, form)` call now needs `composeApplyList(groups, form, "self_check")` to preserve current behavior. Run to verify:

```bash
cd ui && go test ./server/ -run TestComposeApplyList -v
```

Expected: all moved tests pass.

- [ ] **Step 7: Add new cross-check mode tests in `apply_list_test.go`.**

Append (after the moved self-check tests):

```go
func TestComposeApplyList_CrossCheckPrefixesReason(t *testing.T) {
	groups := []ResultGroup{
		{
			GroupID:     1,
			MatchReason: "md5",
			Hash:        "deadbeef",
			Keep:        ResultFile{Path: "/bk/keep.jpg"},
			Remove:      []ResultFile{{Path: "/src/dup.jpg"}},
		},
		{
			GroupID:     2,
			MatchReason: "video_fast",
			Hash:        "",
			Keep:        ResultFile{Path: "/bk/keep.mp4"},
			Remove:      []ResultFile{{Path: "/src/dup.mp4"}},
		},
		{
			GroupID:     3,
			MatchReason: "video_strict",
			Hash:        "",
			Keep:        ResultFile{Path: "/bk/keep.mov"},
			Remove:      []ResultFile{{Path: "/src/dup.mov"}},
		},
	}
	form := url.Values{
		"quarantine": {"/src/dup.jpg", "/src/dup.mp4", "/src/dup.mov"},
	}
	got := composeApplyList(groups, form, "cross_check")
	want := [][]string{
		{"/src/dup.jpg", "/bk/keep.jpg", "1", "cross_hash", "deadbeef"},
		{"/src/dup.mp4", "/bk/keep.mp4", "2", "cross_video_fast", ""},
		{"/src/dup.mov", "/bk/keep.mov", "3", "cross_video_strict", ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("cross-check rows mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestComposeApplyList_SelfCheckLeavesReasonUntouched(t *testing.T) {
	groups := []ResultGroup{
		{
			GroupID:     1,
			MatchReason: "md5",
			Hash:        "abc",
			Keep:        ResultFile{Path: "/p/keep.jpg"},
			Remove:      []ResultFile{{Path: "/p/dup.jpg"}},
		},
	}
	form := url.Values{"quarantine": {"/p/dup.jpg"}}
	got := composeApplyList(groups, form, "self_check")
	want := [][]string{{"/p/dup.jpg", "/p/keep.jpg", "1", "md5", "abc"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("self-check rows mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestMapReason(t *testing.T) {
	cases := []struct {
		mode, in, want string
	}{
		{"self_check", "md5", "md5"},
		{"self_check", "video_fast", "video_fast"},
		{"cross_check", "md5", "cross_hash"},
		{"cross_check", "video_fast", "cross_video_fast"},
		{"cross_check", "video_strict", "cross_video_strict"},
		{"cross_check", "unknown", "unknown"}, // pass through unrecognized
	}
	for _, c := range cases {
		if got := mapReason(c.mode, c.in); got != c.want {
			t.Errorf("mapReason(%q, %q) = %q, want %q", c.mode, c.in, got, c.want)
		}
	}
}
```

- [ ] **Step 8: Run all tests + verify the build.**

```bash
cd ui && go test ./... 2>&1 | tail -20
```

Expected: all pass. If `go vet` complains about unused imports in `selfcheck.go` (e.g., `fmt`, `path/filepath`, `os`), remove them.

- [ ] **Step 9: Commit.**

```bash
git add ui/server/apply_list.go ui/server/apply_list_test.go ui/server/selfcheck.go ui/server/selfcheck_test.go
git commit -m "$(cat <<'EOF'
refactor(ui): extract apply-list helpers to apply_list.go with mode param

composeApplyList and writeApplyList move out of selfcheck.go into a
shared file so cross-check can call them with mode="cross_check" to get
cross_hash / cross_video_fast / cross_video_strict reasons. Self-check
caller passes mode="self_check" — output unchanged.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Go — add `ResultGroup.Mode` + stamp it in `BuildResults`

**Files:**
- Modify: `ui/server/results.go` (add `Mode` field to `ResultGroup`, add `ApplyURL` to `ResultsView`, stamp in `BuildResults`)
- Modify: `ui/server/results_test.go` (new test for Mode stamping)

`Run.Mode` is the Go-side StartOptions.Mode label, e.g., `"self_check_preview"`, `"self_check_apply"`, `"cross_check_preview"`, `"cross_check_apply"`. Templates need a canonical workflow value (`"self_check"` or `"cross_check"`) — strip the `_preview` / `_apply` suffix.

- [ ] **Step 1: Write the failing test.**

Open `ui/server/results_test.go`. Find an existing test that builds a `Run` from events (e.g., `runFromEvents`). Append:

```go
func TestBuildResults_StampsGroupModeCrossCheck(t *testing.T) {
	r := runFromEvents("cross_check_preview",
		`{"type":"run_start","ts":1,"mode":"cross_check","source":"/src"}`,
		`{"type":"dup_group","ts":2,"group_id":1,"match_reason":"md5","hash":"x","keep_path":"/bk/a.jpg","keep_size":100,"keep_mtime":1,"remove_path":"/src/a.jpg","remove_size":100,"remove_mtime":1}`,
		`{"type":"run_end","ts":3,"cancelled":false}`,
	)
	view, err := BuildResults(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Groups) != 1 {
		t.Fatalf("want 1 group, got %d", len(view.Groups))
	}
	if view.Groups[0].Mode != "cross_check" {
		t.Errorf("group Mode = %q, want %q", view.Groups[0].Mode, "cross_check")
	}
	if view.ApplyURL != "/api/cross-check/apply" {
		t.Errorf("view ApplyURL = %q, want %q", view.ApplyURL, "/api/cross-check/apply")
	}
}

func TestBuildResults_StampsGroupModeSelfCheck(t *testing.T) {
	r := runFromEvents("self_check_preview",
		`{"type":"run_start","ts":1,"mode":"self_check","source":"/p"}`,
		`{"type":"dup_group","ts":2,"group_id":1,"match_reason":"md5","hash":"x","keep_path":"/p/a.jpg","keep_size":100,"keep_mtime":1,"remove":[{"path":"/p/b.jpg","size":100,"mtime":1}]}`,
		`{"type":"run_end","ts":3,"cancelled":false}`,
	)
	view, err := BuildResults(r)
	if err != nil {
		t.Fatal(err)
	}
	if view.Groups[0].Mode != "self_check" {
		t.Errorf("group Mode = %q, want %q", view.Groups[0].Mode, "self_check")
	}
	if view.ApplyURL != "/api/self-check/apply" {
		t.Errorf("view ApplyURL = %q, want %q", view.ApplyURL, "/api/self-check/apply")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails.**

```bash
cd ui && go test ./server/ -run TestBuildResults_StampsGroupMode -v
```

Expected: compile error — `ResultGroup` has no `Mode` field; `ResultsView` has no `ApplyURL` field.

- [ ] **Step 3: Add `Mode` to `ResultGroup`.**

Find `ResultGroup` struct in `ui/server/results.go` (around line 35):

```go
type ResultGroup struct {
	GroupID     int
	MatchReason string
	Hash        string
	Keep        ResultFile
	Remove      []ResultFile
	IsSimilar   bool
}
```

Add `Mode string`:

```go
type ResultGroup struct {
	GroupID     int
	MatchReason string
	Hash        string
	Mode        string // "self_check" | "cross_check" — set by BuildResults from Run.Mode
	Keep        ResultFile
	Remove      []ResultFile
	IsSimilar   bool
}
```

- [ ] **Step 4: Add `ApplyURL` to `ResultsView`.**

Find `ResultsView` struct in `ui/server/results.go`. Add a string field:

```go
type ResultsView struct {
	// ... existing fields ...
	ApplyURL string // "/api/self-check/apply" or "/api/cross-check/apply"
	// ... rest unchanged
}
```

- [ ] **Step 5: Stamp Mode + ApplyURL in `BuildResults`.**

At the top of `BuildResults` (around line 83), after `view := ResultsView{...}`, add:

```go
// Canonical workflow mode for templates. Strip _preview/_apply suffix
// from Run.Mode (which is "self_check_preview" / "self_check_apply" /
// "cross_check_preview" / "cross_check_apply" depending on the call site).
workflow := snap.Mode
switch {
case strings.HasPrefix(workflow, "cross_check"):
	workflow = "cross_check"
	view.ApplyURL = "/api/cross-check/apply"
case strings.HasPrefix(workflow, "self_check"):
	workflow = "self_check"
	view.ApplyURL = "/api/self-check/apply"
default:
	view.ApplyURL = "/api/self-check/apply" // safe fallback
}
```

After the event loop (before returning view), iterate groups to stamp the Mode field:

```go
for i := range view.Groups {
	view.Groups[i].Mode = workflow
}
```

Add `"strings"` to the import block if not already present.

- [ ] **Step 6: Run the tests to verify they pass.**

```bash
cd ui && go test ./server/ -run TestBuildResults -v
```

Expected: all `TestBuildResults_*` pass, including the new Mode tests + existing tests (regression — they should still pass because Mode stamping doesn't break anything for already-passing test fixtures).

- [ ] **Step 7: Run the full test suite.**

```bash
cd ui && go test ./... 2>&1 | tail -10
```

Expected: all green.

- [ ] **Step 8: Commit.**

```bash
git add ui/server/results.go ui/server/results_test.go
git commit -m "$(cat <<'EOF'
feat(ui): stamp ResultGroup.Mode + ResultsView.ApplyURL in BuildResults

Templates need a discriminator to render cross-check role badges vs.
self-check's per-row override UI. BuildResults derives a canonical
"self_check" / "cross_check" from Run.Mode (which carries the
_preview/_apply suffix from StartOptions) and stamps it on every group
plus the apply form URL on the view.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Go — fix history filter bug + extend to cross-check

**Files:**
- Modify: `ui/server/history.go` (filter relaxation + bug fix)
- Modify: `ui/server/history_test.go` (update fixtures to real bash event shapes; add cross-check cases)
- Modify: `ui/templates/history_list.html` (add mode badge column)

**Pre-existing bug:** `history.go:125` filters `mode != "self_check_apply"`. Bash actually emits `mode=self_check` for both preview and apply; apply vs preview is discriminated by `dry_run=false|true`. Real-world apply runs never appear in History because the filter rejects them. Existing unit tests pass only because their fixtures use the fake `self_check_apply` mode value. Confirmed by inspecting real journals in `~/.twincut-ui/runs/`.

This task fixes that bug **and** extends the filter to include cross-check.

- [ ] **Step 1: Write failing tests that use real bash event shapes.**

Open `ui/server/history_test.go`. Replace the existing `TestCollectHistory_FiltersAndSorts` test body (the fixture lines that say `"mode":"self_check_apply"` are the broken ones). Update the fixtures:

```go
func TestCollectHistory_FiltersAndSorts(t *testing.T) {
	state := t.TempDir()
	runs := filepath.Join(state, "runs")

	// 1. Self-check apply (dry_run=false, moved>0) — keep.
	writeNDJSON(t, filepath.Join(runs, "A.ndjson"),
		`{"type":"run_start","ts":100,"run_id":"A","mode":"self_check","source":"/p/a","dry_run":false}`,
		`{"type":"run_end","ts":110,"run_id":"A","moved":2,"manifest_path":"/p/a/_QUARANTINE/_m.tsv","cancelled":false}`,
	)
	// 2. Self-check preview (dry_run=true) — filter out, nothing to restore.
	writeNDJSON(t, filepath.Join(runs, "B.ndjson"),
		`{"type":"run_start","ts":200,"run_id":"B","mode":"self_check","source":"/p/b","dry_run":true}`,
		`{"type":"run_end","ts":201,"run_id":"B","moved":0,"manifest_path":"","cancelled":false}`,
	)
	// 3. Self-check apply but no moves — filter out (nothing to restore).
	writeNDJSON(t, filepath.Join(runs, "C.ndjson"),
		`{"type":"run_start","ts":300,"run_id":"C","mode":"self_check","source":"/p/c","dry_run":false}`,
		`{"type":"run_end","ts":301,"run_id":"C","moved":0,"manifest_path":"","cancelled":false}`,
	)
	// 4. Self-check apply, cancelled-partial (moved>0) — keep.
	writeNDJSON(t, filepath.Join(runs, "D.ndjson"),
		`{"type":"run_start","ts":400,"run_id":"D","mode":"self_check","source":"/p/d","dry_run":false}`,
		`{"type":"run_end","ts":410,"run_id":"D","moved":5,"manifest_path":"/p/d/_QUARANTINE/_m.tsv","cancelled":true}`,
	)
	// 5. Apply with no run_end (process killed) — filter out.
	writeNDJSON(t, filepath.Join(runs, "E.ndjson"),
		`{"type":"run_start","ts":500,"run_id":"E","mode":"self_check","source":"/p/e","dry_run":false}`,
	)
	// 6. Cross-check apply (dry_run=false, moved>0) — keep.
	writeNDJSON(t, filepath.Join(runs, "F.ndjson"),
		`{"type":"run_start","ts":600,"run_id":"F","mode":"cross_check","source":"/p/src","backups":["/p/bk"],"dry_run":false}`,
		`{"type":"run_end","ts":610,"run_id":"F","moved":3,"manifest_path":"/p/src/_QUARANTINE/_m.tsv","cancelled":false}`,
	)
	// 7. Cross-check preview (dry_run=true) — filter out.
	writeNDJSON(t, filepath.Join(runs, "G.ndjson"),
		`{"type":"run_start","ts":700,"run_id":"G","mode":"cross_check","source":"/p/src","backups":["/p/bk"],"dry_run":true}`,
		`{"type":"run_end","ts":701,"run_id":"G","moved":0,"manifest_path":"","cancelled":false}`,
	)
	// 8. Restore run (mode=restore) — filter out, not an apply.
	writeNDJSON(t, filepath.Join(runs, "H.ndjson"),
		`{"type":"run_start","ts":800,"run_id":"H","mode":"restore","source":"/p/a/_QUARANTINE/_m.tsv","dry_run":false}`,
		`{"type":"run_end","ts":801,"run_id":"H","restored":2,"cancelled":false}`,
	)

	got, err := collectHistory(state)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].Timestamp > got[j].Timestamp })

	// Expect: A (self_check apply), D (self_check cancelled-partial), F (cross_check apply).
	if len(got) != 3 {
		t.Fatalf("want 3 entries, got %d: %+v", len(got), got)
	}
	gotIDs := []string{got[0].RunID, got[1].RunID, got[2].RunID}
	wantIDs := []string{"F", "D", "A"} // sorted by timestamp desc
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Errorf("ordering mismatch: got %v, want %v", gotIDs, wantIDs)
	}

	// Cross-check entry F should have Mode "cross_check".
	for _, e := range got {
		if e.RunID == "F" {
			if e.Mode != "cross_check" {
				t.Errorf("entry F Mode = %q, want %q", e.Mode, "cross_check")
			}
		}
		if e.RunID == "A" || e.RunID == "D" {
			if e.Mode != "self_check" {
				t.Errorf("entry %s Mode = %q, want %q", e.RunID, e.Mode, "self_check")
			}
		}
	}
}
```

If the test file has a separate `TestCollectHistory_RestoredSidecarDetected` test, also update its `run_start` fixture from `mode:self_check_apply` to `mode:self_check,dry_run:false`.

- [ ] **Step 2: Run the test to verify it fails.**

```bash
cd ui && go test ./server/ -run TestCollectHistory -v
```

Expected: fails — `got 0 entries, want 3`. Because the current filter `mode != "self_check_apply"` rejects all the new real-shape fixtures.

- [ ] **Step 3: Fix the filter in `ui/server/history.go`.**

Locate `loadHistoryEntry` (around line 70). Find the filter block:

```go
	mode, _ := start["mode"].(string)
	// Only surface apply runs; preview runs have nothing to restore.
	if mode != "self_check_apply" {
		return HistoryEntry{}, false, nil
	}
```

Replace with:

```go
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
```

- [ ] **Step 4: Update the `HistoryEntry.Mode` doc comment.**

In `history.go` (around line 37):

```go
	Mode         string // run_start.mode (always "self_check_apply" in v1)
```

Change to:

```go
	Mode         string // canonical workflow: "self_check" or "cross_check"
```

- [ ] **Step 5: Run the test again to verify it passes.**

```bash
cd ui && go test ./server/ -run TestCollectHistory -v
```

Expected: pass — 3 entries (A, D, F) in the right order.

- [ ] **Step 6: Run the full test suite to verify no regressions.**

```bash
cd ui && go test ./... 2>&1 | tail -10
```

Expected: all green.

- [ ] **Step 7: Add mode badge column to `ui/templates/history_list.html`.**

Find the `<thead>` row (likely near the top of the table) and add a `Mode` header. Then in the row template (around lines 24–46), add a `<td>` for the mode badge:

```html
<td>
  {{if eq .Mode "cross_check"}}
    <span class="badge badge-mode-cross">cross-check</span>
  {{else}}
    <span class="badge badge-mode-self">self-check</span>
  {{end}}
</td>
```

Insert this `<td>` after the `<td>{{.MovedCount}}</td>` column and before the status `<td>`. Update the `<thead>` accordingly to add a `<th>Mode</th>` header in the same position.

If `badge-mode-cross` / `badge-mode-self` classes don't exist in the CSS, reuse `badge-info` and `badge-success` respectively (or just `badge` — the column text is enough). Check `ui/server/static/style.css` if unsure; fall back to plain `<span class="badge">` if no styles match.

- [ ] **Step 8: Manual visual check.**

```bash
cd ui && go run . &
SERVER_PID=$!
sleep 1
# Use the dev port from main.go (default :8765)
curl -s http://localhost:8765/tab/history | grep -E 'badge-mode|self-check|cross-check' | head -5
kill $SERVER_PID
```

Expected: badge markup appears in the output. (Real entries may be absent in a clean dev environment, but the template should render the column header.)

- [ ] **Step 9: Commit.**

```bash
git add ui/server/history.go ui/server/history_test.go ui/templates/history_list.html
git commit -m "$(cat <<'EOF'
fix(ui): history filter — use real bash mode + dry_run, add cross-check

Pre-existing bug: history.go filtered on mode=="self_check_apply" but
bash emits mode=="self_check" with dry_run=false; real apply runs never
appeared in History. Unit tests passed because fixtures used the fake
mode value. Confirmed by inspecting ~/.twincut-ui/runs/ journals.

Filter now accepts mode in {self_check, cross_check} && dry_run==false,
matching what bash actually emits. History list template gains a mode
badge column.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Go — `crosscheck.go` handlers + form template + HTMX add-backup-row

**Files:**
- Create: `ui/server/crosscheck.go`
- Create: `ui/server/crosscheck_test.go`
- Create: `ui/templates/crosscheck_form.html`
- Create: `ui/templates/crosscheck_backup_row.html`

The handlers mirror `selfcheck.go` shape closely. Cross-check form takes one `source` field plus repeated `backup` fields (HTML multi-value). The `+ Add backup` button is an HTMX trigger that fetches one more row's worth of HTML and swaps it into the form's backup list container.

- [ ] **Step 1: Write the failing form-parsing test.**

Create `ui/server/crosscheck_test.go`:

```go
package server

import (
	"net/url"
	"reflect"
	"testing"
)

func TestParseCrossCheckForm_Valid(t *testing.T) {
	form := url.Values{
		"source": {"/home/me/photos"},
		"backup": {"/Volumes/bk1", "/Volumes/bk2"},
	}
	src, bks, err := parseCrossCheckForm(form)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if src != "/home/me/photos" {
		t.Errorf("source = %q, want %q", src, "/home/me/photos")
	}
	if !reflect.DeepEqual(bks, []string{"/Volumes/bk1", "/Volumes/bk2"}) {
		t.Errorf("backups = %v", bks)
	}
}

func TestParseCrossCheckForm_FiltersEmptyBackups(t *testing.T) {
	form := url.Values{
		"source": {"/home/me/photos"},
		"backup": {"/Volumes/bk1", "", "  ", "/Volumes/bk2"},
	}
	_, bks, err := parseCrossCheckForm(form)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !reflect.DeepEqual(bks, []string{"/Volumes/bk1", "/Volumes/bk2"}) {
		t.Errorf("backups = %v, want [/Volumes/bk1 /Volumes/bk2]", bks)
	}
}

func TestParseCrossCheckForm_RequiresSource(t *testing.T) {
	form := url.Values{
		"source": {""},
		"backup": {"/Volumes/bk1"},
	}
	_, _, err := parseCrossCheckForm(form)
	if err == nil {
		t.Fatal("want error for empty source, got nil")
	}
}

func TestParseCrossCheckForm_RequiresAtLeastOneBackup(t *testing.T) {
	form := url.Values{
		"source": {"/home/me/photos"},
		"backup": {"", "  "},
	}
	_, _, err := parseCrossCheckForm(form)
	if err == nil {
		t.Fatal("want error for no non-empty backups, got nil")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails (function doesn't exist yet).**

```bash
cd ui && go test ./server/ -run TestParseCrossCheckForm -v
```

Expected: compile error — undefined `parseCrossCheckForm`.

- [ ] **Step 3: Create `ui/server/crosscheck.go` with the form parser stub + handlers.**

```go
// Package server — cross-check Web UI tab. Mirrors selfcheck.go shape.
//
// User flow: form (source + 1+ backups) → preview (dry-run) → results
// (asymmetric cluster cards with [SOURCE] checkboxes and [BACKUP · keep]
// read-only rows) → apply → done. Apply runs join History (stage 6) for
// later Restore via the same code path self-check apply runs use.
//
// Reuses generic infrastructure: run manager (runs.go), SSE (sse.go),
// events parser (events.go), results builder (results.go) — the only
// cross-check-specific code lives here and in the cross-check templates.
package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// parseCrossCheckForm extracts the source path and the non-empty backup
// paths from a submitted cross-check form. Whitespace-only entries are
// dropped. Returns error if source is empty or no non-empty backup
// remains; both are required.
func parseCrossCheckForm(form map[string][]string) (string, []string, error) {
	source := ""
	if v, ok := form["source"]; ok && len(v) > 0 {
		source = strings.TrimSpace(v[0])
	}
	if source == "" {
		return "", nil, errors.New("source folder is required")
	}
	var backups []string
	for _, b := range form["backup"] {
		t := strings.TrimSpace(b)
		if t == "" {
			continue
		}
		backups = append(backups, t)
	}
	if len(backups) == 0 {
		return "", nil, errors.New("at least one backup folder is required")
	}
	return source, backups, nil
}

func (s *Server) handleCrossCheckTab(w http.ResponseWriter, r *http.Request) {
	recents, err := s.recents.List()
	if err != nil {
		http.Error(w, "list recents: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.tmpl.ExecuteTemplate(w, "crosscheck_form.html", map[string]any{
		"Recents": recents,
	}); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleCrossCheckAddBackupRow(w http.ResponseWriter, r *http.Request) {
	if err := s.tmpl.ExecuteTemplate(w, "crosscheck_backup_row.html", nil); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleCrossCheckPreview(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form parse: "+err.Error(), http.StatusBadRequest)
		return
	}
	source, backups, err := parseCrossCheckForm(r.Form)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	if ok, err := IsAllowedPath(source); err != nil || !ok {
		http.Error(w, "source is outside the allowlist (must be under $HOME or /Volumes)", http.StatusForbidden)
		return
	}
	for _, b := range backups {
		if ok, err := IsAllowedPath(b); err != nil || !ok {
			http.Error(w, fmt.Sprintf("backup %q is outside the allowlist", b), http.StatusForbidden)
			return
		}
	}

	args := []string{"--source", source}
	for _, b := range backups {
		args = append(args, "--backup", b)
	}
	args = append(args, "--dry-run")
	args = appendCrossCheckOptions(args, r)

	run, err := s.runs.Start(StartOptions{Mode: "cross_check_preview", Args: args})
	if err != nil {
		http.Error(w, "start run: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_ = s.recents.Add(source)
	for _, b := range backups {
		_ = s.recents.Add(b)
	}

	if err := s.tmpl.ExecuteTemplate(w, "selfcheck_running.html", selfCheckRunningData{
		RunID:       run.ID,
		Folder:      source,
		Mode:        "cross_check_preview",
		NextURL:     "/api/cross-check/results/" + run.ID,
		ShowActions: false,
	}); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleCrossCheckApply(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form parse: "+err.Error(), http.StatusBadRequest)
		return
	}
	source, backups, err := parseCrossCheckForm(r.Form)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	if ok, err := IsAllowedPath(source); err != nil || !ok {
		http.Error(w, "source is outside the allowlist", http.StatusForbidden)
		return
	}
	for _, b := range backups {
		if ok, err := IsAllowedPath(b); err != nil || !ok {
			http.Error(w, fmt.Sprintf("backup %q is outside the allowlist", b), http.StatusForbidden)
			return
		}
	}

	previewID := r.FormValue("preview_run_id")
	if previewID == "" {
		http.Error(w, "missing preview_run_id", http.StatusBadRequest)
		return
	}
	previewRun := s.runs.Get(previewID)
	if previewRun == nil {
		http.Error(w, "preview run not found: "+previewID, http.StatusBadRequest)
		return
	}
	view, err := BuildResults(previewRun)
	if err != nil {
		http.Error(w, "build results: "+err.Error(), http.StatusInternalServerError)
		return
	}

	rows := composeApplyList(view.Groups, r.Form, "cross_check")
	listPath, err := writeApplyList(s.opts.StateDir, rows)
	if err != nil {
		http.Error(w, "write apply-list: "+err.Error(), http.StatusInternalServerError)
		return
	}

	args := []string{"--source", source}
	for _, b := range backups {
		args = append(args, "--backup", b)
	}
	args = append(args,
		"--quarantine", source+"/_QUARANTINE",
		"--assume-yes",
		"--apply-list", listPath,
	)

	run, err := s.runs.Start(StartOptions{Mode: "cross_check_apply", Args: args})
	if err != nil {
		http.Error(w, "start run: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err := s.tmpl.ExecuteTemplate(w, "selfcheck_running.html", selfCheckRunningData{
		RunID:       run.ID,
		Folder:      source,
		Mode:        "cross_check_apply",
		NextURL:     "/api/cross-check/done/" + run.ID,
		ShowActions: false,
	}); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleCrossCheckResults(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run := s.runs.Get(id)
	if run == nil {
		http.Error(w, "run not found: "+id, http.StatusNotFound)
		return
	}
	view, err := BuildResults(run)
	if err != nil {
		http.Error(w, "build results: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.tmpl.ExecuteTemplate(w, "selfcheck_results.html", view); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleCrossCheckDone(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run := s.runs.Get(id)
	if run == nil {
		http.Error(w, "run not found: "+id, http.StatusNotFound)
		return
	}
	view, err := BuildResults(run)
	if err != nil {
		http.Error(w, "build results: "+err.Error(), http.StatusInternalServerError)
		return
	}
	view.Mode = "cross_check" // Done page chooses the verb ("Quarantined N") from this.
	if err := s.tmpl.ExecuteTemplate(w, "selfcheck_done.html", view); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
	}
}

// appendCrossCheckOptions reads the form's advanced flags and appends the
// corresponding CLI args. Mirrors appendCommonOptions in selfcheck.go but
// scoped to cross-check's slightly different option set.
func appendCrossCheckOptions(args []string, r *http.Request) []string {
	switch strings.ToLower(strings.TrimSpace(r.FormValue("matching_mode"))) {
	case "exact":
		args = append(args, "--exact")
	case "strict":
		args = append(args, "--video-fast-strict")
	}
	if v := strings.TrimSpace(r.FormValue("min_size")); v != "" {
		args = append(args, "--min-size", v)
	}
	if v := strings.TrimSpace(r.FormValue("ext")); v != "" {
		args = append(args, "--ext", v)
	}
	if v := strings.TrimSpace(r.FormValue("quarantine")); v != "" {
		args = append(args, "--quarantine", v)
	}
	return args
}
```

- [ ] **Step 4: Run the form-parser tests.**

```bash
cd ui && go test ./server/ -run TestParseCrossCheckForm -v
```

Expected: 4/4 pass.

- [ ] **Step 5: Create `ui/templates/crosscheck_form.html`.**

```html
<section class="tab-section">
  <header class="tab-section-header">
    <h2>Cross-check</h2>
    <p class="subtitle">Find files in <strong>source</strong> that already exist in one or more <strong>backup</strong> folders, then move duplicates from source to quarantine.</p>
  </header>

  <form id="crosscheck-form"
        hx-post="/api/cross-check/preview"
        hx-target="#tab-content"
        hx-swap="innerHTML"
        hx-disabled-elt="find button">

    <label class="field">
      <span class="field-label">Source folder</span>
      <div class="folder-picker">
        <input type="text" name="source" id="source-input"
               placeholder="/Users/me/Pictures/2024"
               autocomplete="off" required>
      </div>
      {{if .Recents}}
      <div class="recents">
        {{range .Recents}}
          <button type="button" class="recent-chip"
                  onclick="document.getElementById('source-input').value=this.dataset.path"
                  data-path="{{.}}">{{.}}</button>
        {{end}}
      </div>
      {{end}}
    </label>

    <fieldset class="field">
      <legend class="field-label">Backup folders</legend>
      <div id="backup-list">
        <div class="backup-row">
          <input type="text" name="backup" placeholder="/Volumes/backup-1" autocomplete="off">
          <button type="button" class="btn btn-icon btn-remove-backup"
                  onclick="this.parentElement.remove()">×</button>
        </div>
      </div>
      <button type="button" class="btn btn-secondary btn-small"
              hx-get="/api/cross-check/add-backup-row"
              hx-target="#backup-list"
              hx-swap="beforeend">+ Add backup</button>
    </fieldset>

    <details class="advanced">
      <summary>Advanced options</summary>
      <div class="advanced-grid">
        <label class="field">
          <span class="field-label">Matching mode</span>
          <select name="matching_mode">
            <option value="exact" selected>Hash-only (safest)</option>
            <option value="default">Default (video-fast)</option>
            <option value="strict">Strict (video-fast-strict)</option>
          </select>
        </label>
        <label class="field">
          <span class="field-label">Minimum size</span>
          <input type="text" name="min_size" placeholder="e.g. 100k or 1M">
        </label>
        <label class="field field-wide">
          <span class="field-label">Extensions (comma-separated)</span>
          <input type="text" name="ext" placeholder="leave blank for defaults">
        </label>
        <label class="field field-wide">
          <span class="field-label">Custom quarantine dir</span>
          <input type="text" name="quarantine" placeholder="default: &lt;source&gt;/_QUARANTINE">
        </label>
      </div>
    </details>

    <div class="form-actions">
      <button type="submit" class="btn btn-primary">Preview</button>
    </div>
  </form>
</section>
```

- [ ] **Step 6: Create `ui/templates/crosscheck_backup_row.html`.**

```html
<div class="backup-row">
  <input type="text" name="backup" placeholder="/Volumes/backup-N" autocomplete="off">
  <button type="button" class="btn btn-icon btn-remove-backup"
          onclick="this.parentElement.remove()">×</button>
</div>
```

- [ ] **Step 7: Add a handler-level smoke test.**

Append to `ui/server/crosscheck_test.go`:

```go
import (
	"net/http"
	"net/http/httptest"
	// (along with existing imports)
)

func TestHandleCrossCheckTab_RendersForm(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/tab/cross-check", nil)
	rec := httptest.NewRecorder()
	srv.handleCrossCheckTab(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, fragment := range []string{
		`name="source"`,
		`name="backup"`,
		`+ Add backup`,
		`Matching mode`,
	} {
		if !strings.Contains(body, fragment) {
			t.Errorf("body missing %q; got snippet:\n%s", fragment, body[:min(500, len(body))])
		}
	}
}

func TestHandleCrossCheckPreview_RejectsEmptySource(t *testing.T) {
	srv := newTestServer(t)
	form := url.Values{
		"source": {""},
		"backup": {"/Volumes/bk1"},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/cross-check/preview",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleCrossCheckPreview(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCrossCheckPreview_RejectsNoBackups(t *testing.T) {
	srv := newTestServer(t)
	form := url.Values{
		"source": {"/Users/me/photos"},
		"backup": {""},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/cross-check/preview",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleCrossCheckPreview(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCrossCheckAddBackupRow_ReturnsRowFragment(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/cross-check/add-backup-row", nil)
	rec := httptest.NewRecorder()
	srv.handleCrossCheckAddBackupRow(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `name="backup"`) {
		t.Errorf("row fragment missing backup input; got: %s", body)
	}
}
```

If `newTestServer` doesn't exist, check `ui/server/selfcheck_test.go` or `ui/server/history_test.go` for the equivalent helper (it's a standard pattern in this codebase — a function that builds a `*Server` with temp dirs). If absent, create one in a new file `ui/server/testhelp_test.go`:

```go
package server

import (
	"html/template"
	"path/filepath"
	"testing"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	stateDir := t.TempDir()
	tmplGlob := filepath.Join("..", "templates", "*.html")
	tmpl, err := template.ParseGlob(tmplGlob)
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	rm := NewRunManager(stateDir, "/usr/bin/false") // twincut path unused in handler tests
	rc, err := NewRecents(stateDir)
	if err != nil {
		t.Fatalf("recents: %v", err)
	}
	return &Server{
		tmpl:    tmpl,
		runs:    rm,
		recents: rc,
		opts:    ServerOptions{StateDir: stateDir},
	}
}
```

Adjust signatures to match the actual `Server` struct fields and `RunManager`/`Recents` constructors found via:

```bash
grep -n "type Server struct\|NewRunManager\|NewRecents\|ServerOptions" ui/server/*.go | head -20
```

- [ ] **Step 8: Extend `selfcheck_running.html` title switch for cross-check modes.**

The handlers above pass `Mode: "cross_check_preview"` and `Mode: "cross_check_apply"`. The stage-6 running template only knows `"apply"` / `"restore"` / default. Without an extension, cross-check apply would render the "Previewing…" title.

Find `ui/templates/selfcheck_running.html` line ~4–7:

```html
{{- if eq .Mode "apply" -}}Applying…
{{- else if eq .Mode "restore" -}}Restoring…
{{- else -}}Previewing…
{{- end -}}
```

Replace with:

```html
{{- if eq .Mode "apply" -}}Applying…
{{- else if eq .Mode "cross_check_apply" -}}Applying cross-check…
{{- else if eq .Mode "cross_check_preview" -}}Cross-checking…
{{- else if eq .Mode "restore" -}}Restoring…
{{- else -}}Previewing…
{{- end -}}
```

- [ ] **Step 9: Run all crosscheck tests.**

```bash
cd ui && go test ./server/ -run CrossCheck -v
```

Expected: all green.

- [ ] **Step 10: Commit.**

```bash
git add ui/server/crosscheck.go ui/server/crosscheck_test.go ui/templates/crosscheck_form.html ui/templates/crosscheck_backup_row.html ui/templates/selfcheck_running.html
# If newTestServer was added:
git add ui/server/testhelp_test.go 2>/dev/null || true
git commit -m "$(cat <<'EOF'
feat(ui): crosscheck.go handlers + form template

Six handlers mirror selfcheck.go shape: tab/preview/apply/results/done/
add-backup-row. Form parses repeated backup[] inputs, validates source
+ ≥1 backup. HTMX add-backup-row fragment lets users grow the list
without page reload.

Extends selfcheck_running.html title switch with cross_check_preview
and cross_check_apply cases.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Template — role-aware cluster card in `selfcheck_results.html`

**Files:**
- Modify: `ui/templates/selfcheck_results.html` (Mode-aware branching + ApplyURL)

The existing template hard-codes the apply form action and treats every row as having a user-overridable keep/quarantine choice. Cross-check needs:
- The form action to come from `.ApplyURL` (so cross-check posts to `/api/cross-check/apply` and self-check still posts to `/api/self-check/apply`).
- For groups with `.Mode == "cross_check"`: `.Keep` renders as a read-only `[BACKUP · keep]` row, and each `.Remove[i]` renders as a `[SOURCE]` row with a `quarantine` checkbox (default checked = will quarantine).
- For groups with `.Mode == "self_check"` (or empty): existing rendering, unchanged.

- [ ] **Step 1: Read the current template to find the apply form + cluster card structure.**

```bash
cat ui/templates/selfcheck_results.html
```

Note the location of:
1. The `<form>` element with the apply submit (look for `action=` or `hx-post=`).
2. The `{{range $g := .Groups}}` loop and its inner cluster-card structure.

- [ ] **Step 2: Replace the apply form's hard-coded URL with `.ApplyURL`.**

Find the form (likely near the bottom or top of the template):

```html
<form hx-post="/api/self-check/apply" ...>
```

Change to:

```html
<form hx-post="{{.ApplyURL}}" ...>
```

- [ ] **Step 3: Add the cross-check branch inside the cluster card.**

Find the `{{range $g := .Groups}}` loop. Inside, before the existing keep/remove rendering, add a top-level branch on `.Mode`:

```html
{{range $g := .Groups}}
<div class="result-group {{if .IsSimilar}}result-group-similar{{end}}" data-group-id="{{.GroupID}}">
  <div class="result-group-header">
    <span class="badge badge-match">{{.MatchReason}}</span>
    {{if eq .Mode "cross_check"}}
      <span class="badge badge-role-cluster">cross-check</span>
    {{end}}
  </div>

  {{if eq .Mode "cross_check"}}
    {{/* Cross-check: asymmetric rendering. Backup keep is read-only;
         each source remove gets a quarantine checkbox. */}}
    <div class="result-row result-row-backup">
      <span class="badge badge-role-backup">BACKUP · keep</span>
      <code class="result-path">{{.Keep.Path}}</code>
      <span class="result-size">{{.Keep.SizeStr}}</span>
    </div>
    {{range .Remove}}
    <div class="result-row result-row-source">
      <label class="result-row-label">
        <input type="checkbox" name="quarantine" value="{{.Path}}" checked>
        <span class="badge badge-role-source">SOURCE</span>
        <code class="result-path">{{.Path}}</code>
        <span class="result-size">{{.SizeStr}}</span>
      </label>
    </div>
    {{end}}
  {{else}}
    {{/* Self-check: existing symmetric rendering. */}}
    <!-- KEEP THE EXISTING BLOCK HERE — DO NOT DELETE IT -->
  {{end}}
</div>
{{end}}
```

For the `<!-- KEEP THE EXISTING BLOCK HERE -->` placeholder, **paste in the original cluster-card body** that was inside the `{{range $g := .Groups}}` loop (the symmetric keep-radio / quarantine-checkbox rendering, plus the IsSimilar branch if it exists at the inner level). Do not delete any existing rendering logic — wrap it in the `{{else}}` branch.

- [ ] **Step 4: Verify templates parse.**

```bash
cd ui && go build ./... 2>&1 | head -5
```

Expected: no errors. (Template parse errors only show at request time, so we run a smoke after.)

- [ ] **Step 5: Smoke test — render results with both modes.**

Add a template-rendering test in `ui/server/results_test.go`:

```go
func TestResultsTemplate_CrossCheckRendersRoleBadges(t *testing.T) {
	srv := newTestServer(t)
	view := ResultsView{
		RunID:    "test-x",
		Mode:     "cross_check",
		ApplyURL: "/api/cross-check/apply",
		Groups: []ResultGroup{
			{
				GroupID:     1,
				MatchReason: "md5",
				Mode:        "cross_check",
				Keep:        ResultFile{Path: "/bk/a.jpg", SizeStr: "1.0 MB"},
				Remove:      []ResultFile{{Path: "/src/a.jpg", SizeStr: "1.0 MB"}},
			},
		},
	}
	var buf strings.Builder
	if err := srv.tmpl.ExecuteTemplate(&buf, "selfcheck_results.html", view); err != nil {
		t.Fatalf("execute: %v", err)
	}
	body := buf.String()
	for _, want := range []string{
		`hx-post="/api/cross-check/apply"`,
		`BACKUP · keep`,
		`SOURCE`,
		`/bk/a.jpg`,
		`/src/a.jpg`,
		`type="checkbox" name="quarantine"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestResultsTemplate_SelfCheckUsesSelfCheckApplyURL(t *testing.T) {
	srv := newTestServer(t)
	view := ResultsView{
		RunID:    "test-s",
		Mode:     "self_check",
		ApplyURL: "/api/self-check/apply",
		Groups: []ResultGroup{
			{
				GroupID:     1,
				MatchReason: "md5",
				Mode:        "self_check",
				Keep:        ResultFile{Path: "/p/a.jpg", SizeStr: "1.0 MB"},
				Remove:      []ResultFile{{Path: "/p/b.jpg", SizeStr: "1.0 MB"}},
			},
		},
	}
	var buf strings.Builder
	if err := srv.tmpl.ExecuteTemplate(&buf, "selfcheck_results.html", view); err != nil {
		t.Fatalf("execute: %v", err)
	}
	body := buf.String()
	if !strings.Contains(body, `hx-post="/api/self-check/apply"`) {
		t.Errorf("self-check apply URL not in body")
	}
	if strings.Contains(body, `BACKUP · keep`) || strings.Contains(body, `>SOURCE<`) {
		t.Errorf("self-check rendering leaked cross-check role badges:\n%s", body)
	}
}
```

- [ ] **Step 6: Run the template tests.**

```bash
cd ui && go test ./server/ -run TestResultsTemplate -v
```

Expected: 2/2 pass. If the cross-check test fails because `BuildResults` doesn't stamp `view.Mode` (only `ResultGroup.Mode`), update `BuildResults` to also set `view.Mode = workflow` near the ApplyURL assignment. The template can use either; this test happens to set both explicitly.

- [ ] **Step 7: Commit.**

```bash
git add ui/templates/selfcheck_results.html ui/server/results_test.go
git commit -m "$(cat <<'EOF'
feat(ui): role-aware cluster cards in results template

Templates now branch on ResultGroup.Mode: cross-check groups render
[BACKUP · keep] read-only for .Keep and [SOURCE] checkbox rows for each
.Remove. Self-check rendering unchanged.

Apply form action is parameterized via .ApplyURL so self-check posts to
/api/self-check/apply and cross-check posts to /api/cross-check/apply
from the same template.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: Wire all cross-check routes in `http.go`

**Files:**
- Modify: `ui/server/http.go:69` (replace placeholder + add 5 new routes)

- [ ] **Step 1: Read the current route block for context.**

```bash
sed -n '60,85p' ui/server/http.go
```

Confirm the placeholder is still at line 69 and identify the cleanest insertion point for the cross-check route block (parallel to the self-check block).

- [ ] **Step 2: Replace the placeholder and add new routes.**

Find and replace:

```go
mux.HandleFunc("GET /tab/cross-check", s.handleTabPlaceholder("Cross-check"))
```

With:

```go
mux.HandleFunc("GET /tab/cross-check", s.handleCrossCheckTab)
```

Then, immediately after the self-check workflow block (after the line registering `GET /api/self-check/done/{id}`), add:

```go
// Cross-check workflow endpoints.
mux.HandleFunc("POST /api/cross-check/preview", s.handleCrossCheckPreview)
mux.HandleFunc("GET /api/cross-check/results/{id}", s.handleCrossCheckResults)
mux.HandleFunc("POST /api/cross-check/apply", s.handleCrossCheckApply)
mux.HandleFunc("GET /api/cross-check/done/{id}", s.handleCrossCheckDone)
mux.HandleFunc("GET /api/cross-check/add-backup-row", s.handleCrossCheckAddBackupRow)
```

- [ ] **Step 3: Build to confirm everything compiles.**

```bash
cd ui && go build ./...
```

Expected: no errors.

- [ ] **Step 4: Manual smoke — boot server and hit the tab.**

```bash
cd ui && go run . &
SERVER_PID=$!
sleep 1
curl -s http://localhost:8765/tab/cross-check | head -30
kill $SERVER_PID
```

Expected: HTML output containing `<input type="text" name="source"` and `+ Add backup` button — confirms the form renders. If you see "Cross-check (placeholder)" text, the route swap didn't take.

- [ ] **Step 5: Run the full test suite.**

```bash
cd ui && go test ./... 2>&1 | tail -10
```

Expected: all green.

- [ ] **Step 6: Commit.**

```bash
git add ui/server/http.go
git commit -m "$(cat <<'EOF'
feat(ui): wire cross-check routes — replace tab placeholder

Removes the cross-check placeholder route and connects the 5
crosscheck.go handlers + the add-backup-row HTMX endpoint.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: End-to-end smoke + Gemini review + PR

**Files:**
- Verification only; no code changes unless review surfaces issues.

- [ ] **Step 1: Set up a scratch test directory** (must be inside the allowlist — under `$HOME` or `/Volumes`).

```bash
SMOKE_DIR="$HOME/twincut-stage7-smoke"
rm -rf "$SMOKE_DIR"
mkdir -p "$SMOKE_DIR/src" "$SMOKE_DIR/bk1" "$SMOKE_DIR/bk2"
# Source has 3 files
echo "alpha-content" > "$SMOKE_DIR/src/a.jpg"
echo "beta-content"  > "$SMOKE_DIR/src/b.jpg"
echo "gamma-only-in-src" > "$SMOKE_DIR/src/c.jpg"
# bk1 has alpha + beta (both source duplicates)
echo "alpha-content" > "$SMOKE_DIR/bk1/a.jpg"
echo "beta-content"  > "$SMOKE_DIR/bk1/b.jpg"
# bk2 has only beta (so b.jpg should match in 2 backups)
echo "beta-content"  > "$SMOKE_DIR/bk2/b.jpg"
ls -la "$SMOKE_DIR/src" "$SMOKE_DIR/bk1" "$SMOKE_DIR/bk2"
```

- [ ] **Step 2: Start the dev server.**

```bash
cd ui && go run . &
SERVER_PID=$!
sleep 1
```

- [ ] **Step 3: Submit a cross-check preview via curl.**

```bash
PREVIEW_RESP=$(curl -s -X POST http://localhost:8765/api/cross-check/preview \
  -d "source=$SMOKE_DIR/src" \
  -d "backup=$SMOKE_DIR/bk1" \
  -d "backup=$SMOKE_DIR/bk2" \
  -d "matching_mode=exact")
echo "$PREVIEW_RESP" | grep -E 'run_id|data-run-id' | head -5
```

Extract the `run_id` value (look for `data-run-id="..."` in the running template).

- [ ] **Step 4: Wait for the preview to finish, then fetch results.**

```bash
RUN_ID="<paste run_id from step 3>"
# Wait up to 10s
for i in 1 2 3 4 5; do
  sleep 2
  curl -s "http://localhost:8765/api/cross-check/results/$RUN_ID" | grep -q "BACKUP · keep" && break
done
RESULTS=$(curl -s "http://localhost:8765/api/cross-check/results/$RUN_ID")
echo "$RESULTS" | grep -E 'BACKUP|SOURCE|/src/' | head -20
```

Expected: 2 cross-check clusters (a.jpg matches bk1, b.jpg matches bk1 + bk2). c.jpg has no match and doesn't appear.

- [ ] **Step 5: Submit apply, leaving everything checked.**

```bash
APPLY_RESP=$(curl -s -X POST http://localhost:8765/api/cross-check/apply \
  -d "source=$SMOKE_DIR/src" \
  -d "backup=$SMOKE_DIR/bk1" \
  -d "backup=$SMOKE_DIR/bk2" \
  -d "preview_run_id=$RUN_ID" \
  -d "quarantine=$SMOKE_DIR/src/a.jpg" \
  -d "quarantine=$SMOKE_DIR/src/b.jpg")
echo "$APPLY_RESP" | grep -E 'data-run-id|run_id' | head -2
```

Extract the apply run_id.

- [ ] **Step 6: Wait for apply, then verify the moves.**

```bash
APPLY_ID="<paste apply run_id>"
sleep 3
ls -la "$SMOKE_DIR/src/"          # should NOT contain a.jpg or b.jpg (only c.jpg)
ls -la "$SMOKE_DIR/src/_QUARANTINE/" 2>/dev/null   # should contain a.jpg + b.jpg directly (NOT under _self_dupes/)
```

Expected: `$SMOKE_DIR/src/` has only `c.jpg` left; `$SMOKE_DIR/src/_QUARANTINE/a.jpg` and `_QUARANTINE/b.jpg` exist directly.

- [ ] **Step 7: Verify History shows the cross-check apply.**

```bash
HIST=$(curl -s http://localhost:8765/tab/history)
echo "$HIST" | grep -E 'cross-check|self-check|history-row' | head -10
```

Expected: at least one row with the `cross-check` badge.

- [ ] **Step 8: Restore via the History flow.**

Find the apply run's row and follow the Restore link. For a quick test, hit the History preview directly:

```bash
PREVIEW_HIST=$(curl -s "http://localhost:8765/history/$APPLY_ID/preview")
echo "$PREVIEW_HIST" | grep -E 'WillRestore|restore-confirm|Confirm' | head -5
# Apply the restore:
curl -s -X POST "http://localhost:8765/history/$APPLY_ID/apply" -d "" | grep -E 'data-run-id|Restored' | head -3
sleep 2
ls "$SMOKE_DIR/src/"   # should once again contain a.jpg, b.jpg, c.jpg
```

Expected: all three source files present.

- [ ] **Step 9: Shut down the server and clean up.**

```bash
kill $SERVER_PID
rm -rf "$SMOKE_DIR"
```

- [ ] **Step 10: Run the full test suite one more time.**

```bash
cd ui && go test ./... 2>&1 | tail -10
python3 tests/json_events/run_tests.py 2>&1 | tail -5
```

Expected: all green.

- [ ] **Step 11: Dispatch the Gemini reviewer.**

Per the `~/.claude/CLAUDE.md` rule, run third-party review before opening the PR. Dispatch `reviewer-gemini` with:
- Scope: the diff from `main` (`git diff main..HEAD`)
- Focus: bash 3.2 compat (per `project_twincut_bash_compat.md`), path safety on the new cross-check handlers (`IsAllowedPath` is applied to source + all backups; double-check no path is shell-interpolated), template injection (the form template uses Go's html/template auto-escaping — verify), filter logic correctness in `history.go` (the bug fix), apply-list format (the new `cross_*` reasons).

If Gemini returns `BLOCKED: Gemini unavailable`, surface to the user before continuing.

- [ ] **Step 12: Address all BLOCKER/MAJOR findings** with new commits on the same branch. Re-run tests after each fix.

- [ ] **Step 13: Push the branch and open the PR.**

```bash
git push -u origin feature/stage-7-crosscheck
gh pr create --title "feat(stage 7): cross-check Web UI tab" --body "$(cat <<'EOF'
## Summary
- New `/tab/cross-check` working end-to-end: form with multi-backup picker → preview → asymmetric cluster results (`[SOURCE]` checkboxes / `[BACKUP · keep]` read-only) → apply → History/Restore.
- Extends `bin/twincut.sh` `process_apply_list` to recognize cross-check reasons (`cross_hash` / `cross_video_*`) — routes to `$QUAR_DIR/` directly (no subdir), matching scan-mode behavior.
- Fixes pre-existing bug in `history.go`: filter was `mode != "self_check_apply"` but bash emits `mode == "self_check"` with `dry_run==false`. Real apply runs never appeared in History; tests passed only because fixtures used a fake mode value.
- Adds `ResultGroup.Mode` + `ResultsView.ApplyURL` so the existing results template can branch on workflow type.
- All 7 implementation tasks complete with passing tests.

## Test plan
- [x] All Go unit tests pass (`cd ui && go test ./...`)
- [x] JSON-events tests pass (`python3 tests/json_events/run_tests.py`)
- [x] E2E smoke (curl): cross-check preview → apply (2 backups, mixed coverage) → History row appears with `cross-check` badge → Restore loops files back
- [x] Self-check regression — existing self-check flow unchanged
- [x] Gemini review: <PASS / list of addressed findings>

## Known limitations (deferred)
- Saved "setup" recents (tuple of source+backups) not implemented; per-path recents only.
- Cross-check `--video-fast-strict` similarity matches don't render thumbnails (stage 5 thumbnail logic keyed on `match_reason != md5`; cross-check uses `cross_video_*` which would need separate template branch). Defer until users ask.
- Path overlap locking between concurrent runs not implemented (self-check doesn't have this either).
- Multi-backup progress phase detail in running view shows single bar; bash emits phase events but UI doesn't differentiate them yet.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 14: Final commit cleanup.**

If there were Gemini-fix commits, those stand on their own. Don't squash unless requested — the per-task commit history is useful for review.

- [ ] **Step 15: Return PR URL to the user.**

The `gh pr create` command prints the PR URL on success. Confirm it's reachable, then report to the user.

---

## Spec coverage check (self-review for the planner)

Each spec requirement → task:

| Spec section | Implementing task |
|--------------|-------------------|
| Bash `process_apply_list` cross-check case arm | Task 1 |
| Apply-list TSV reason mapping (`cross_hash` / `cross_video_*`) | Tasks 1 + 2 |
| Shared `composeApplyList` / `writeApplyList` with `mode` param | Task 2 |
| `ResultGroup.Mode` field + stamp in `BuildResults` | Task 3 |
| `ResultsView.ApplyURL` | Task 3 |
| History filter relaxation to `mode ∈ {self_check, cross_check}` | Task 4 |
| History row mode badge | Task 4 |
| History `dry_run==false` filter (bug fix) | Task 4 |
| `crosscheck.go` handlers (5 main + add-backup-row) | Task 5 |
| `crosscheck_form.html` with multi-backup picker | Task 5 |
| `crosscheck_backup_row.html` HTMX fragment | Task 5 |
| Form validation (source required, ≥1 backup required, allowlist) | Task 5 |
| `selfcheck_results.html` role-aware branching | Task 6 |
| HTTP route registration (replace placeholder + 5 new routes) | Task 7 |
| E2E smoke + Gemini + PR | Task 8 |

All covered. No gaps.

---

## Out-of-scope reminder (do not implement)

Per the spec:
- Saved "setup" recents (tuple memory).
- Cross-check similar-video thumbnails (`--video-fast-strict` × stage 5 thumbnail rendering).
- Path overlap locking across concurrent runs.
- Multi-backup phase-by-phase progress detail.
- i18n on new templates (stage 8).
- Detecting duplicates between backup folders.
