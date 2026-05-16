# Stage 6: History tab + Restore — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Surface every past UI-originated self-check Apply in a History tab, let the user click any row to see a live `--restore-dry-run` preview, and confirm to roll back inside the browser.

**Architecture:** `do_restore` in `twincut.sh` gains NDJSON event emission so it flows through the same SSE / run-manager pipeline as scan/apply. The Go server adds `ui/server/history.go` with list + preview + apply + done handlers reusing the existing run manager and running/done templates. The running template's `IsApply bool` is replaced by a `NextURL string` so it stays workflow-agnostic.

**Tech Stack:** bash (twincut.sh), Go net/http + html/template, HTMX + SSE.

---

## File Structure

**Created:**
- `ui/server/history.go` — `collectHistory`, `resolveManifest`, all four handlers
- `ui/server/history_test.go` — unit tests for the helpers
- `ui/templates/history_list.html` — the History list view
- `ui/templates/history_preview.html` — the dry-run summary + Confirm/Cancel

**Modified:**
- `bin/twincut.sh` — `do_restore` gains `emit_event` calls (~25 added lines, no behavior change)
- `tests/json_events/run_tests.py` — adds `test_restore_dry_run_emits_action_events` and `test_restore_executes_and_emits_run_end`
- `ui/server/selfcheck.go` — `selfCheckRunningData` swaps `IsApply bool` for `NextURL string` + `Mode string` + `ShowActions bool`. Preview and apply handlers populate the new fields.
- `ui/server/results.go` — `ResultsView` gains `Mode string` (so done template can read "Restored" vs. "Quarantined"). `BuildResults` propagates run_start.mode.
- `ui/templates/selfcheck_running.html` — title switches on `.Mode`; JS handoff reads `data-next-url`
- `ui/templates/selfcheck_done.html` — title switches on `.Mode`
- `ui/server/http.go` — register four new routes + swap History placeholder
- `ui/templates/_layout.html` (or wherever the History sidebar link lives) — only needs route change

---

## Task 1: Bash — emit JSON events from `do_restore`

**Files:**
- Modify: `bin/twincut.sh:696-768` (the `do_restore` function)

