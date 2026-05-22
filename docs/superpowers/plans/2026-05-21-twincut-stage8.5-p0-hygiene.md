# Stage 8.5 — P0 Hygiene — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close three Stage 8 BLOCKERS — make preview→apply replayable, populate manifest keeper for L2/L3 quarantines, eliminate the TOCTOU rename race — without touching the Go/bash architectural boundary.

**Architecture:** Three independent surgical fixes. (1) Under `--json-events`, L1 candidates flow through NDJSON events (`thumb_candidate decision=thumb_l1_review`) instead of the source-scoped `_review.csv`; legacy CLI behavior unchanged. (2) Apply TSV gains a 7th `keeper` column — L2/L3 hydrate from journaled `keeper`, L1 leaves it empty (no paired keeper exists). (3) `RunManager.Start` accepts a caller-provided ID via `StartOptions.ID` with regex + existence validation; the post-Start rename in `handleThumbnailsApply` is deleted.

**Tech Stack:** bash, Go (`net/http`, `regexp`), NDJSON, TSV

**Spec:** [docs/superpowers/specs/2026-05-21-twincut-stage8.5-p0-hygiene-design.md](../specs/2026-05-21-twincut-stage8.5-p0-hygiene-design.md)

**Branch:** `feature/stage-8.5-p0-hygiene` (already created, off `feature/stage-8-thumbnail-ui`)

---

## File map

| File | Touched in tasks | Purpose |
|---|---|---|
| `lib/thumb.sh` | T1, T4 | `thumb_write_review` events-mode branch; `thumb_confirm_review` reads keeper column |
| `ui/server/events.go` | T2 | Decision doc comment update |
| `ui/server/results.go` | T2, T3 | BuildResults: L1 event branch, delete disk read; `ResultMember.Keeper` field |
| `ui/server/apply_list.go` | T3 | `composeThumbnailConfirmTSV` 7-column output |
| `ui/server/runs.go` | T5 | `StartOptions.ID`, regex + existence validation |
| `ui/server/thumbnail.go` | T6 | `handleThumbnailsApply` passes ID, no rename |
| `tests/p1_thumb_smoke.sh` | T1, T4 | New events-mode L1 section; sections 9/9c → 7-column |
| `ui/server/results_test.go` | T2, T3 | L1 event fixture; Keeper assertions |
| `ui/server/apply_list_test.go` | T3 | Keeper column assertions in existing tests + new L1-empty case |
| `ui/server/runs_test.go` | T5 | New tests for Start with caller-provided ID |
| `ui/server/thumbnail_test.go` | T6 | No-rename behavior assertion |
| `tests/manual/stage8_smoke.md` | T7 | Replay regression + manifest keeper validation cases |

---

## Task 1: [Bash] `thumb_write_review` events-mode branch

**Files:**
- Modify: `lib/thumb.sh:293-313` (current `thumb_write_review` body)
- Test: `tests/p1_thumb_smoke.sh` (new section after existing last section)

The current `thumb_write_review` unconditionally writes `<source>/_thumbnails/_review.csv`. Under `--json-events` (the Web UI path), Go must consume L1 candidates from the run journal, not from a mutable source-scoped file. The fix branches on `$JSON_EVENTS` (the global set by CLI arg parsing in `bin/twincut.sh:158, 876`). The `emit_event` helper is defined in `bin/twincut.sh:187` and is in scope here.

- [ ] **Step 1: Add the failing test section to smoke**

Locate the current last numbered section in `tests/p1_thumb_smoke.sh` (after section 11). Append:

