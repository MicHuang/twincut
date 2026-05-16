# Stage 7 — Cross-check Web UI

Status: design approved 2026-05-16, awaiting implementation plan.

## Context

After stage 6 (History tab + Restore, PR #5 merged), the self-check loop is fully closed in the browser. The cross-check tab in the left sidebar is still a placeholder route (`http.go:69 → handleTabPlaceholder("Cross-check")`). Stage 7 makes that tab functional with the same four-state flow self-check uses: Form → Running → Results → Done, with per-cluster keep/quarantine override and History/Restore integration.

This is light-lift relative to stage 6 because most pipeline pieces already exist:
- `bin/twincut.sh` emits NDJSON events for cross-check (run_start mode=cross_check, dup_group, action, progress, run_end) already — no instrumentation work needed.
- `ui/server/results.go` already decodes cross-check `dup_group` shape (existing test `TestBuildResults_CrossCheckShape`).
- `ui/server/runs.go`, `sse.go`, `events.go`, `recents.go`, `fs.go`, `history.go` are generic and reusable as-is.
- The `--apply-list` short-circuit at `twincut.sh:1003-1009` is already mode-agnostic — the only bash change is routing cross-check rows to the right subdir.

## Goals

- Replace the cross-check placeholder with a working tab.
- Source folder + one-or-more backup folders, with a dynamic add/remove backup picker.
- Asymmetric cluster cards: source rows have a keep/quarantine checkbox, backup rows are read-only "[BACKUP · keep]".
- Apply via the existing `--apply-list` flow (extended to recognize cross-check reasons).
- Cross-check apply runs enter History and are restorable through the existing stage-6 flow.

## Non-goals (v1)

- Saved "setup" recents that remember a source + backups combo as one entry. Per-path recents only.
- Cross-check thumbnails for similar-video matches (`--video-fast-strict` mode). Defer until users ask.
- Path overlap locking across concurrent runs. Self-check doesn't have this either; introducing a lock manager is its own project.
- Multi-backup phase-by-phase progress detail in the running view (bash emits phase events but UI keeps a single bar in v1).
- i18n on the new templates (stage 8).
- Detecting duplicates _between_ backup folders (that's self-check territory).

## Architecture

### Bash side: extend `--apply-list` to cross-check

`bin/twincut.sh` change in `process_apply_list` (line ~324). The function is already called regardless of mode (the short-circuit at line 1009 is unconditional on `$APPLY_LIST` being set). The only thing wrong with it today for cross-check is the subdir routing: it routes every move into `_self_dupes/` or `_similar_video_source/`, but cross-check's scan mode (line 1307) routes source-side dupes directly into `$QUAR_DIR` with no subdir.

Add a `case` arm for cross-check reasons:

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

Bash 3.2 compat: just an extra case arm, no new syntax. See `project_twincut_bash_compat.md`.

The manifest format (`init_manifest` is called inside `process_apply_list`) and the run_end event shape are unchanged. Cross-check apply manifests are byte-compatible with self-check apply manifests, so the existing `do_restore` and History/Restore code paths work without modification.

### Go side: `ui/server/crosscheck.go` (new)

Mirrors `ui/server/selfcheck.go` shape. Handlers:

- `GET /tab/cross-check` — render the form template. Replaces the `handleTabPlaceholder` registration at `http.go:69`.
- `POST /cross-check/preview` — parse form (source + multi-value `backup[]`), validate via `IsAllowedPath`, spawn `twincut.sh --source X --backup Y [--backup Z ...] --dry-run --json-events` (plus any advanced flags). Returns the workflow-agnostic `selfcheck_running.html` with `Mode: "cross_check_preview"`.
- `POST /cross-check/apply` — load the preview run's `ResultGroup`s, run them through `composeApplyList(..., Mode: "cross_check")` to produce TSV rows, write the TSV, spawn `twincut.sh --source X --backup Y --quarantine <source>/_QUARANTINE --apply-list <tsv> --assume-yes --json-events`. The `--source`/`--backup` flags are passed for context but the `--apply-list` short-circuit skips re-scanning. Returns `selfcheck_running.html` with `Mode: "cross_check_apply"`.
- `GET /cross-check/{run_id}/results` — render results via the existing `BuildResults` + a shared (or cross-check-specific) results template (see "Templates" below).
- `GET /cross-check/{run_id}/done` — render `selfcheck_done.html` with `Mode: "cross_check"`.
- `POST /cross-check/add-backup` (HTMX endpoint) — returns a single backup-row HTML fragment that the form swaps into the backup list. Lets the user add more rows without page reload.

Form parsing: `r.Form["backup"]` is `[]string` because multiple `<input name="backup">` rows submit as multi-value. Empty values are filtered out; the resulting slice must have length ≥1 or the request fails with 422.

### Shared apply-list helper: `ui/server/apply_list.go` (extracted)

Move `composeApplyList` and `writeApplyList` out of `selfcheck.go` into a new shared file. Both functions gain a `mode string` parameter (`"self_check"` or `"cross_check"`) that controls the `reason` column:

- Self-check: `md5` (hash matches), `video_fast` / `video_strict` (similarity matches).
- Cross-check: `cross_hash` (hash matches), `cross_video_fast` / `cross_video_strict` (similarity matches in strict mode).

The TSV format and `writeApplyList`'s file location (`<stateDir>/applylists/`) are unchanged. Two callers (`selfcheck.go` and `crosscheck.go`) each pass their own mode.

### Results decoding: group-level mode

Cross-check's `dup_group` event (`bin/twincut.sh:1289`) uses `keep_path` for the backup match and `remove_path` for the source file. `decodeGroup` already populates `ResultGroup.Keep` and `ResultGroup.Remove[]` from these fields — so the role is **structural**, encoded by which slot a file occupies, not by a per-file flag.

To let templates pick the right rendering branch (cross-check role badges vs. self-check per-row override), add a `Mode string` field to `ResultGroup`:

```go
type ResultGroup struct {
    GroupID     int
    MatchReason string
    Hash        string
    Mode        string  // "self_check" | "cross_check" — set by BuildResults from run.Mode
    Keep        ResultFile
    Remove      []ResultFile
    // ...existing fields
}
```

`BuildResults` (in `results.go`) gains a `mode` parameter (or reads `run.Mode` from the run snapshot) and stamps every group's `Mode` field. Templates branch on `{{if eq .Mode "cross_check"}} role-badge rendering {{else}} existing per-row override rendering {{end}}`.

No `ResultFile` change. No `decodeGroup` change.

### Templates

| Template | Treatment |
|----------|-----------|
| `templates/crosscheck_form.html` | **New.** Source folder picker + dynamic backup-row list with `+ Add backup` (HTMX-driven) + Advanced (Matching mode select, min size, ext, custom quarantine). |
| `templates/selfcheck_running.html` | **Reuse.** Stage 6 generalized this to `Mode + NextURL + ShowActions`. Add cases for `Mode == "cross_check_preview"` / `"cross_check_apply"` to drive the title text. |
| `templates/selfcheck_results.html` | **Reuse + small change.** Cluster card branches on `.Mode`:<br/>• `"cross_check"` → `.Keep` renders as `[BACKUP · keep]` (read-only), each `.Remove[i]` renders as `[SOURCE]` with quarantine checkbox (default checked = will quarantine)<br/>• `"self_check"` (or empty) → existing rendering, unchanged |
| `templates/selfcheck_done.html` | **Reuse.** Same Mode field; cross-check uses `"Quarantined N files"` wording (identical to self-check). |

### History/Restore integration

`ui/server/history.go` — one-line filter relaxation in `collectHistory`:

```go
// before
if entry.Mode != "self_check" { continue }
// after
if entry.Mode != "self_check" && entry.Mode != "cross_check" { continue }
```

The dry-run-false and `manifest_path != ""` filters stay. The history row template gains a small mode badge column ("Self-check" / "Cross-check") so the user can tell which type of run produced a row.

Restore preview/apply: **zero changes.** `do_restore` reads the manifest TSV format, which is identical between self-check and cross-check apply outputs. The existing `/history/{id}/preview`, `/history/{id}/apply`, `/history/{id}/done` routes all work as-is for cross-check apply runs.

### Form layout

```
┌─ Source folder ─────────────────────────────────┐
│ [/Users/me/Pictures/2024            ] [Browse]  │
└─────────────────────────────────────────────────┘

┌─ Backup folders ────────────────────────────────┐
│ [/Volumes/backup-1                  ] [×]       │
│ [/Volumes/backup-2                  ] [×]       │
│ [+ Add backup]                                  │
└─────────────────────────────────────────────────┘

▸ Advanced options
  · Matching mode:  ( ) Default (video-fast)
                    (•) Hash-only (--exact)         ← form default
                    ( ) Strict (--video-fast-strict)
  · Min size (KB):  [          ]  (blank = default)
  · Extensions:     [          ]  (blank = default)
  · Custom quarantine dir: [          ]  (blank = <source>/_QUARANTINE)

         [ Preview ]
```

Notes:

- **Multi-backup picker:** Dynamic row list. Start with 1 row. `+ Add backup` is an HTMX trigger calling `POST /cross-check/add-backup` which returns a single-row HTML fragment that gets swapped into the list container. Each row has an `×` remove button that drops the row client-side (form-only, no server roundtrip). At least 1 non-empty backup row is required; submit-side validation rejects empty.
- **Matching mode default:** Hash-only (`--exact`). Cross-check is fundamentally a "I know I have this file in a backup somewhere" check; hash precision is what users expect. The CLI's default (video-fast) stays available as a select option. This is an opinionated UI default, same pattern as the self-check "Similar-video Auto" tri-state from PR #4.
- **Recents:** Source and each backup path go into the existing per-path recents list independently. Clicking a recent fills the source field if empty, otherwise the first empty backup row. No tuple memory for v1.

### Run flow

```
[ CROSS-CHECK FORM ]
  source: ~/Pictures/2024
  backup: /Volumes/bk1
          /Volumes/bk2
  matching: hash-only
              │ [Preview]
              ▼
[ PREVIEW RUNNING ]    ← selfcheck_running.html, Mode=cross_check_preview
  (SSE-driven progress: hash backup → hash source → compare)
              ▼ (run_end)
[ RESULTS ]            ← selfcheck_results.html with role badges
  ┌────────────────────────────────────────────────────────┐
  │ Cluster 1                                              │
  │ [SOURCE]  ~/Pictures/2024/IMG_1234.jpg  [☑ quarantine] │
  │ [BACKUP·keep]  /Volumes/bk1/IMG_1234.jpg               │
  └────────────────────────────────────────────────────────┘
  ┌────────────────────────────────────────────────────────┐
  │ Cluster 2                                              │
  │ [SOURCE]  ~/Pictures/2024/IMG_1235.jpg  [☐ quarantine] │
  │ [BACKUP·keep]  /Volumes/bk1/IMG_1235.jpg               │
  │ [BACKUP·keep]  /Volumes/bk2/IMG_1235.jpg               │
  └────────────────────────────────────────────────────────┘
              │ [Apply]
              ▼
[ APPLY RUNNING ]      ← selfcheck_running.html, Mode=cross_check_apply
              ▼
[ DONE ]               ← selfcheck_done.html, "Quarantined N files"
                       [Open _QUARANTINE]  [Back to Cross-check]
              │
              ▼ (also visible in History tab)
[ HISTORY ]            ← stage-6 History, mode badge = "Cross-check"
              │ Restore link
              ▼ (existing stage-6 restore flow)
```

## Testing

Bash side:
- New `tests/json_events/` case: cross-check + `--apply-list` short-circuit. Seed a TSV with `cross_hash` rows, run `twincut.sh --source SRC --backup BK --quarantine QD --apply-list FOO.tsv --json-events`, assert: `run_start mode=cross_check`, per-row `action` event, `run_end manifest_path=...`, files land in `$QUAR_DIR/` directly (not in a self-check subdir).
- Regression check: existing self-check `--apply-list` test still passes — rows with `reason=md5`/`video_*` still route to `_self_dupes/` / `_similar_video_source/`.

Go side:
- `parseCrossCheckForm` — unit tests for: source required, ≥1 backup required, empty-value filtering, allowlist rejection.
- `composeApplyList` — unit tests for both `mode="self_check"` (reason values unchanged) and `mode="cross_check"` (reason becomes `cross_hash` / `cross_video_*`).
- `BuildResults` — unit test that `ResultGroup.Mode` is correctly stamped from the run's mode (cross-check run → `"cross_check"`, self-check run → `"self_check"`).
- `collectHistory` — unit test that cross-check apply runs (mode=cross_check, dry_run=false, manifest_path non-empty) appear in the history list.
- Handler HTTP smoke: `GET /tab/cross-check` returns 200 with form markup including source/backup fields and `+ Add backup` button; `POST /cross-check/preview` with empty source returns 422.

UI smoke (manual):
- Run a real cross-check via the form: pick source + 2 backups → preview → results show mixed source/backup rows → uncheck one source → apply → quarantine actually receives the checked source files → History tab shows the run with "Cross-check" badge → Restore via history → files come back.

## Out of scope (deferred)

- **Saved "setup" recents** — tuple `{source, backups[]}` memory for one-click re-run. Defer until users ask.
- **Cross-check similar-video thumbnails** — `--video-fast-strict` mode produces similarity matches that could render thumbnails (per stage 5), but the existing thumbnail trigger logic is keyed on `match_reason != "md5"` and the cross-check reason `cross_video_*` would need to be added to the template branch. Defer; uncommon usage.
- **Path overlap locking** — not present for self-check either; introducing a lock manager is its own cross-cutting project.
- **Multi-backup progress phase detail** — bash emits `progress phase=...` events but UI keeps a single bar in v1; phase-aware progress is polish.
- **Detecting duplicates between backup folders** — that's self-check territory. Cross-check focuses on source-vs-backup.
- **i18n** — stage 8.

## Task decomposition preview

| # | Task | Size |
|---|------|------|
| 1 | Bash: extend `process_apply_list` `case "$_reason"` arm for cross-check + new JSON events test | small |
| 2 | Go: extract `composeApplyList`/`writeApplyList` to `apply_list.go` + add `mode` param + unit tests | small |
| 3 | Go: `results.go` add `ResultGroup.Mode` + stamp in `BuildResults` from run mode + unit test | small |
| 4 | Go: `history.go` relax filter to `mode in {self_check, cross_check}` + history row mode badge + test | small |
| 5 | Go + template: `crosscheck.go` handlers + `crosscheck_form.html` (with HTMX add-backup-row fragment) | medium |
| 6 | Template: `selfcheck_results.html` role badge branches + hide checkbox for backup rows | small |
| 7 | Replace `http.go:69` placeholder route → `handleTabCrossCheck`, wire all 5 cross-check routes | small |
| 8 | E2E smoke + Gemini review + PR | medium |

## Open questions

None currently. The decisions captured:

- v1 scope: full feature parity with self-check (form → running → results-with-override → done → history/restore). (user's pick)
- Apply mechanism: extend `--apply-list` to cross-check (bash change accepted; uniform code path; restore reuse). (user's pick)
- Cluster card layout: reuse self-check card + role badges (`[SOURCE]` / `[BACKUP · keep]`). (user's pick)
- Form default Matching mode: Hash-only (`--exact`) — opinionated UI default, safer than the CLI's video-fast default for cross-check intent.
- Multi-backup picker: dynamic row list, `+ Add backup`, `×` per row, ≥1 required.
- Recents: per-path independent (no tuple memory).
- Path locking: deferred (self-check doesn't have it either).