The existing function uses plain `echo` for everything. We keep those echoes (they're still useful for CLI users) and add `emit_event` calls in parallel so the UI can consume the event stream identically to apply.

- [ ] **Step 1: Add Python test stub that will fail** — open `tests/json_events/run_tests.py` and append two test stubs at the bottom (just before `main()`):

```python
def test_restore_dry_run_emits_action_events(tmp: Path) -> None:
    # Seed a manifest TSV that mimics one written by a self-check apply,
    # then run --restore --restore-dry-run --json-events and assert the
    # event stream covers run_start/progress/action/run_end with the
    # restore-specific kinds and counts.
    manifest = tmp / "_manifest.tsv"
    quar_dir = tmp / "_QUARANTINE" / "_self_dupes"
    quar_dir.mkdir(parents=True)
    # File still present in quarantine, original missing → restorable
    write_file(quar_dir / "ok.jpg", b"data-ok")
    # File that we'll claim is in quarantine but actually isn't → missing
    # File whose original already exists at restore-target → conflict
    write_file(tmp / "conflict_target.jpg", b"already-here")
    write_file(quar_dir / "conflict.jpg", b"data-conflict")

    manifest.write_text(
        "# header noise twincut tolerates\n"
        "run_id\tts\torig\tquar\tmatched\talgo\thash\tdecision\tsize\tmtime\n"
        f"R1\t1\t{tmp / 'ok.jpg'}\t{quar_dir / 'ok.jpg'}\t-\tmd5\tDEAD\tself:moved\t0\t0\n"
        f"R1\t1\t{tmp / 'gone.jpg'}\t{quar_dir / 'gone.jpg'}\t-\tmd5\tBEEF\tself:moved\t0\t0\n"
        f"R1\t1\t{tmp / 'conflict_target.jpg'}\t{quar_dir / 'conflict.jpg'}\t-\tmd5\tCAFE\tself:moved\t0\t0\n"
        f"R1\t1\t{tmp / 'deleted_orig.jpg'}\t\t-\tmd5\tFADE\tself:deleted\t0\t0\n"
    )

    events, _, ec = run_twincut(["--restore", str(manifest), "--restore-dry-run"])
    assert ec == 0, f"expected exit 0, got {ec}"
    validate_structure(events)

    starts = [e for e in events if e["type"] == "run_start"]
    assert len(starts) == 1 and starts[0]["mode"] == "restore", starts
    assert starts[0]["source"] == str(manifest)

    ends = [e for e in events if e["type"] == "run_end"]
    assert len(ends) == 1, ends
    end = ends[0]
    assert end["restored"] == 1, f"restored count: {end}"
    assert end["missing"] == 1, f"missing count: {end}"
    assert end["skipped"] == 1, f"skipped count: {end}"
    assert end["unrecoverable"] == 1, f"unrecoverable count: {end}"
    assert end["cancelled"] is False

    actions = [e for e in events if e["type"] == "action"]
    kinds = sorted(a["kind"] for a in actions)
    assert kinds == ["restore", "restore_conflict", "restore_missing", "restore_unrecoverable"], kinds


def test_restore_executes_and_emits_run_end(tmp: Path) -> None:
    # Real (non-dry-run) restore of one file: emits a restore action with
    # dry_run=false and the file actually moves back.
    manifest = tmp / "_manifest.tsv"
    quar_dir = tmp / "_QUARANTINE"
    quar_dir.mkdir()
    write_file(quar_dir / "back.jpg", b"data-back")
    manifest.write_text(
        "run_id\tts\torig\tquar\tmatched\talgo\thash\tdecision\tsize\tmtime\n"
        f"R\t0\t{tmp / 'back.jpg'}\t{quar_dir / 'back.jpg'}\t-\tmd5\tHH\tself:moved\t0\t0\n"
    )

    events, _, ec = run_twincut(["--restore", str(manifest)])
    assert ec == 0
    validate_structure(events)

    actions = [e for e in events if e["type"] == "action" and e["kind"] == "restore"]
    assert len(actions) == 1
    assert actions[0]["dry_run"] is False
    assert actions[0]["src"] == str(quar_dir / "back.jpg")
    assert actions[0]["dst"] == str(tmp / "back.jpg")
    assert (tmp / "back.jpg").exists()
    assert not (quar_dir / "back.jpg").exists()
```

Also extend the `REQUIRED_FIELDS` dict near the top of the file to teach the validator about restore-shaped events. Find the block (~line 35–55) and update:

```python
REQUIRED_FIELDS = {
    "run_start": {"mode", "source"},
    "run_end": {"total", "dupes", "moved", "cancelled"},
    "progress": {"phase", "done"},
    "dup_group": {"group_id", "match_reason", "keep_path", "remove"},
    "action": {"kind", "src"},
    "warn": {"code"},
    "error": {"code", "detail"},
}
```

The existing `run_end` schema is for scan/apply; restore's run_end has a different shape (no `total`/`dupes`/`moved`, instead `restored`/`missing`/`skipped`/`unrecoverable`). Rather than ratchet up REQUIRED_FIELDS, relax `run_end` to only require `cancelled`, and let each test assert the keys it cares about:

```python
REQUIRED_FIELDS = {
    "run_start": {"mode"},
    "run_end": {"cancelled"},
    "progress": {"phase", "done"},
    "dup_group": {"group_id", "match_reason", "keep_path", "remove"},
    "action": {"kind", "src"},
    "warn": {"code"},
    "error": {"code", "detail"},
}
```

- [ ] **Step 2: Run the test, confirm it fails**

```bash
python3 tests/json_events/run_tests.py 2>&1 | tail -15
```

Expected: the two new tests FAIL because `do_restore` doesn't yet emit any NDJSON events.

- [ ] **Step 3: Add `emit_event` calls to `do_restore`** — open `bin/twincut.sh` and replace the whole function (lines 696–768) with:

```bash
do_restore(){
  local mf="$1"
  [[ -f "$mf" ]] || die "manifest not found: $mf"

  local restored=0 skipped_exists=0 missing=0 unrecoverable=0 errors=0
  local done_marker="${mf}.restored"
  local already_done=""
  [[ -f "$done_marker" ]] && already_done="$(cat "$done_marker")"

  emit_event run_start mode=restore source="$mf"

  # Count restorable rows for the progress total. Cheap upfront walk.
  local total=0
  while IFS=$'\t' read -r run_id _rest; do
    [[ -z "${run_id:-}" ]] && continue
    [[ "$run_id" == "run_id" ]] && continue
    [[ "${run_id:0:1}" == "#" ]] && continue
    total=$((total+1))
  done < "$mf"

  echo "[*] Restoring from manifest: $mf"
  $RESTORE_DRY_RUN && echo "[*] (restore dry-run; no files will move)"

  local seen=0
  while IFS=$'\t' read -r run_id ts orig quar matched algo hh dec sz mt; do
    [[ -z "${run_id:-}" ]] && continue
    [[ "$run_id" == "run_id" ]] && continue
    [[ "${run_id:0:1}" == "#" ]] && continue

    if [[ -n "$already_done" ]] && grep -Fqx -- "$orig" <<<"$already_done"; then
      continue
    fi

    seen=$((seen+1))
    emit_event progress phase=restore done=@"$seen" total=@"$total" current_path="$orig"

    if [[ "$dec" == *":deleted" ]]; then
      echo "[unrecoverable] deleted: $orig"
      emit_event action kind=restore_unrecoverable src="$orig" dst="" dry_run=@"$RESTORE_DRY_RUN"
      unrecoverable=$((unrecoverable+1))
      continue
    fi

    if [[ -z "$quar" || ! -e "$quar" ]]; then
      if [[ -z "$quar" ]]; then
        echo "[skip] no quarantine path recorded: $orig"
      else
        echo "[missing] quarantine file gone: $quar"
      fi
      emit_event action kind=restore_missing src="$quar" dst="$orig" dry_run=@"$RESTORE_DRY_RUN"
      missing=$((missing+1))
      continue
    fi

    if [[ -e "$orig" ]]; then
      echo "[conflict] original exists, skipping: $orig"
      emit_event action kind=restore_conflict src="$quar" dst="$orig" dry_run=@"$RESTORE_DRY_RUN"
      skipped_exists=$((skipped_exists+1))
      continue
    fi

    if $RESTORE_DRY_RUN; then
      echo "[DRY] mv \"$quar\" \"$orig\""
      emit_event action kind=restore src="$quar" dst="$orig" dry_run=@true
      restored=$((restored+1))
      continue
    fi

    mkdir -p "$(dirname -- "$orig")" || { errors=$((errors+1)); continue; }
    if mv -- "$quar" "$orig"; then
      emit_event action kind=restore src="$quar" dst="$orig" dry_run=@false
      restored=$((restored+1))
      printf '%s\n' "$orig" >> "$done_marker"
    else
      echo "ERROR: mv failed: $quar -> $orig" >&2
      emit_event error code=mv_failed detail="$quar -> $orig"
      errors=$((errors+1))
    fi
  done < "$mf"

  echo "===== RESTORE SUMMARY ====="
  echo "Restored:        $restored"
  echo "Skipped (exists): $skipped_exists"
  echo "Missing:         $missing"
  echo "Unrecoverable:   $unrecoverable"
  echo "Errors:          $errors"
  echo "==========================="

  emit_event run_end \
    restored=@"$restored" \
    skipped=@"$skipped_exists" \
    missing=@"$missing" \
    unrecoverable=@"$unrecoverable" \
    errors=@"$errors" \
    manifest_path="$mf" \
    cancelled=@false

  if [[ "$errors" -gt 0 ]]; then exit 3; fi
  exit 0
}
```

What changed:
- Added `emit_event run_start` at the top with mode=restore and the manifest as `source`.
- Added a count-pass before the action loop to compute `total` for progress events (cheap; the manifest is tens to thousands of lines).
- Each branch (unrecoverable/missing/conflict/dry-restore/real-restore) emits an `action` event with the appropriate kind and `dry_run` flag.
- Real-restore emits an `error` event when `mv` fails (matches existing scan-path behavior).
- `run_end` carries restore-specific counts.

Note: the `dry_run=@"$RESTORE_DRY_RUN"` substitution works because `$RESTORE_DRY_RUN` is either the literal string `true` or `false` (bash booleans), and the `@` prefix tells `emit_event` to emit the value raw.

- [ ] **Step 4: Run the tests, confirm they pass**

```bash
python3 tests/json_events/run_tests.py 2>&1 | tail -20
```

Expected: ALL tests PASS (previous tests still green; the two new ones pass).

- [ ] **Step 5: Commit**

```bash
git add bin/twincut.sh tests/json_events/run_tests.py
git commit -m "feat: emit NDJSON events from twincut.sh do_restore

Restore path now flows through the same SSE pipeline as scan/apply:
run_start (mode=restore), progress, action (kinds: restore,
restore_conflict, restore_missing, restore_unrecoverable), run_end.
Existing echo lines stay for CLI users.

Two new Python tests cover --restore-dry-run event coverage and
real --restore execution; relaxes run_end REQUIRED_FIELDS so each
mode can carry its own shape.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 2: Go — refactor running/done templates to be workflow-agnostic

The running template currently hard-codes preview vs. apply via an `IsApply` bool. To support restore, we generalize: the handler tells the template what to call this run and where the JS should go after run_end.

**Files:**
- Modify: `ui/server/selfcheck.go` (the `selfCheckRunningData` struct + the two handlers that populate it)
- Modify: `ui/templates/selfcheck_running.html`
- Modify: `ui/templates/selfcheck_done.html`
- Modify: `ui/server/results.go` (`ResultsView` gains `Mode` so the done page knows what verb to use)

This task should leave existing tests green — it's a pure rename/refactor — but we run the suite to confirm no regression.

- [ ] **Step 1: Update `selfCheckRunningData` in `ui/server/selfcheck.go`** — replace the struct (lines 20-26 in the current file) with:

```go
// selfCheckRunningData feeds the running-panel template. Mode controls the
// title ("Previewing…" / "Applying…" / "Restoring…"). NextURL is where the
// JS navigates after the run_end SSE event arrives. ShowActions controls
// whether per-file move log lines appear in the panel (apply/restore yes,
// preview no — preview events would flood the log).
type selfCheckRunningData struct {
	RunID       string
	Folder      string
	Mode        string // "preview" | "apply" | "restore"
	NextURL     string
	ShowActions bool
}
```

- [ ] **Step 2: Update `handleSelfCheckPreview`** — change the `ExecuteTemplate` call near the end of the function (currently passes `IsApply: false`):

```go
if err := s.tmpl.ExecuteTemplate(w, "selfcheck_running.html", selfCheckRunningData{
	RunID:       run.ID,
	Folder:      folder,
	Mode:        "preview",
	NextURL:     "/api/self-check/results/" + run.ID,
	ShowActions: false,
}); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
}
```

- [ ] **Step 3: Update `handleSelfCheckApply`** — the same template call (currently passes `IsApply: true`):

```go
if err := s.tmpl.ExecuteTemplate(w, "selfcheck_running.html", selfCheckRunningData{
	RunID:       run.ID,
	Folder:      folder,
	Mode:        "apply",
	NextURL:     "/api/self-check/done/" + run.ID,
	ShowActions: true,
}); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
}
```

- [ ] **Step 4: Add `Mode` to `ResultsView`** in `ui/server/results.go` — find the struct (lines 11-32) and add `Mode string` near the top (next to `RunID`). Find `BuildResults` and have it set `Mode` from the `run_start.mode` event when it parses it. (`BuildResults` already extracts the mode for other reasons — just propagate it onto the view.)

If `BuildResults` doesn't currently read `run_start.mode`, add the read; the field is in the existing event payload as documented by `tests/json_events/run_tests.py:REQUIRED_FIELDS["run_start"]`.

- [ ] **Step 5: Update `selfcheck_running.html`** — replace lines 2-7:

```html
  <header class="tab-section-header">
    <h2>
      {{- if eq .Mode "apply" -}}Applying…
      {{- else if eq .Mode "restore" -}}Restoring…
      {{- else -}}Previewing…
      {{- end -}}
    </h2>
    <p class="subtitle"><code>{{.Folder}}</code></p>
  </header>

  <div class="run-panel" id="run-panel"
       data-run-id="{{.RunID}}"
       data-next-url="{{.NextURL}}"
       data-show-actions="{{.ShowActions}}">