```bash
# ---------------------------------------------------------------------
# Section 12: Stage 8.5 Fix 1 — L1 → NDJSON events under --json-events
# ---------------------------------------------------------------------
note "12. thumb-detect with --json-events emits thumb_l1_review events and skips _review.csv"
rm -rf "$SRC"; mkdir -p "$SRC"
# Make two thumbnail-sized images that don't match anything (L1-only suspects)
sips -z 200 200 "$SEED" --out "$SRC/orphanA.png" >/dev/null
sips -z 300 300 "$SEED" --out "$SRC/orphanB.png" >/dev/null

LOG12="/tmp/twincut_stage85_t12.log"
"$TWINCUT" --thumbnail-detect --dry-run --json-events \
  --source "$SRC" --assume-yes >"$LOG12" 2>&1

# Disk file must NOT exist (events replace it)
[[ ! -f "$SRC/_thumbnails/_review.csv" ]] \
  && ok "section 12: _review.csv NOT created under --json-events" \
  || bad "section 12: _review.csv was created under --json-events (should be skipped)"

# At least two L1 events present in log
N12=$(grep -c '"decision":"thumb_l1_review"' "$LOG12" || true)
[[ "$N12" -ge 2 ]] \
  && ok "section 12: $N12 thumb_l1_review events emitted (>=2 expected)" \
  || bad "section 12: only $N12 thumb_l1_review events in log (expected >=2)"

# Each event has path + reason + width + height + size_bytes
grep '"decision":"thumb_l1_review"' "$LOG12" | head -1 | \
  grep -q '"path":".*"' && \
  grep '"decision":"thumb_l1_review"' "$LOG12" | head -1 | \
  grep -q '"reason":"l1_only_' && \
  grep '"decision":"thumb_l1_review"' "$LOG12" | head -1 | \
  grep -q '"width":[0-9]' \
  && ok "section 12: L1 event has path/reason/width fields" \
  || bad "section 12: L1 event missing required fields"

# ---------------------------------------------------------------------
# Section 12b: Stage 8.5 regression — legacy CLI (no --json-events) still writes file
# ---------------------------------------------------------------------
note "12b. thumb-detect without --json-events still writes _review.csv (legacy CLI regression guard)"
rm -rf "$SRC"; mkdir -p "$SRC"
sips -z 200 200 "$SEED" --out "$SRC/orphanC.png" >/dev/null

LOG12B="/tmp/twincut_stage85_t12b.log"
"$TWINCUT" --thumbnail-detect --dry-run \
  --source "$SRC" --assume-yes >"$LOG12B" 2>&1

[[ -f "$SRC/_thumbnails/_review.csv" ]] \
  && ok "section 12b: _review.csv written for legacy CLI path" \
  || bad "section 12b: _review.csv missing for legacy CLI path"

grep -q "orphanC.png" "$SRC/_thumbnails/_review.csv" \
  && ok "section 12b: review file contains expected suspect" \
  || bad "section 12b: review file missing expected suspect"
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bash tests/p1_thumb_smoke.sh 2>&1 | tail -25`
Expected: FAIL on `_review.csv NOT created under --json-events` (because `thumb_write_review` doesn't yet branch on JSON_EVENTS), and FAIL on `thumb_l1_review events emitted` (because no such events are produced yet).

- [ ] **Step 3: Implement events-mode branch**

Replace the current `thumb_write_review` body in `lib/thumb.sh` (lines 293–313). The new function:

```bash
# Anything still L1=suspect (after L2/L3 passes) and still on disk:
# - Under --json-events: emit one thumb_candidate event per suspect (decision=thumb_l1_review);
#   do not write the source-scoped _review.csv (Stage 8.5 Fix 1: Go consumes events, not disk).
# - Legacy CLI (no --json-events): write _review.csv as before.
# We never delete or move L1-only suspects automatically.
thumb_write_review(){
  THUMB_REVIEW_CNT=0
  [[ -s "${THUMB_INDEX_FILE:-}" ]] || return 0

  if $JSON_EVENTS; then
    local f w h cls _sz
    while IFS=$'\t' read -r f w h cls; do
      [[ "$cls" == "ok" ]] && continue
      [[ ! -e "$f" ]] && continue   # already handled by L2/L3
      _sz="$(wc -c < "$f" 2>/dev/null | tr -d ' ')" || _sz=0
      emit_event "thumb_candidate" \
        "decision=thumb_l1_review" \
        "path=$f" \
        "reason=l1_only_${cls}" \
        "width=@${w:-0}" \
        "height=@${h:-0}" \
        "size_bytes=@${_sz:-0}"
      THUMB_REVIEW_CNT=$((THUMB_REVIEW_CNT+1))
    done < "$THUMB_INDEX_FILE"

    if (( THUMB_REVIEW_CNT > 0 )); then
      # fd 1 has been redirected to fd 2 by the --json-events epilogue,
      # so plain echo here writes to stderr (correct for human-readable log).
      echo "[*] L1-only suspects emitted as events: $THUMB_REVIEW_CNT"
    fi
    return 0
  fi

  mkdir -p "$THUMB_DIR" || die3 "cannot create $THUMB_DIR"
  if [[ ! -f "$THUMB_REVIEW_CSV" ]]; then
    printf 'path\treason\twidth\theight\tnote\n' > "$THUMB_REVIEW_CSV"
  fi

  while IFS=$'\t' read -r f w h cls; do
    [[ "$cls" == "ok" ]] && continue
    [[ ! -e "$f" ]] && continue   # already handled by L2/L3
    local reason="l1_only_${cls}"
    printf '%s\t%s\t%s\t%s\t\n' "$f" "$reason" "$w" "$h" >> "$THUMB_REVIEW_CSV"
    THUMB_REVIEW_CNT=$((THUMB_REVIEW_CNT+1))
  done < "$THUMB_INDEX_FILE"

  if (( THUMB_REVIEW_CNT > 0 )); then
    echo "[*] L1-only suspects pending review: $THUMB_REVIEW_CNT  → $THUMB_REVIEW_CSV"
  fi
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `bash tests/p1_thumb_smoke.sh 2>&1 | tail -25`
Expected: PASS on all section 12 + 12b assertions. Other sections unchanged.

- [ ] **Step 5: Commit**

```bash
git add lib/thumb.sh tests/p1_thumb_smoke.sh
git commit -m "fix(stage-8.5): thumb_write_review emits L1 events under --json-events

Under --json-events, emit one thumb_candidate event per L1 suspect
(decision=thumb_l1_review) and skip the source-scoped _review.csv
write. Legacy CLI path (no --json-events) unchanged. Closes BLOCKER #1
from gemini+codex stage-8 review by making preview→apply replayable.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 2: [Go] `BuildResults` reads L1 from events; remove disk-read

**Files:**
- Modify: `ui/server/results.go:190-241` (extend EventThumbCandidate case for L1)
- Modify: `ui/server/results.go:260-309` (delete entire disk-read block)
- Modify: `ui/server/events.go:80` (update Decision doc comment)
- Test: `ui/server/results_test.go` (new fixture + update existing)

Currently `BuildResults` reads `<source>/_thumbnails/_review.csv` post-loop to materialize an `l1-suspects` group. After Task 1, L1 candidates arrive as `thumb_candidate` events with `decision=thumb_l1_review`. The event handler must dispatch by decision and the disk-read block goes away.

- [ ] **Step 1: Write the failing test**

In `ui/server/results_test.go`, add this test. Use the existing test infrastructure (`newRunFromJournal` or whatever the file uses to build a `*Run` from a journal — check the top of the file for the helper; if none exists, build one inline using `os.WriteFile` to a temp dir and the `RunManager.AttachExisting` or similar API the file already uses).

```go
func TestBuildResults_L1FromEvents_NoDiskRead(t *testing.T) {
	tmp := t.TempDir()
	runID := "20260521T140000Z-stage85t2"
	journalDir := filepath.Join(tmp, "runs")
	if err := os.MkdirAll(journalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(journalDir, runID+".ndjson")

	// Write a minimal journal: run_start + two thumb_l1_review events + run_end.
	lines := []string{
		`{"type":"run_start","ts":1700000000,"run_id":"` + runID + `","mode":"thumbnail_detect_preview","source":"` + tmp + `/src"}`,
		`{"type":"thumb_candidate","ts":1700000001,"run_id":"` + runID + `","decision":"thumb_l1_review","path":"/tmp/src/orphanA.png","reason":"l1_only_suspect","width":200,"height":200,"size_bytes":1234}`,
		`{"type":"thumb_candidate","ts":1700000002,"run_id":"` + runID + `","decision":"thumb_l1_review","path":"/tmp/src/orphanB.png","reason":"l1_only_maybe","width":300,"height":300,"size_bytes":5678}`,
		`{"type":"run_end","ts":1700000003,"run_id":"` + runID + `","cancelled":false,"moved":0,"deleted":0,"restored":0}`,
	}
	if err := os.WriteFile(journalPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	run := &Run{
		ID:          runID,
		Mode:        "thumbnail_detect_preview",
		Status:      RunStatusSucceeded,
		SourcePath:  tmp + "/src",
		JournalPath: journalPath,
	}

	view, err := BuildResults(run)
	if err != nil {
		t.Fatalf("BuildResults: %v", err)
	}

	// Find l1-suspects group
	var l1 *ResultGroup
	for i := range view.Groups {
		if view.Groups[i].StringGroupID == "l1-suspects" {
			l1 = &view.Groups[i]
			break
		}
	}
	if l1 == nil {
		t.Fatalf("no l1-suspects group; groups=%+v", view.Groups)
	}
	if len(l1.Members) != 2 {
		t.Fatalf("expected 2 l1 members, got %d", len(l1.Members))
	}
	if l1.Members[0].Path != "/tmp/src/orphanA.png" || l1.Members[0].Role != "suspect" {
		t.Errorf("l1 member 0 unexpected: %+v", l1.Members[0])
	}
	if l1.Members[0].Reason != "l1_only_suspect" {
		t.Errorf("l1 member 0 reason: got %q want %q", l1.Members[0].Reason, "l1_only_suspect")
	}
	if l1.Members[0].Decision != "thumb_confirmed" {
		t.Errorf("l1 member 0 decision: got %q want %q (apply TSV needs allow-listed value)", l1.Members[0].Decision, "thumb_confirmed")
	}

	// Verify NO disk read happened: source dir should not be created
	if _, err := os.Stat(tmp + "/src/_thumbnails"); err == nil {
		t.Errorf("BuildResults created/read source _thumbnails dir; should be event-only")
	}
}
```

Also update the imports at the top of `results_test.go` if `strings`, `path/filepath`, or `os` aren't already there.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && go test ./server -run TestBuildResults_L1FromEvents_NoDiskRead -v`
Expected: FAIL with "no l1-suspects group" (the EventThumbCandidate case currently treats every event as L2/L3 and tries to attach to a `tc.GroupID`-keyed group; L1 events have no `group_id` and won't be classified properly).

- [ ] **Step 3a: Update `ThumbCandidate` struct in events.go (add `Reason`; update Decision doc)**

In `ui/server/events.go` lines 79–87 (the `ThumbCandidate` struct), update to:

```go
// ThumbCandidate is the parsed payload of a "thumb_candidate" event emitted
// by lib/thumb.sh during --dry-run --json-events. One event per candidate file.
type ThumbCandidate struct {
	Decision  string `json:"decision"`   // thumb_l2_exif | thumb_l3_embed | thumb_l1_review
	Path      string `json:"path"`       // absolute path of the candidate thumbnail
	Keeper    string `json:"keeper"`     // absolute path of the file being kept (L2/L3 only; empty for L1)
	GroupID   string `json:"group_id"`   // L2: EXIF fingerprint SHA1; L3: "l3:<sha1>"; absent for L1
	Reason    string `json:"reason"`     // L1 only: "l1_only_suspect" | "l1_only_maybe"; empty for L2/L3
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	SizeBytes int64  `json:"size_bytes"`
}
```

(Two changes: extend Decision doc to include `thumb_l1_review`; add the `Reason` field with its `json:"reason"` tag.)

- [ ] **Step 3b: Extend EventThumbCandidate case in BuildResults**

In `ui/server/results.go`, after the existing `var tc ThumbCandidate; if err := UnmarshalThumbCandidate ...` block (around line 201) and BEFORE the "Find or create the ResultGroup for this group_id" comment, insert the L1 branch:

```go
				// Stage 8.5 Fix 1: L1 suspects are flat (no paired keeper). They aggregate
				// into a single synthetic "l1-suspects" group; the apply path emits them
				// with decision=thumb_confirmed (the apply-TSV allow-listed value).
				if tc.Decision == "thumb_l1_review" {
					l1Idx := -1
					for gi := range view.Groups {
						if view.Groups[gi].StringGroupID == "l1-suspects" {
							l1Idx = gi
							break
						}
					}
					if l1Idx == -1 {
						view.Groups = append(view.Groups, ResultGroup{StringGroupID: "l1-suspects"})
						l1Idx = len(view.Groups) - 1
					}
					view.Groups[l1Idx].Members = append(view.Groups[l1Idx].Members, ResultMember{
						Path:      tc.Path,
						Role:      "suspect",
						Decision:  "thumb_confirmed",
						Reason:    tc.Reason,
						Width:     tc.Width,
						Height:    tc.Height,
						SizeBytes: tc.SizeBytes,
					})
					break
				}
```

- [ ] **Step 3c: Delete the disk-read block**

In `ui/server/results.go`, delete the entire block starting with the comment `// Thumbnail mode: read _review.csv for L1 suspects ...` (around line 260) through the closing `}` that terminates the `if len(l1Group.Members) > 0 { view.Groups = append(view.Groups, l1Group) }` clause (around line 309). The L1 group is now built inside the event loop.

After deletion, the function continues to whatever post-loop logic exists below line 309.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && go test ./server -run TestBuildResults_L1FromEvents_NoDiskRead -v`
Expected: PASS.

Run: `cd ui && go test ./server`
Expected: All other tests still pass. **If a pre-existing test relied on the disk-read path** (a fixture writing `<source>/_thumbnails/_review.csv` and asserting an L1 group is built from it), update that test to write a journal with `thumb_l1_review` events instead of the disk file. Look specifically at tests that create `_thumbnails` directories under `t.TempDir()`.

- [ ] **Step 5: Commit**

```bash
git add ui/server/results.go ui/server/events.go ui/server/results_test.go
git commit -m "fix(stage-8.5): BuildResults reads L1 from events, no source-disk read

EventThumbCandidate case gains a thumb_l1_review branch that appends
into a synthetic 'l1-suspects' group with Decision='thumb_confirmed'
(the apply-TSV allow-listed value). The post-loop _review.csv disk
read is deleted entirely.

Closes the Go side of BLOCKER #1: Web UI no longer depends on the
mutable source-scoped review file.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 3: [Go] `ResultMember.Keeper` + `composeThumbnailConfirmTSV` 7th column

**Files:**
- Modify: `ui/server/results.go:62-71` (add `Keeper string` to ResultMember)
- Modify: `ui/server/results.go:234-241` (populate Keeper for L2/L3 members in EventThumbCandidate case)
- Modify: `ui/server/apply_list.go:144-186` (composeThumbnailConfirmTSV adds keeper column)
- Test: `ui/server/apply_list_test.go` (extend existing tests + add L1 case)
- Test: `ui/server/results_test.go` (extend test from Task 2 to also cover L2/L3 keeper hydration)

Apply TSV currently emits 6 columns; bash's `qmove` receives `matched=""` for every row. Fix populates a 7th `keeper` column from journaled keeper for L2/L3; L1 leaves it empty (no paired keeper).

- [ ] **Step 1: Write the failing tests**

In `ui/server/apply_list_test.go`, add a new test (place after `TestComposeThumbnailConfirmTSV_DecisionPropagation`):

```go
func TestComposeThumbnailConfirmTSV_KeeperColumnFromMembers(t *testing.T) {
	groups := []ResultGroup{
		{
			StringGroupID: "l2:abc123",
			Members: []ResultMember{
				{Path: "/src/keeper-a.jpg", Role: "keeper"},
				{Path: "/src/thumb-a.jpg", Role: "thumbnail", Decision: "thumb_l2_exif", Keeper: "/src/keeper-a.jpg", Width: 200, Height: 200},
			},
		},
		{
			StringGroupID: "l3:def456",
			Members: []ResultMember{
				{Path: "/src/keeper-b.jpg", Role: "keeper"},
				{Path: "/src/thumb-b.jpg", Role: "thumbnail", Decision: "thumb_l3_embed", Keeper: "/src/keeper-b.jpg", Width: 300, Height: 300},
			},
		},
		{
			StringGroupID: "l1-suspects",
			Members: []ResultMember{
				{Path: "/src/orphan.jpg", Role: "suspect", Decision: "thumb_confirmed", Reason: "l1_only_suspect", Width: 400, Height: 400},
			},
		},
	}
	form := url.Values{
		"group:l2:abc123.member1": []string{"on"},
		"group:l3:def456.member1": []string{"on"},
		"group:l1-suspects.member0": []string{"on"},
	}
	out, err := composeThumbnailConfirmTSV(groups, form)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("want 4 lines (header + 3 rows), got %d: %q", len(lines), string(out))
	}
	wantHeader := "path\treason\twidth\theight\tnote\tdecision\tkeeper"
	if lines[0] != wantHeader {
		t.Errorf("header:\n got %q\nwant %q", lines[0], wantHeader)
	}
	// L2 row: keeper populated
	if !strings.HasSuffix(lines[1], "\tthumb_l2_exif\t/src/keeper-a.jpg") {
		t.Errorf("L2 row missing keeper: %q", lines[1])
	}
	// L3 row: keeper populated
	if !strings.HasSuffix(lines[2], "\tthumb_l3_embed\t/src/keeper-b.jpg") {
		t.Errorf("L3 row missing keeper: %q", lines[2])
	}
	// L1 row: keeper empty (trailing tab + EOL)
	if !strings.HasSuffix(lines[3], "\tthumb_confirmed\t") {
		t.Errorf("L1 row should end with empty keeper column: %q", lines[3])
	}
}

func TestComposeThumbnailConfirmTSV_RejectsTabInKeeper(t *testing.T) {
	groups := []ResultGroup{{
		StringGroupID: "l2:x",
		Members: []ResultMember{
			{Path: "/src/k.jpg", Role: "keeper"},
			{Path: "/src/t.jpg", Role: "thumbnail", Decision: "thumb_l2_exif", Keeper: "/src/bad\tkeeper.jpg"},
		},
	}}
	form := url.Values{"group:l2:x.member1": []string{"on"}}
	_, err := composeThumbnailConfirmTSV(groups, form)
	if err == nil {
		t.Fatal("expected error for tab-in-keeper, got nil")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd ui && go test ./server -run TestComposeThumbnailConfirmTSV_KeeperColumnFromMembers -v`
Expected: FAIL — either compile error (`ResultMember.Keeper` undefined) or header mismatch (currently 6 columns).

- [ ] **Step 3a: Add `Keeper` field to `ResultMember`**

In `ui/server/results.go` lines 62–71, replace the struct definition:

```go
// ResultMember is one file in a thumbnail-detect cluster.
type ResultMember struct {
	Path      string // absolute path
	Role      string // "keeper" | "thumbnail" | "suspect"
	Decision  string // "thumb_l2_exif" | "thumb_l3_embed" | "thumb_confirmed"
	Reason    string // "l1_only_thumb" | "l1_only_maybe" (L1 suspects only)
	Width     int
	Height    int
	SizeBytes int64
	Keeper    string // absolute path of the kept original (L2/L3); empty for L1 (no paired keeper)
}
```

- [ ] **Step 3b: Populate `Keeper` for L2/L3 in BuildResults**

In `ui/server/results.go` around line 234–241, modify the append for non-L1 thumb members (the existing `view.Groups[groupIdx].Members = append(..., ResultMember{ ... Decision: tc.Decision, ... })` block):

```go
				view.Groups[groupIdx].Members = append(view.Groups[groupIdx].Members, ResultMember{
					Path:      tc.Path,
					Role:      "thumbnail",
					Decision:  tc.Decision,
					Keeper:    tc.Keeper,
					Width:     tc.Width,
					Height:    tc.Height,
					SizeBytes: tc.SizeBytes,
				})
```

(Only the `Keeper: tc.Keeper,` line is added.)

- [ ] **Step 3c: Update `composeThumbnailConfirmTSV` to 7 columns**

In `ui/server/apply_list.go` lines 144–186, replace the function:

```go
// composeThumbnailConfirmTSV walks thumbnail ResultGroups and the apply form
// to produce the seven-column enhanced review TSV consumed by --thumb-confirm.
// Only checked members (form key "group:<gid>.member<i>=on") are included.
// Keeper-role members are never included regardless of form state.
//
// TSV columns (tab-separated, no quoting):
//   path  reason  width  height  note  decision  keeper
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
```

- [ ] **Step 3d: Update existing tests in apply_list_test.go**

The existing `TestComposeThumbnailConfirmTSV_ChecksFiltered`, `_DecisionPropagation`, `_AllowsCommasAndQuotesUnescaped`, `_UnicodePaths`, `_RejectsTabInPath`, `_RejectsNewlineInPath` all assert on 6-column output. For each, find the `wantHeader` or row-shape assertion and append `\tkeeper` to the header expectation and `\t""` (or actual keeper value if the fixture sets one) to row expectations. The implementer should grep for `path\treason\twidth\theight\tnote\tdecision` literal strings in the test file and append `\tkeeper` to header, `\t` to row tails.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd ui && go test ./server -v`
Expected: All tests pass. New keeper tests pass. Existing TestComposeThumbnailConfirmTSV_* pass with updated 7-column assertions.

- [ ] **Step 5: Commit**

```bash
git add ui/server/results.go ui/server/apply_list.go ui/server/results_test.go ui/server/apply_list_test.go
git commit -m "fix(stage-8.5): apply TSV gains keeper column (L2/L3 hydrated; L1 empty)

ResultMember gains a Keeper field. BuildResults populates it from the
thumb_candidate event's keeper field for L2/L3 (decision != l1_review).
composeThumbnailConfirmTSV emits a 7-column TSV with keeper as the
trailing column; L1 rows carry an empty keeper (no paired keeper exists,
by spec). Field-guard tab/newline rejection extends to keeper.

Closes the Go side of BLOCKER #2: manifest will receive the keeper
relationship that justified each L2/L3 move once bash consumer (Task 4)
lands.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 4: [Bash] `thumb_confirm_review` reads keeper from $7

**Files:**
- Modify: `lib/thumb.sh:359-418` (current `thumb_confirm_review` body)
- Modify: `tests/p1_thumb_smoke.sh:226-249` (section 9 — extend to 7-column with keeper)
- Modify: `tests/p1_thumb_smoke.sh:270-287` (section 9c — extend to 7-column)
- Leave: `tests/p1_thumb_smoke.sh:252-267` (section 9b legacy 5-column) **unchanged**

The bash consumer must read the 7th column (keeper) and pass it to `qmove` as the `matched` argument so manifest entries record the keeper relationship.

- [ ] **Step 1: Update smoke sections 9 and 9c (failing test setup)**

In `tests/p1_thumb_smoke.sh`, section 9 (lines 235–239), replace the TSV fixture to 7 columns and add a keeper-grep assertion. The block becomes:

```bash
# 7-column TSV: path\treason\twidth\theight\tnote\tdecision\tkeeper (no quoting)
printf 'path\treason\twidth\theight\tnote\tdecision\tkeeper\n' > "$CSV9"
printf '%s\tl1_only_thumb\t200\t200\t\tthumb_l2_exif\t%s\n' "$SRC/thumbA.png" "$SRC/keeperA.png" >> "$CSV9"
printf '%s\tl1_only_thumb\t300\t300\t\tthumb_l3_embed\t%s\n' "$SRC/thumbB.png" "$SRC/keeperB.png" >> "$CSV9"
printf '%s\tl1_only_thumb\t400\t400\t\tthumb_confirmed\t\n' "$SRC/thumbC.png" >> "$CSV9"
```

(The `thumb_confirmed` row leaves keeper empty — L1 case.)

The keeper-file existence isn't required for the TSV to be parsed correctly (qmove will record matched even if the file isn't present in section 9's tmp dir), so no extra `sips` calls needed.

After the existing manifest assertions (after line 249), append:

```bash
grep -q "$SRC/keeperA.png" "$MF9" && ok "9: manifest records L2 keeper path"   || bad "9: L2 keeper missing from manifest"
grep -q "$SRC/keeperB.png" "$MF9" && ok "9: manifest records L3 keeper path"   || bad "9: L3 keeper missing from manifest"
```

In section 9c (line 276), replace the header line to add keeper, and the row line to include an empty keeper field:

```bash
printf 'path\treason\twidth\theight\tnote\tdecision\tkeeper\n' > "$CSV9C"
printf '%s\tl1_only_thumb\t200\t200\t\tinvalid_value\t\n' "$SRC/thumbE.png" >> "$CSV9C"
```

Section 9b (legacy 5-column, lines 252–267): **leave entirely unchanged**. This is the backward-compat guard.

- [ ] **Step 2: Run smoke to verify section 9 fails**

Run: `bash tests/p1_thumb_smoke.sh 2>&1 | grep -E "section 9|9: " | head -20`
Expected: FAIL on `9: manifest records L2 keeper path` (manifest still records empty matched because bash hasn't been updated).

- [ ] **Step 3: Update `thumb_confirm_review`**

In `lib/thumb.sh` (the `thumb_confirm_review` body starting around line 359), inside the `while IFS= read -r _raw_line; do ... done` loop, find this section:

```bash
    local p dec
    p="$(awk -F'\t' '{print $1}' <<< "$_raw_line")"
    dec="$(awk -F'\t' '{print $6}' <<< "$_raw_line")"
```

Add a keeper extraction and update the `qmove` call. Replace those lines and the subsequent `qmove ...` invocation:

```bash
    local p dec keeper
    p="$(awk -F'\t' '{print $1}' <<< "$_raw_line")"
    dec="$(awk -F'\t' '{print $6}' <<< "$_raw_line")"
    keeper="$(awk -F'\t' '{print $7}' <<< "$_raw_line")"  # empty for legacy 6/5-column TSVs or L1 rows
```

Then the `qmove` call further down — currently:

```bash
    if qmove "$p" "$THUMB_DIR" "" "" "$dec"; then
```

becomes:

```bash
    if qmove "$p" "$THUMB_DIR" "$keeper" "" "$dec"; then
```

The 3rd positional arg of `qmove` is `MATCHED` (the kept original); empty string is legal and yields the legacy behavior.

- [ ] **Step 4: Run smoke to verify it passes**

Run: `bash tests/p1_thumb_smoke.sh 2>&1 | tail -30`
Expected: All sections (including 9, 9b, 9c, and 12/12b from Task 1) pass.

- [ ] **Step 5: Commit**

```bash
git add lib/thumb.sh tests/p1_thumb_smoke.sh
git commit -m "fix(stage-8.5): thumb_confirm_review reads keeper from \$7 → qmove matched

Apply TSV's 7th column carries the kept-original path (L2/L3 only).
thumb_confirm_review extracts \$7 and passes it to qmove as the matched
argument so manifest entries record the keeper relationship.

Backward compat: 5- and 6-column legacy TSVs yield empty \$7 → qmove
receives matched=\"\" → existing behavior preserved (section 9b unchanged).

Closes the bash side of BLOCKER #2. Pairs with Task 3.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 5: [Go] `StartOptions.ID` + regex/existence validation in `Start`

**Files:**
- Modify: `ui/server/runs.go:238-392` (`StartOptions`, `Start`, package-level regex)
- Test: `ui/server/runs_test.go` (4 new test cases)

`StartOptions` gains an optional `ID string`. `Start` validates it against `^\d{8}T\d{6}Z-[a-z0-9]+$` and rejects collisions on the journal path.

- [ ] **Step 1: Write the failing tests**

In `ui/server/runs_test.go`, append:

```go
func TestStart_GeneratesIDWhenOptsIDEmpty(t *testing.T) {
	mgr := newTestRunManager(t)
	r, err := mgr.Start(StartOptions{
		Mode: "self_check",
		Args: []string{"--help"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if r.ID == "" {
		t.Fatal("Run.ID empty")
	}
	if !regexp.MustCompile(`^\d{8}T\d{6}Z-[a-z0-9]+$`).MatchString(r.ID) {
		t.Errorf("Run.ID does not match expected shape: %q", r.ID)
	}
}

func TestStart_RejectsMalformedCallerID(t *testing.T) {
	mgr := newTestRunManager(t)
	bad := []string{
		"../etc/passwd",
		"not-a-run-id",
		"20260521T140000Z-",       // empty suffix
		"20260521T140000Z-UPPER",  // uppercase
		"20260521T140000Z-abc/de", // slash
		"",                        // empty (caller passed "" → should default, not error; covered above)
	}
	for _, id := range bad {
		if id == "" {
			continue // empty means "generate", not "error"
		}
		_, err := mgr.Start(StartOptions{
			ID:   id,
			Mode: "self_check",
			Args: []string{"--help"},
		})
		if err == nil {
			t.Errorf("Start with malformed ID %q: expected error, got nil", id)
		}
	}
}

func TestStart_RejectsCollidingCallerID(t *testing.T) {
	mgr := newTestRunManager(t)
	id := "20260521T140000Z-stage85t5"

	// Pre-create the journal file
	journalDir := filepath.Join(mgr.stateDir, "runs")
	if err := os.MkdirAll(journalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(journalDir, id+".ndjson")
	if err := os.WriteFile(journalPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := mgr.Start(StartOptions{
		ID:   id,
		Mode: "self_check",
		Args: []string{"--help"},
	})
	if err == nil {
		t.Fatal("Start with colliding ID: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error should mention collision; got %v", err)
	}
}

func TestStart_AcceptsValidCallerID(t *testing.T) {
	mgr := newTestRunManager(t)
	id := "20260521T140000Z-stage85t5b"

	r, err := mgr.Start(StartOptions{
		ID:   id,
		Mode: "self_check",
		Args: []string{"--help"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if r.ID != id {
		t.Errorf("Run.ID: got %q want %q", r.ID, id)
	}
	// Journal must exist at the requested ID's path
	journalPath := filepath.Join(mgr.stateDir, "runs", id+".ndjson")
	if _, err := os.Stat(journalPath); err != nil {
		t.Errorf("journal not created at expected path %q: %v", journalPath, err)
	}
}
```

Note on `newTestRunManager`: this helper may already exist in `runs_test.go` (used by `TestRunMode_*`). If it does, reuse it. If not, define it inline at the top of the new tests block — typical shape:

```go
func newTestRunManager(t *testing.T) *RunManager {
	t.Helper()
	tmp := t.TempDir()
	mgr, err := NewRunManager(RunManagerConfig{StateDir: tmp, TwincutPath: "/bin/true"})
	if err != nil {
		t.Fatal(err)
	}
	return mgr
}
```

(Check the actual `NewRunManager` signature in `runs.go` — adjust struct field names if they differ.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd ui && go test ./server -run TestStart_ -v`
Expected: All 4 new tests fail (compile error: `StartOptions` has no `ID` field).

- [ ] **Step 3a: Add the regex and `ID` field**

At package-level scope in `ui/server/runs.go` (near other package-level vars, or just above the `StartOptions` struct):

```go
var runIDRegex = regexp.MustCompile(`^\d{8}T\d{6}Z-[a-z0-9]+$`)
```

Ensure `import "regexp"` is added to the file's import block if not present.

Update `StartOptions` (currently lines 238–242):

```go
type StartOptions struct {
	ID   string   // optional; empty → newRunID()
	Mode string
	Args []string
	Env  []string
}
```

- [ ] **Step 3b: Add validation in `Start`**

In `ui/server/runs.go`, at the top of `Start`'s body, replace the existing first line `id := newRunID()` with:

```go
	var id string
	if opts.ID == "" {
		id = newRunID()
	} else {
		if !runIDRegex.MatchString(opts.ID) {
			return nil, fmt.Errorf("invalid caller-provided run ID: %q", opts.ID)
		}
		journalCheckPath := filepath.Join(m.stateDir, "runs", opts.ID+".ndjson")
		if _, err := os.Stat(journalCheckPath); err == nil {
			return nil, fmt.Errorf("run journal already exists for ID: %q", opts.ID)
		}
		id = opts.ID
	}
```

The rest of `Start` is unchanged — `id` is already used downstream.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd ui && go test ./server -run TestStart_ -v`
Expected: All 4 pass.

Run: `cd ui && go test ./server`
Expected: All existing tests still pass (no behavioral change for callers not passing ID).

- [ ] **Step 5: Commit**

```bash
git add ui/server/runs.go ui/server/runs_test.go
git commit -m "fix(stage-8.5): StartOptions accepts caller-provided ID with validation

StartOptions gains an optional ID field. If non-empty, Start validates
against ^\d{8}T\d{6}Z-[a-z0-9]+\$ (matching newRunID() shape, prevents
path traversal) and rejects collisions on the journal path. If empty,
Start generates an ID via newRunID() as before.

No existing caller passes ID, so default behavior is unchanged. Prepares
for Task 6 (handleThumbnailsApply uses caller-provided ID to eliminate
the TOCTOU rename race).

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 6: [Go] `handleThumbnailsApply` uses caller-provided ID, delete rename

**Files:**
- Modify: `ui/server/thumbnail.go:148-177` (apply handler — TSV write + Start + rename)
- Test: `ui/server/thumbnail_test.go` (assert no-rename behavior)

`applyRunID` is now passed to `Start` via `StartOptions.ID`. The post-Start rename block is deleted entirely.

- [ ] **Step 1: Write the failing test**

In `ui/server/thumbnail_test.go`, append:

```go
func TestHandleThumbnailsApply_NoRenameRunIDMatchesAppliedID(t *testing.T) {
	srv := newThumbTestServer(t)

	// Set up a succeeded preview run with one L2 result so the apply path produces a row.
	previewID := "20260521T140000Z-stage85t6prev"
	previewRun := &Run{
		ID:         previewID,
		Mode:       "thumbnail_detect_preview",
		Status:     RunStatusSucceeded,
		SourcePath: t.TempDir(),
		Results: &ResultsView{
			Groups: []ResultGroup{{
				StringGroupID: "l2:fake",
				Members: []ResultMember{
					{Path: "/src/keeper.jpg", Role: "keeper"},
					{Path: "/src/thumb.jpg", Role: "thumbnail", Decision: "thumb_l2_exif", Keeper: "/src/keeper.jpg"},
				},
			}},
		},
	}
	storeRun(srv.runs, previewID, previewRun)

	form := url.Values{}
	form.Set("source", previewRun.SourcePath)
	form.Set("preview_run_id", previewID)
	form.Set("group:l2:fake.member1", "on")

	req := httptest.NewRequest("POST", "/api/thumbnails/apply", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleThumbnailsApply(rec, req)

	if rec.Code/100 != 2 && rec.Code/100 != 3 {
		t.Fatalf("apply handler: status %d, body %s", rec.Code, rec.Body.String())
	}

	// Find the apply run (not the preview).
	// If the run manager exposes a List/Snapshot method, use it; otherwise iterate the
	// internal map the way other tests in this file already do.
	var applyRun *Run
	for _, r := range listAllRuns(srv.runs) { // helper used by other thumbnail tests
		if r.ID != previewID && r.Mode == "thumbnail_detect_apply" {
			applyRun = r
			break
		}
	}
	if applyRun == nil {
		t.Fatal("apply run not found in run manager")
	}

	// Core no-rename assertion: TSV must exist at <stateDir>/runs/<applyRun.ID>.thumb-confirm.tsv.
	tsvPath := filepath.Join(srv.opts.StateDir, "runs", applyRun.ID+".thumb-confirm.tsv")
	if _, err := os.Stat(tsvPath); err != nil {
		t.Errorf("TSV not at expected path %q: %v", tsvPath, err)
	}
}
```

If `listAllRuns` (or an equivalent listing helper) doesn't exist yet, the implementer should either reuse the iteration pattern already used by other tests in `thumbnail_test.go` (e.g., the test added in Stage 8 Task 11), or read `srv.runs`'s internal map directly via whatever package-private accessor `storeRun` uses.

The core guarantee being tested is one line: **a TSV exists at `<stateDir>/runs/<applyRun.ID>.thumb-confirm.tsv`**. Pre-fix this is false because the rename moved it to a different name. Post-fix this is true because `applyRunID == run.ID` and no rename happens.

A weaker but sufficient alternative test (if the listing API is awkward): assert that after the handler runs, exactly one `*.thumb-confirm.tsv` file exists under `<stateDir>/runs/`, and that its basename (minus extension) matches a valid run ID present in the manager. The pre-fix code could create two such files (the original + the rename target) if rename succeeds but a follow-up rename to-already-exists happens; post-fix can only create one.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && go test ./server -run TestHandleThumbnailsApply_NoRenameRunIDMatchesAppliedID -v`
Expected: FAIL — the TSV exists at `applyRunID` (a different value from `run.ID` since Start generated a new ID), then the rename block tries to rename to `run.ID`. After the rename attempt, the file at `<runs>/<applyRunID>.tsv` may or may not be there. The args slice still references the original applyRunID path. So the test may pass coincidentally — be prepared to tighten by also asserting `applyRun.ID` does NOT match the original applyRunID (which would prove the rename happened).

Alternative simpler assertion: check that `srv.runs.Start` was called with `StartOptions.ID == applyRunID`. The current code doesn't pass ID, so this is provably false.

- [ ] **Step 3: Update `handleThumbnailsApply`**

In `ui/server/thumbnail.go` lines 148–177, find this section:

```go
applyRunID := newRunID()
runsDir := filepath.Join(s.opts.StateDir, "runs")
// ...
tsvPath := filepath.Join(runsDir, applyRunID+".thumb-confirm.tsv")
if err := os.WriteFile(tsvPath, tsvData, 0o644); err != nil { /* error response */ }

thumbDir := filepath.Join(source, "_thumbnails")
args := []string{"--thumb-confirm", tsvPath, "--assume-yes", "--json-events", "--thumb-dir", thumbDir, "--source", source}
run, err := s.runs.Start(StartOptions{Mode: "thumbnail_detect_apply", Args: args})
// ...

// If the run manager assigned a different ID, rename the TSV to match.
if run.ID != applyRunID {
    newTSVPath := filepath.Join(runsDir, run.ID+".thumb-confirm.tsv")
    if renameErr := os.Rename(tsvPath, newTSVPath); renameErr != nil {
        // Non-fatal: log but continue — TSV still exists under applyRunID name.
        _ = renameErr
    }
}
```

Replace with:

```go
applyRunID := newRunID()
runsDir := filepath.Join(s.opts.StateDir, "runs")
// ...
tsvPath := filepath.Join(runsDir, applyRunID+".thumb-confirm.tsv")
if err := os.WriteFile(tsvPath, tsvData, 0o644); err != nil { /* error response (unchanged) */ }

thumbDir := filepath.Join(source, "_thumbnails")
args := []string{"--thumb-confirm", tsvPath, "--assume-yes", "--json-events", "--thumb-dir", thumbDir, "--source", source}
run, err := s.runs.Start(StartOptions{ID: applyRunID, Mode: "thumbnail_detect_apply", Args: args})
// ... (no rename block — run.ID == applyRunID always)
```

(Only two surgical changes: add `ID: applyRunID,` to `StartOptions{...}` and delete the entire `if run.ID != applyRunID { ... }` block. Existing error-handling around `s.runs.Start` is unchanged.)

- [ ] **Step 4: Run test to verify it passes**

Run: `cd ui && go test ./server -run TestHandleThumbnailsApply_NoRenameRunIDMatchesAppliedID -v`
Expected: PASS.

Run: `cd ui && go test ./server`
Expected: All other tests still pass.

- [ ] **Step 5: Commit**

```bash
git add ui/server/thumbnail.go ui/server/thumbnail_test.go
git commit -m "fix(stage-8.5): handleThumbnailsApply uses caller-provided ID, no rename

applyRunID is now passed to RunManager.Start via StartOptions.ID,
guaranteeing run.ID == applyRunID. The post-Start rename block is
deleted — the bash child reads the TSV at the original path, no
window exists for a rename to win the race.

Closes BLOCKER #3 (gemini P1 TOCTOU rename race).

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 7: [Doc] Manual smoke — replay regression + manifest keeper case

**Files:**
- Modify: `tests/manual/stage8_smoke.md` (append two new cases)

Captures the two regressions that BLOCKER #1 and BLOCKER #2 produced, so a human running the manual smoke can catch them if they ever come back.

- [ ] **Step 1: Append to `tests/manual/stage8_smoke.md`**

At the end of the existing manual smoke doc, append:

````markdown
## Stage 8.5 regression cases

### Replay regression (BLOCKER #1)

This test catches the source-scoped `_review.csv` re-emerging or any
similar leak that lets the apply view drift from the preview snapshot.

1. In a scratch source dir, drop ~6 small images (some look like
   thumbnails — under 512px on the long edge — others look like
   half-size dupes between 512 and 1024px).
2. Open the Web UI, hit **Thumbnails**, pick the scratch source dir,
   leave thresholds at default, **Preview**. Note the L1 suspect set.
   Copy the `preview_run_id` from the URL (or page footer if shown).
3. Without leaving the preview page, change `--thumb-max-edge` to a
   much smaller value (e.g. 64) and **Preview again**. The L1 set
   should now be empty or very different (fewer files qualify as
   "thumbnail-sized").
4. Navigate back to the FIRST preview by URL (`/runs/<preview_run_id>`
   or via the History tab). Confirm L1 group shows the **original**
   suspect set.
5. Check the L1 boxes you want to quarantine, **Apply**.
6. After apply completes, open the manifest TSV and the quarantine
   dir. Confirm the moved files match what you selected in the FIRST
   preview, NOT the second preview's (smaller) set.

If files from the SECOND preview's threshold show up in the
quarantine, the source-of-truth has drifted again — re-investigate
whether `_review.csv` is being written under `--json-events` or a
similar source-disk state has crept back into BuildResults.

### Manifest keeper validation (BLOCKER #2)

This test catches a regression where the apply TSV's keeper column
loses its hydration (Go side) or bash's `qmove` stops receiving it.

1. Set up a scratch source dir with at least one L2 hit (full-size
   image + EXIF-stripped thumbnail-size sibling — typically what
   `iPhoto` exports + a generated `convert -strip` thumbnail).
2. Preview, confirm L2 group shows up with both the keeper and the
   thumbnail.
3. Check the thumbnail in the L2 group, **Apply**.
4. Open the manifest TSV at the path shown in the apply result page.
5. Locate the row for the moved thumbnail. Verify the **matched**
   column contains the absolute path to the L2 keeper file. If empty,
   the keeper-hydration chain broke — check that:
   - `thumb_candidate` events in the preview run journal contain
     `keeper=<path>`,
   - `ResultMember.Keeper` is populated when BuildResults runs,
   - `composeThumbnailConfirmTSV` writes the 7th column,
   - `thumb_confirm_review` reads `$7` and passes it to `qmove`.
````

- [ ] **Step 2: Verify the doc renders sensibly**

Open `tests/manual/stage8_smoke.md` in a markdown viewer (or `cat`).
Expected: appended sections are well-formed, headings are at the right
level, no broken markdown.

- [ ] **Step 3: Commit**

```bash
git add tests/manual/stage8_smoke.md
git commit -m "docs(stage-8.5): manual smoke — replay + manifest keeper cases

Two regression cases tied to BLOCKER #1 (preview→apply replay) and
BLOCKER #2 (keeper column in apply TSV). Run by a human; cheap insurance.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Final verification

After all 7 tasks land:

- [ ] **Bash smoke**: `bash tests/p1_thumb_smoke.sh 2>&1 | tail -10` shows green sections including 9, 9b, 9c, 12, 12b.
- [ ] **Go tests**: `cd ui && go test ./server -v` — all green.
- [ ] **Go vet**: `cd ui && go vet ./server` — no findings.
- [ ] **Manual smoke**: walk both new cases in `tests/manual/stage8_smoke.md`.

Then dispatch `reviewer-gemini` against the branch diff. If gemini finds substantive issues, fix and re-review. The architectural-review signals (Codex) are not expected to fire — this is pure hygiene, no new modules or boundary changes.

After review passes, ready to merge into `feature/stage-8-thumbnail-ui` or open a PR straight to `main` depending on integration strategy.
