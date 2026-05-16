# Stage 6 — History tab + Restore

Status: design approved 2026-05-16, awaiting implementation plan.

## Context

After stages 1–5.5 + the sim-video auto-default polish (PR #4), self-check is fully usable end-to-end in the Web UI. The remaining gap: after an Apply moves files to quarantine, the only path to roll back is the CLI command `twincut --restore <manifest.tsv>`. The done page surfaces the manifest path as copy-pasteable text but the action itself is not clickable. Stage 6 closes that loop entirely inside the browser.

This is the natural launching pad after the headline scan→preview→apply→done flow lands; restore is the symmetric completion of that loop.

## Goals

- Show past Apply runs in a History tab with timestamp / folder / count / status.
- Click a row → live dry-run preview of what restore would do (which files still recoverable, which conflicts exist).
- Confirm → execute the real restore with live progress (SSE), then a done summary.
- Reuse all existing infrastructure (run manager, SSE, running/done templates, BuildResults).

## Non-goals (v1)

- Discovering CLI-originated manifests by walking quarantine roots. UI lists only `~/.twincut-ui/runs/*.ndjson` records.
- Re-rendering the original Apply's cluster cards. The manifest TSV is the authoritative record of what was moved; previews can diverge if the user unchecked rows.
- Search, filter, paginate, delete-from-history. Defer until the run volume warrants it.
- Restore for cross-check (stage 7 territory, but the same data path will work once cross-check apply lands).

## Architecture

### Bash side: emit JSON events from `do_restore`

`bin/twincut.sh` change. `do_restore` (line ~696) currently uses plain `echo` — we add `emit_event` calls so the Go run-manager + SSE pipeline can consume restore identically to apply.

New events:
- `run_start` — `mode=restore`, `source=<manifest_path>` (the manifest stands in for "what scope this run covers")
- `progress` — `phase=restore`, `done`, `total` (manifest row count, deduped against `.restored` sidecar)
- `action` — `kind=restore | restore_conflict | restore_missing | restore_unrecoverable`, `src=<quarantine_path>`, `dst=<original_path>`, `dry_run=@true/false`. Mapping to existing do_restore branches:
  - `restore` = successful move quarantine→original (counter: `restored`)
  - `restore_conflict` = `[conflict] original exists, skipping` (counter: `skipped_exists`)
  - `restore_missing` = `[missing] quarantine file gone` OR `[skip] no quarantine path recorded` (counter: `missing`)
  - `restore_unrecoverable` = `[unrecoverable] deleted` (counter: `unrecoverable`)
  - Existing `error` events already cover mv failures, no new kind needed
- `run_end` — counts: `restored`, `skipped`, `missing`, `unrecoverable`, `errors`; `cancelled` flag

Constraint from the user: must not regress existing behavior. The existing echo lines stay (routed to stderr under `--json-events`, matching how the scan path handles them already). Added `emit_event` calls are gated by `$JSON_EVENTS` like every other emitter.

### Go side: `ui/server/history.go` (new)

Endpoints:
- `GET /tab/history` — read `<stateDir>/runs/*.ndjson`, parse run_start + run_end of each. Filter: `mode in {self_check_apply}`. Sort: ts desc. Render list.
- `GET /history/{run_id}/preview` — load the original run, extract `manifest_path` from its run_end. Spawn `twincut.sh --restore <manifest> --restore-dry-run --json-events`. Stream the new restore run to its own SSE topic. When dry-run completes, swap to a Restore Preview fragment that summarizes the counts and lists the files.
- `POST /history/{run_id}/apply` — spawn `twincut.sh --restore <manifest> --json-events --assume-yes`. Return the existing `selfcheck_running.html` template (with a `Mode: "restore"` flag controlling the title text).
- `GET /history/done/{restore_run_id}` — done page; reuses `selfcheck_done.html` with a restore-mode title.

Helper to extract:
- `func collectHistory(stateDir string) ([]HistoryEntry, error)` — pure function over `runs/*.ndjson`, easy to unit-test.
- `func resolveManifest(run *Run) (string, error)` — pulls manifest_path from run_end; returns error if missing or file gone.

### Run filtering rules

A run shows up in History when ALL of:
- `run_start.mode == "self_check_apply"`
- run has a `run_end` event (without it, we can't tell what happened)
- `run_end.manifest_path != ""`

Status badge derived from run_end:
- `cancelled == true && moved > 0` → `cancelled-partial` (restorable, partial)
- `cancelled == true && moved == 0` → filtered out (nothing happened)
- `errors > 0` → `failed-partial` (still restorable if any files moved)
- otherwise → `success`

A "restored" check: if `<manifest>.restored` sidecar exists (twincut writes this), badge the row "restored" and disable Restore (or change label to "Restore again").

### UI flow

```
[ HISTORY LIST ]
  ┌──────────────────────────────────────────────────────────────┐
  │ 2026-05-16 14:32 · ~/Pictures/2024  · 142 files · success    │
  │ 2026-05-16 11:08 · ~/Movies         · 3 files   · restored   │
  │ 2026-05-15 22:45 · /Volumes/photos  · 27 files  · cancelled  │
  └──────────────────────────────────────────────────────────────┘
              │ click row
              ▼
[ RESTORE PREVIEW ]
  ┌─ Running --restore-dry-run … ────────────────────────────────┐
  │ (SSE-driven progress, same look as Apply preview)            │
  └──────────────────────────────────────────────────────────────┘
       ▼ (preview finishes)
  ┌──────────────────────────────────────────────────────────────┐
  │ Will restore:    138 files                                   │
  │ Skipped (target exists):  3                                  │
  │ Missing (quarantine gone): 1                                 │
  │ Unrecoverable (deleted):   0                                 │
  │ [Show full list ▾]                                           │
  │ [Confirm restore]  [Cancel]                                  │
  └──────────────────────────────────────────────────────────────┘
              │ confirm
              ▼
[ RESTORE RUNNING ]  ← reuses selfcheck_running.html
              ▼
[ RESTORE DONE ]      "Restored 138 files. 4 skipped."
                      [Back to History]  [Back to Self-check]
```

## Templates

- `templates/history_list.html` (new) — the list view
- `templates/history_preview.html` (new) — the dry-run summary + Confirm/Cancel
- `templates/selfcheck_running.html` — replace the existing `IsApply bool` with a `Mode string` field carrying one of `"preview" | "apply" | "restore"`; template picks the title (`"Previewing…"` / `"Applying…"` / `"Restoring…"`)
- `templates/selfcheck_done.html` — same Mode field, used to pick the success-line wording (`"Quarantined N"` vs. `"Restored N"`)

## Data flow notes

- The `_QUARANTINE` button on the existing done page still works. No conflict — that opens a folder; History runs a tool.
- The original Apply's run_id and the restore run's run_id are different. Loading the restore run's results does NOT chain back to the Apply's preview; the manifest dry-run output is self-contained.

## Testing

Bash side:
- New JSON events emitted by `do_restore` need a `tests/json_events/` case: seed a manifest, run `--restore-dry-run --json-events`, assert the event stream looks correct (run_start, N progress, N+something action events, run_end).
- Existing echo behavior must stay: spot-check that human-readable lines still arrive on stderr under `--json-events`.

Go side:
- `collectHistory` — unit tests with fixture ndjson files covering: success, cancelled-partial, no-run_end, no-manifest, mode-not-apply, multiple sorted.
- `resolveManifest` — unit tests for present/missing manifest path.
- handlers — go through the same pattern as `selfcheck_test.go`: HTTP-level smoke against a stubbed run manager.

UI smoke:
- Run a real self-check apply → switch to History tab → click row → confirm restore → verify files moved back.

## Out of scope (deferred)

- "Restore again" semantics if user restores then re-applies the same scan.
- Showing per-file metadata (size, mtime) in the preview — manifest TSV has limited fields; if it's a quick win during build we add it.
- i18n on the new templates' English strings — stage 8 sweeps everything.

## Open questions

None currently. The decisions captured:
- History scope: UI-originated applies only (user's pick).
- Restore flow: dry-run preview → confirm → execute (user's pick).
- Detail page: single "Restore Preview" page (user's pick).
- twincut.sh changes acceptable as long as no functional regression (user's pick).