```

Then in the `<script>` block (lines 43-99), replace the early lines so the JS reads the new data attributes:

```javascript
    const panel = document.getElementById('run-panel');
    const runID = panel.dataset.runId;
    const nextURL = panel.dataset.nextUrl;
    const showActions = panel.dataset.showActions === 'true';
```

And in the `action` event listener, change `if (isApply)` to `if (showActions)`. In the `run_end` listener, replace the `next` constant + ternary with simply `nextURL`:

```javascript
    es.addEventListener('run_end', (e) => {
      const p = JSON.parse(e.data);
      es.close();
      progressFill.style.width = '100%';
      htmx.ajax('GET', nextURL, { target: '#tab-content', swap: 'innerHTML' });
    });
```

Delete the old `isApply` declaration.

- [ ] **Step 6: Update `selfcheck_done.html`** — replace line 2's done-banner block so the heading and verbs switch on `.Mode`. Find the `<strong>Quarantined {{.MovedCount}} file...</strong>` line (~line 17) and gate on Mode:

```html
  {{else}}
    <div class="alert alert-success">
      {{if eq .Mode "restore"}}
        <strong>Restored {{.MovedCount}} file{{if ne .MovedCount 1}}s{{end}}.</strong>
      {{else}}
        <strong>Quarantined {{.MovedCount}} file{{if ne .MovedCount 1}}s{{end}}.</strong>
      {{end}}
      {{if .ManifestPath}}
        <br><span class="muted small">Manifest: <code>{{.ManifestPath}}</code></span>
        {{if ne .Mode "restore"}}
        <br><span class="muted small">Roll back via History or:
          <code>twincut --restore "{{.ManifestPath}}"</code></span>
        {{end}}
      {{end}}
    </div>
  {{end}}
```

The page heading at the top stays "Done" (works for both verbs).

- [ ] **Step 7: Run the existing Go test suite to confirm no regression**

```bash
cd ui && go vet ./... && go test ./...
```

Expected: PASS, same counts as before this task.

- [ ] **Step 8: Run the bash JSON events suite for safety**

```bash
python3 tests/json_events/run_tests.py 2>&1 | tail -5
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add ui/server/selfcheck.go ui/server/results.go ui/templates/selfcheck_running.html ui/templates/selfcheck_done.html
git commit -m "refactor: make running/done templates workflow-agnostic

Replaces selfCheckRunningData.IsApply with a Mode string + NextURL
so the same template can drive preview, apply, and the upcoming
restore flow. ResultsView gains Mode so the done page picks the
right verb ('Restored' vs. 'Quarantined').

No behavioral change; preview and apply still produce the same
running screen and done summary.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 3: Go — `collectHistory` + `resolveManifest` helpers

Pure helpers, no HTTP. These are the ones that need rigorous unit tests because every downstream handler depends on them parsing the run files correctly.

**Files:**
- Create: `ui/server/history.go` (helpers only for this task; handlers come next)
- Create: `ui/server/history_test.go`

- [ ] **Step 1: Write the failing tests** — create `ui/server/history_test.go`:

```go
package server

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func writeNDJSON(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCollectHistory_FiltersAndSorts(t *testing.T) {
	state := t.TempDir()
	runs := filepath.Join(state, "runs")

	// 1. Self-check apply, success, moved=2.
	writeNDJSON(t, filepath.Join(runs, "A.ndjson"),
		`{"type":"run_start","ts":100,"run_id":"A","mode":"self_check_apply","source":"/p/a"}`,
		`{"type":"run_end","ts":110,"run_id":"A","moved":2,"manifest_path":"/p/a/_QUARANTINE/_m.tsv","cancelled":false}`,
	)
	// 2. Self-check preview — must be filtered out.
	writeNDJSON(t, filepath.Join(runs, "B.ndjson"),
		`{"type":"run_start","ts":200,"run_id":"B","mode":"self_check","source":"/p/b"}`,
		`{"type":"run_end","ts":201,"run_id":"B","moved":0,"manifest_path":"","cancelled":false}`,
	)
	// 3. Self-check apply but no moves — filtered out (nothing to restore).
	writeNDJSON(t, filepath.Join(runs, "C.ndjson"),
		`{"type":"run_start","ts":300,"run_id":"C","mode":"self_check_apply","source":"/p/c"}`,
		`{"type":"run_end","ts":301,"run_id":"C","moved":0,"manifest_path":"","cancelled":false}`,
	)
	// 4. Self-check apply, cancelled-partial (moved>0) — keep.
	writeNDJSON(t, filepath.Join(runs, "D.ndjson"),
		`{"type":"run_start","ts":400,"run_id":"D","mode":"self_check_apply","source":"/p/d"}`,
		`{"type":"run_end","ts":410,"run_id":"D","moved":5,"manifest_path":"/p/d/_QUARANTINE/_m.tsv","cancelled":true}`,
	)
	// 5. Apply with no run_end (process killed) — filtered out.
	writeNDJSON(t, filepath.Join(runs, "E.ndjson"),
		`{"type":"run_start","ts":500,"run_id":"E","mode":"self_check_apply","source":"/p/e"}`,
	)

	got, err := collectHistory(state)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].Timestamp > got[j].Timestamp })

	wantIDs := []string{"D", "A"}
	gotIDs := make([]string, len(got))
	for i, e := range got {
		gotIDs[i] = e.RunID
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Errorf("history IDs (desc by ts) = %v; want %v", gotIDs, wantIDs)
	}
	if got[0].Status != "cancelled-partial" {
		t.Errorf("entry D status = %q; want cancelled-partial", got[0].Status)
	}
	if got[1].Status != "success" {
		t.Errorf("entry A status = %q; want success", got[1].Status)
	}
	if got[1].Folder != "/p/a" {
		t.Errorf("entry A folder = %q; want /p/a", got[1].Folder)
	}
	if got[1].MovedCount != 2 {
		t.Errorf("entry A moved = %d; want 2", got[1].MovedCount)
	}
}

func TestCollectHistory_RestoredSidecarDetected(t *testing.T) {
	state := t.TempDir()
	runs := filepath.Join(state, "runs")
	manifest := filepath.Join(state, "scratch", "_manifest-A.tsv")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest+".restored", []byte("/p/a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeNDJSON(t, filepath.Join(runs, "A.ndjson"),
		`{"type":"run_start","ts":100,"run_id":"A","mode":"self_check_apply","source":"/p/a"}`,
		`{"type":"run_end","ts":110,"run_id":"A","moved":1,"manifest_path":"`+manifest+`","cancelled":false}`,
	)
	got, err := collectHistory(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Restored {
		t.Errorf("expected one entry with Restored=true; got %+v", got)
	}
}

func TestCollectHistory_EmptyDir(t *testing.T) {
	got, err := collectHistory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("empty stateDir yielded %d entries; want 0", len(got))
	}
}

func TestResolveManifest_SuccessAndMissing(t *testing.T) {
	state := t.TempDir()
	runs := filepath.Join(state, "runs")
	manifest := filepath.Join(state, "scratch", "_m.tsv")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(manifest, []byte(""), 0o644)
	writeNDJSON(t, filepath.Join(runs, "OK.ndjson"),
		`{"type":"run_start","ts":1,"run_id":"OK","mode":"self_check_apply","source":"/p"}`,
		`{"type":"run_end","ts":2,"run_id":"OK","moved":1,"manifest_path":"`+manifest+`","cancelled":false}`,
	)
	// Run pointing at a manifest file that doesn't exist anymore.
	writeNDJSON(t, filepath.Join(runs, "GONE.ndjson"),
		`{"type":"run_start","ts":3,"run_id":"GONE","mode":"self_check_apply","source":"/p"}`,
		`{"type":"run_end","ts":4,"run_id":"GONE","moved":1,"manifest_path":"/nope/_m.tsv","cancelled":false}`,
	)

	gotPath, err := resolveManifest(state, "OK")
	if err != nil {
		t.Fatalf("resolveManifest OK: %v", err)
	}
	if gotPath != manifest {
		t.Errorf("got %q; want %q", gotPath, manifest)
	}
	if _, err := resolveManifest(state, "GONE"); err == nil {
		t.Errorf("expected error for missing manifest; got nil")
	}
	if _, err := resolveManifest(state, "NO_SUCH_RUN"); err == nil {
		t.Errorf("expected error for unknown run; got nil")
	}
}
```

- [ ] **Step 2: Run, confirm it fails**

```bash
cd ui && go test ./server/ -run "TestCollectHistory|TestResolveManifest" 2>&1 | tail -5
```

Expected: build error (`undefined: collectHistory`, `undefined: resolveManifest`, `undefined: HistoryEntry`).

- [ ] **Step 3: Implement helpers** — create `ui/server/history.go`:

```go
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
	if mode != "self_check_apply" {
		return HistoryEntry{}, false, nil
	}
	moved := jsonInt(end["moved"])
	manifest, _ := end["manifest_path"].(string)
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
```

- [ ] **Step 4: Run, confirm tests pass**

```bash
cd ui && go test ./server/ -run "TestCollectHistory|TestResolveManifest" -v 2>&1 | tail -15
```

Expected: all four tests PASS.

- [ ] **Step 5: Run full suite to confirm no regression**

```bash
cd ui && go vet ./... && go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add ui/server/history.go ui/server/history_test.go
git commit -m "feat: collectHistory + resolveManifest helpers for stage 6

Pure parsers over ~/.twincut-ui/runs/*.ndjson:
- collectHistory(stateDir) returns the list of past self-check
  applies that moved >0 files and produced a manifest, sorted by
  timestamp descending.
- resolveManifest(stateDir, runID) returns the manifest path,
  erroring if the run is unknown or the manifest file has been
  deleted/moved on disk.

Both detect the .restored sidecar twincut.sh writes so the UI can
badge already-restored runs.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 4: Go — History list handler + template + sidebar wiring

**Files:**
- Modify: `ui/server/history.go` (append the `handleHistoryTab` handler)
- Create: `ui/templates/history_list.html`
- Modify: `ui/server/http.go` (swap the placeholder route)
- Modify: `ui/server/history_test.go` (add a handler smoke test)

- [ ] **Step 1: Append the handler to `ui/server/history.go`**:

```go
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
```

You'll also need to add `"net/http"` to the import block at the top of `ui/server/history.go`.

- [ ] **Step 2: Create the template** — `ui/templates/history_list.html`:

```html
<section class="tab-section">
  <header class="tab-section-header">
    <h2>History</h2>
    <p class="subtitle">Past self-check applies — click any row to restore.</p>
  </header>

  {{if not .Entries}}
    <div class="alert alert-info">
      <strong>No history yet.</strong> Run a self-check and apply some moves; this page lists what you can roll back.
    </div>
  {{else}}
    <table class="history-table">
      <thead>
        <tr>
          <th>When</th>
          <th>Folder</th>
          <th>Files</th>
          <th>Status</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        {{range .Entries}}
        <tr class="history-row {{if .Restored}}history-row-restored{{end}}">
          <td><time data-ts="{{.Timestamp}}">{{.Timestamp}}</time></td>
          <td><code>{{.Folder}}</code></td>
          <td>{{.MovedCount}}</td>
          <td>
            {{if .Restored}}<span class="badge badge-info">restored</span>
            {{else if eq .Status "success"}}<span class="badge badge-success">success</span>
            {{else if eq .Status "cancelled-partial"}}<span class="badge badge-warn">partial</span>
            {{else}}<span class="badge badge-error">{{.Status}}</span>{{end}}
          </td>
          <td>
            <a href="#" class="btn btn-secondary btn-small"
               hx-get="/history/{{.RunID}}/preview"
               hx-target="#tab-content"
               hx-swap="innerHTML">
              {{if .Restored}}Inspect{{else}}Restore…{{end}}
            </a>
          </td>
        </tr>
        {{end}}
      </tbody>
    </table>
  {{end}}
</section>

<script>
  // Format timestamps to local time so the table is human-readable.
  document.querySelectorAll('time[data-ts]').forEach(function (el) {
    const ts = parseInt(el.dataset.ts, 10);
    if (!ts) return;
    const d = new Date(ts * 1000);
    el.textContent = d.toLocaleString();
  });
</script>
```

- [ ] **Step 3: Wire the route** — in `ui/server/http.go`, find the placeholder:

```go
mux.HandleFunc("GET /tab/history", s.handleTabPlaceholder("History"))
```

Replace with:

```go
mux.HandleFunc("GET /tab/history", s.handleHistoryTab)
```

- [ ] **Step 4: Add a handler smoke test** — append to `ui/server/history_test.go`:

```go
import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleHistoryTab_RendersEntries(t *testing.T) {
	state := t.TempDir()
	runs := filepath.Join(state, "runs")
	manifest := filepath.Join(state, "_m.tsv")
	os.WriteFile(manifest, []byte(""), 0o644)
	writeNDJSON(t, filepath.Join(runs, "A.ndjson"),
		`{"type":"run_start","ts":100,"run_id":"A","mode":"self_check_apply","source":"/p/a"}`,
		`{"type":"run_end","ts":110,"run_id":"A","moved":3,"manifest_path":"`+manifest+`","cancelled":false}`,
	)
	s, err := New(Options{StateDir: state})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/tab/history", nil)
	w := httptest.NewRecorder()
	s.handleHistoryTab(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d; want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "/p/a") {
		t.Errorf("response missing folder /p/a; body=\n%s", body)
	}
	if !strings.Contains(body, "/history/A/preview") {
		t.Errorf("response missing restore link; body=\n%s", body)
	}
}

func TestHandleHistoryTab_EmptyState(t *testing.T) {
	state := t.TempDir()
	s, err := New(Options{StateDir: state})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/tab/history", nil)
	w := httptest.NewRecorder()
	s.handleHistoryTab(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d; want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "No history yet") {
		t.Errorf("missing empty-state message")
	}
}
```

(Update the existing `import` block at the top of `history_test.go` to include the new imports — they're additive.)

- [ ] **Step 5: Run all tests**

```bash
cd ui && go vet ./... && go test ./server/ -run "History"
```

Expected: PASS (new tests + previous Task 3 tests both green).

- [ ] **Step 6: Commit**

```bash
git add ui/server/history.go ui/server/history_test.go ui/templates/history_list.html ui/server/http.go
git commit -m "feat: History tab lists past self-check applies

Renders a table from collectHistory; rows link to the per-run
preview endpoint that the next commit wires up. Empty-state copy
explains how to populate it. Restored runs render with a badge
and the action becomes 'Inspect' instead of 'Restore'.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 5: Go — Restore preview handler + template

The preview handler spawns `twincut.sh --restore <manifest> --restore-dry-run --json-events`, waits for completion, then renders a summary fragment. It uses the same run manager that scan/apply uses, with the running template as the intermediate page (Mode="restore", NextURL=`/history/{preview_run_id}/preview-results`).

**Files:**
- Modify: `ui/server/history.go`
- Create: `ui/templates/history_preview.html`
- Modify: `ui/server/http.go`

- [ ] **Step 1: Append handlers to `ui/server/history.go`**:

```go
// historyPreviewView is the payload for history_preview.html — the
// post-dry-run summary the user confirms or cancels.
type historyPreviewView struct {
	OriginalRunID  string // for the Confirm POST
	ManifestPath   string
	Folder         string
	WillRestore    int
	WillSkip       int
	WillMiss       int
	WillUnrecover  int
}

// handleHistoryPreview kicks off a dry-run restore. Returns the running
// panel; the JS hands off to /history/{id}/preview-results when run_end
// arrives.
func (s *Server) handleHistoryPreview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	manifest, err := resolveManifest(s.opts.StateDir, id)
	if err != nil {
		http.Error(w, "resolve manifest: "+err.Error(), http.StatusNotFound)
		return
	}
	args := []string{"--restore", manifest, "--restore-dry-run"}
	run, err := s.runs.Start(StartOptions{Mode: "restore_preview", Args: args})
	if err != nil {
		http.Error(w, "start: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.tmpl.ExecuteTemplate(w, "selfcheck_running.html", selfCheckRunningData{
		RunID:       run.ID,
		Folder:      manifest,
		Mode:        "restore",
		NextURL:     "/history/" + run.ID + "/preview-results?orig=" + id,
		ShowActions: true,
	})
}

// handleHistoryPreviewResults renders the summary of a completed dry-run
// restore. The "orig" query param threads the original Apply's run_id
// through so the Confirm button can POST to /history/{orig}/apply.
func (s *Server) handleHistoryPreviewResults(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	origID := r.URL.Query().Get("orig")
	run := s.runs.Get(id)
	if run == nil {
		http.Error(w, "unknown run", http.StatusNotFound)
		return
	}
	view := buildRestorePreview(run, origID)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "history_preview.html", view); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// buildRestorePreview tallies action events by kind from a finished
// restore-dry-run.
func buildRestorePreview(run *Run, origID string) historyPreviewView {
	view := historyPreviewView{OriginalRunID: origID}
	for _, raw := range run.Events() {
		var ev map[string]any
		if err := json.Unmarshal(raw, &ev); err != nil {
			continue
		}
		switch ev["type"] {
		case "run_start":
			if src, ok := ev["source"].(string); ok {
				view.ManifestPath = src
				view.Folder = filepath.Dir(filepath.Dir(src))
			}
		case "action":
			switch ev["kind"] {
			case "restore":
				view.WillRestore++
			case "restore_conflict":
				view.WillSkip++
			case "restore_missing":
				view.WillMiss++
			case "restore_unrecoverable":
				view.WillUnrecover++
			}
		}
	}
	return view
}
```

If `Run.Events()` doesn't exist with that name, look at how `BuildResults` (in `ui/server/results.go`) currently iterates over the run's events and use the same accessor pattern. The point is to walk the buffered ndjson events; the run-manager already exposes them.

- [ ] **Step 2: Create the preview template** — `ui/templates/history_preview.html`:

```html
<section class="tab-section">
  <header class="tab-section-header">
    <h2>Restore preview</h2>
    <p class="subtitle"><code>{{.ManifestPath}}</code></p>
  </header>

  <div class="summary-cards">
    <div class="summary-card">
      <div class="summary-value">{{.WillRestore}}</div>
      <div class="summary-label">file{{if ne .WillRestore 1}}s{{end}} ready to restore</div>
    </div>
    {{if .WillSkip}}
    <div class="summary-card summary-card-warn">
      <div class="summary-value">{{.WillSkip}}</div>
      <div class="summary-label">target already exists</div>
    </div>
    {{end}}
    {{if .WillMiss}}
    <div class="summary-card summary-card-warn">
      <div class="summary-value">{{.WillMiss}}</div>
      <div class="summary-label">quarantine file gone</div>
    </div>
    {{end}}
    {{if .WillUnrecover}}
    <div class="summary-card summary-card-warn">
      <div class="summary-value">{{.WillUnrecover}}</div>
      <div class="summary-label">unrecoverable (was deleted)</div>
    </div>
    {{end}}
  </div>

  {{if .WillRestore}}
  <form hx-post="/history/{{.OriginalRunID}}/apply"
        hx-target="#tab-content"
        hx-swap="innerHTML">
    <div class="form-actions">
      <button type="submit" class="btn btn-primary">Confirm restore ({{.WillRestore}} file{{if ne .WillRestore 1}}s{{end}})</button>
      <a href="#" class="btn btn-secondary" hx-get="/tab/history" hx-target="#tab-content">Cancel</a>
    </div>
  </form>
  {{else}}
  <div class="alert alert-info">
    <strong>Nothing to restore.</strong>
    Every file in this manifest is either already in place or unrecoverable.
  </div>
  <div class="form-actions">
    <a href="#" class="btn btn-secondary" hx-get="/tab/history" hx-target="#tab-content">Back to history</a>
  </div>
  {{end}}
</section>
```

- [ ] **Step 3: Wire the routes** — in `ui/server/http.go`, add after the existing history line:

```go
mux.HandleFunc("GET /history/{id}/preview", s.handleHistoryPreview)
mux.HandleFunc("GET /history/{id}/preview-results", s.handleHistoryPreviewResults)
```

- [ ] **Step 4: Add a test for `buildRestorePreview`** — append to `ui/server/history_test.go`:

```go
func TestBuildRestorePreview_TalliesByKind(t *testing.T) {
	// Use the helper that other tests in this package use to fake a Run with
	// pre-captured events. If there isn't one already, fall back to the same
	// approach used in results_test.go.
	events := []string{
		`{"type":"run_start","ts":1,"mode":"restore","source":"/p/_QUARANTINE/_m.tsv"}`,
		`{"type":"action","kind":"restore","src":"/q/a","dst":"/p/a","dry_run":true}`,
		`{"type":"action","kind":"restore","src":"/q/b","dst":"/p/b","dry_run":true}`,
		`{"type":"action","kind":"restore_conflict","src":"/q/c","dst":"/p/c","dry_run":true}`,
		`{"type":"action","kind":"restore_missing","src":"/q/d","dst":"/p/d","dry_run":true}`,
		`{"type":"action","kind":"restore_unrecoverable","src":"/p/e","dst":"","dry_run":true}`,
		`{"type":"run_end","ts":2,"restored":2,"skipped":1,"missing":1,"unrecoverable":1,"cancelled":false}`,
	}
	run := newRunForTest(t, events) // helper from existing results_test.go pattern
	view := buildRestorePreview(run, "ORIG1")
	if view.OriginalRunID != "ORIG1" {
		t.Errorf("OriginalRunID = %q; want ORIG1", view.OriginalRunID)
	}
	if view.WillRestore != 2 || view.WillSkip != 1 || view.WillMiss != 1 || view.WillUnrecover != 1 {
		t.Errorf("tally mismatch: %+v", view)
	}
	if view.ManifestPath != "/p/_QUARANTINE/_m.tsv" {
		t.Errorf("manifest = %q", view.ManifestPath)
	}
}
```

If `newRunForTest` doesn't exist, look at how `results_test.go` synthesizes a Run for testing and replicate (or factor that helper out into `runs_test.go` for sharing). Use whatever pattern is already present rather than inventing a new one.

- [ ] **Step 5: Run tests**

```bash
cd ui && go vet ./... && go test ./server/ -run "History|RestorePreview"
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add ui/server/history.go ui/server/history_test.go ui/templates/history_preview.html ui/server/http.go
git commit -m "feat: History 'Restore preview' page

Click any history row → server spawns twincut --restore-dry-run
--json-events, the running panel streams progress, then the
results fragment summarizes (will-restore / will-skip / will-miss
/ unrecoverable) with a Confirm button that POSTs to the apply
endpoint added in the next commit.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 6: Go — Restore apply + done handlers

The apply handler spawns the real restore (no dry-run flag). The done handler reuses `selfcheck_done.html` with Mode="restore" so the page reads "Restored N files".

**Files:**
- Modify: `ui/server/history.go`
- Modify: `ui/server/http.go`
- Modify: `ui/server/results.go` (`BuildResults` already populates a `Mode` from run_start; make sure restore runs map to MovedCount = restored count for the done page)

- [ ] **Step 1: Append handlers to `ui/server/history.go`**:

```go
// handleHistoryApply spawns the real (non-dry-run) restore for the
// manifest of past run id. Returns the running panel.
func (s *Server) handleHistoryApply(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	manifest, err := resolveManifest(s.opts.StateDir, id)
	if err != nil {
		http.Error(w, "resolve manifest: "+err.Error(), http.StatusNotFound)
		return
	}
	args := []string{"--restore", manifest}
	run, err := s.runs.Start(StartOptions{Mode: "restore_apply", Args: args})
	if err != nil {
		http.Error(w, "start: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.tmpl.ExecuteTemplate(w, "selfcheck_running.html", selfCheckRunningData{
		RunID:       run.ID,
		Folder:      manifest,
		Mode:        "restore",
		NextURL:     "/history/" + run.ID + "/done",
		ShowActions: true,
	})
}

// handleHistoryDone renders the restore-completion summary. Reuses the
// existing selfcheck_done.html template — the Mode field on ResultsView
// switches the verbs.
func (s *Server) handleHistoryDone(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run := s.runs.Get(id)
	if run == nil {
		http.Error(w, "unknown run", http.StatusNotFound)
		return
	}
	view, err := BuildResults(run)
	if err != nil {
		http.Error(w, "build results: "+err.Error(), http.StatusInternalServerError)
		return
	}
	view.Mode = "restore"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "selfcheck_done.html", view); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
```

- [ ] **Step 2: Teach `BuildResults` about restore run_end** — open `ui/server/results.go` and find the run_end handler. Currently it reads `moved` for `MovedCount`. Restore runs use `restored` instead. Make it fall back:

```go
// Inside the run_end branch of BuildResults' switch on event type:
if v, ok := ev["moved"].(float64); ok {
    view.MovedCount = int(v)
} else if v, ok := ev["restored"].(float64); ok {
    // Restore runs report counts under a different name.
    view.MovedCount = int(v)
}
```

(Find the exact existing line and adapt — the principle is "fall through to `restored` when `moved` isn't present.")

- [ ] **Step 3: Wire the routes** — in `ui/server/http.go`:

```go
mux.HandleFunc("POST /history/{id}/apply", s.handleHistoryApply)
mux.HandleFunc("GET /history/{id}/done", s.handleHistoryDone)
```

- [ ] **Step 4: Run tests**

```bash
cd ui && go vet ./... && go test ./...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add ui/server/history.go ui/server/results.go ui/server/http.go
git commit -m "feat: History restore execution + done page

Confirm on the preview page → POST /history/{id}/apply → spawns
twincut --restore <manifest>. Reuses the running panel; done
page reuses selfcheck_done.html with Mode=restore so it reads
'Restored N files'. BuildResults falls back to run_end.restored
when run_end.moved is absent.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 7: End-to-end smoke test in the browser

This task isn't TDD-style — it's manual verification that the full self-check → apply → history → restore loop closes.

- [ ] **Step 1: Build the binary**

```bash
cd ui && go build -o /tmp/twincut-ui .
```

- [ ] **Step 2: Seed a clean scratch folder**

```bash
SCRATCH=/tmp/twincut-stage6-smoke
rm -rf "$SCRATCH" && mkdir -p "$SCRATCH"
# Two identical files so the apply has something to quarantine
printf 'duplicate-bytes' > "$SCRATCH/a.jpg"
cp "$SCRATCH/a.jpg" "$SCRATCH/b.jpg"
```

- [ ] **Step 3: Launch the server**

```bash
TWINCUT_UI_NO_BROWSER=1 /tmp/twincut-ui &
sleep 1
```

- [ ] **Step 4: Open http://127.0.0.1:7681 in your browser. Click through:**
  - Self-check tab → folder=$SCRATCH → Preview
  - Wait for results, hit Apply
  - On Done page, verify "Quarantined 1 file" and the "Open quarantine folder" button works
  - Switch to History tab → confirm the run shows up with badge "success", 1 file
  - Click "Restore…" → confirm preview page shows "1 file ready to restore"
  - Hit "Confirm restore" → verify Done page reads "Restored 1 file"
  - Switch back to History → confirm the row now badges "restored"
  - On disk, confirm `$SCRATCH/b.jpg` is back

- [ ] **Step 5: Stop the server and clean up**

```bash
pkill -f "/tmp/twincut-ui"
rm -rf "$SCRATCH"
```

- [ ] **Step 6: Send the diff to reviewer-gemini for a third-party pass**

```
Agent({
  subagent_type: "reviewer-gemini",
  prompt: "Review the stage 6 changes (4 commits ahead of main). Focus areas:
  (1) twincut.sh do_restore — did the new emit_event calls preserve every CLI-visible behavior? Is the dry_run=@$RESTORE_DRY_RUN trick safe (RESTORE_DRY_RUN is a bash bool string)?
  (2) Go: collectHistory + resolveManifest correctness, especially handling of malformed ndjson lines.
  (3) The Mode/NextURL refactor on selfcheck_running.html — does any code path still rely on the old IsApply bool?
  (4) Cross-cutting: do the new routes have allowlist concerns? (manifest paths come from user-controlled NDJSON files.)
  Report critical/important/nit with file:line refs, under 300 words."
})
```

- [ ] **Step 7: Address findings, run tests again, then push & open PR**

```bash
cd ui && go test ./... && python3 ../tests/json_events/run_tests.py
git push -u origin feature/stage-6-history
gh pr create --title "Stage 6: History tab + Restore" --body "$(cat <<'EOF'
## Summary

- Closes the self-check loop: every UI-originated Apply shows up in a new History tab. Click a row → live `--restore-dry-run` preview → confirm → roll back inside the browser.
- `do_restore` in `twincut.sh` now emits NDJSON events so the same SSE pipeline drives it.
- Running/done templates refactored to a Mode string + NextURL — preview/apply/restore all share them.

## Test plan

- [x] 2 new Python tests for restore NDJSON events
- [x] 7 new Go tests (collectHistory × 3, resolveManifest, handleHistoryTab × 2, buildRestorePreview)
- [x] Live smoke: self-check apply → History → restore round-trips
- [ ] Reviewer: confirm restored files land back at their original paths

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Self-Review Notes

Spec coverage check:
- ✅ History list (Task 4)
- ✅ Click-row → restore preview (Task 5)
- ✅ Confirm → execute (Task 6)
- ✅ Running/done pages reused (Tasks 2 + 6)
- ✅ Bash: emit_event in do_restore (Task 1)
- ✅ All 4 new files + all listed modified files have explicit tasks
- ✅ Out-of-scope items (CLI manifest discovery, search/filter, history deletion) stay out of scope

Type consistency check:
- `HistoryEntry` defined Task 3, used in Tasks 4-6 with consistent field names
- `selfCheckRunningData` Mode/NextURL/ShowActions added Task 2, used Tasks 5+6
- `historyPreviewView` Task 5 only
- `BuildResults` Mode handling: Task 2 propagates from run_start; Task 6 falls back to run_end.restored

Things the implementer might trip on:
- Task 5 references `Run.Events()` and `newRunForTest` without verifying they exist — both are placeholders for "use the existing pattern from results.go / results_test.go". The implementer should grep first; don't invent new APIs.
- Task 1's `dry_run=@"$RESTORE_DRY_RUN"` relies on RESTORE_DRY_RUN being the literal string `true` or `false` (bash bools). This is the same convention used throughout twincut.sh — verify by reading the existing scan-path emit_event call sites.
