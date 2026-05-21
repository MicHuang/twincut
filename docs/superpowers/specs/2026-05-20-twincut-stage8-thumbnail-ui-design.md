# Stage 8 — Thumbnail-detect Web UI

Status: design approved 2026-05-20, awaiting implementation plan.

## Context

After stage 7 (Cross-check tab, PR merged), the web UI covers both self-check and cross-check workflows end-to-end, with History/Restore wired through both. The sidebar, however, still ships the stage-4 placeholder copy: Cross-check and History both render with `disabled` styling and "Coming in stage 7/6" badges even though both features are fully wired, and the footer reads `stage 4`. Stage 8 adds the Thumbnails tab as the third first-class workflow and cleans up the stale sidebar state as part of the same change.

P1 wave 1 landed the thumbnail-detect CLI (`bin/twincut.sh --thumbnail-detect`), implementing three detection layers: L1 (resolution heuristic — small files with no larger peer), L2 (EXIF fingerprint cluster — small files sharing a camera EXIF fingerprint with a larger sibling), and L3 (embedded-thumb match — files whose pixel content matches the embedded JPEG thumbnail of a larger file). The CLI already moves matching files to quarantine and writes a restore-compatible manifest. What it does not yet have is a dry-run NDJSON event stream or a `--thumb-confirm` apply path that Go can drive — those are the bash-side additions in this stage.

Stage 8 wires thumbnail-detect into the UI as a preview → pick → apply flow, aligned with the self-check and cross-check patterns the user already knows. The form collects source folder and detection thresholds. Preview streams `thumb_candidate` NDJSON events from a dry-run. Results renders cluster cards for L2 and L3 groups and a collapsible L1 review block. Apply writes an enhanced six-column review CSV and launches `--thumb-confirm` to execute the moves.

## Goals

- Add a working Thumbnails tab to the sidebar as a new nav entry.
- Sidebar cleanup: drop the `disabled` class and "soon" badges from Cross-check and History (stale since their stages merged); update the footer label from `stage 4` to `stage 8`.
- Source folder picker with max-edge and maybe-max-edge threshold fields, and a require-EXIF-match toggle.
- Cluster cards for L2 (keeper + EXIF-fingerprint siblings) and L3 (large file + embedded-thumb match).
- Collapsible L1 review block: flat table of suspects, default unchecked, opt-in consent model.
- Preview via `--thumbnail-detect --dry-run --json-events`; no files moved until apply.
- Apply via `--thumb-confirm` with an extended six-column review CSV carrying a per-row `decision` field.
- Thumbnail apply runs enter History and are restorable through the existing stage-6 wiring, unchanged.
- Backward compatibility with hand-edited five-column review CSVs (CLI users' apply path unaffected).

## Non-goals (v1)

- L2/L3 keeper override (let user swap which file is kept) — stage 9 candidate.
- Preview-time thumbnail prefetch warmup — performance polish, not correctness.
- L1 perceptual-hash clustering — P1 wave 2, separate roadmap with its own detection spec.
- Concurrent multi-source detect within one run — does not fit the current single-run architecture.
- Per-source threshold memory (recents extension) — bundle with future recents work.
- i18n on new templates — "stage 9 sweeps everything."

## Architecture

### Bash side (`lib/thumb.sh` + small `bin/twincut.sh` tweaks)

`lib/thumb.sh` functions `thumb_run_l2` and `thumb_run_l3` today call `qmove` unconditionally. When `DRY_RUN=true`, they must instead emit NDJSON to stdout and return without touching any file.

L2 dry-run event (one per thumbnail candidate):

```bash
printf '{"type":"thumb_candidate","decision":"thumb_l2_exif","path":"%s","keeper":"%s","group_id":"%s","width":%d,"height":%d,"size_bytes":%d}\n' \
  "$_thumb_path" "$_keeper_path" "$_fingerprint_sha1" "$_w" "$_h" "$_sz"
```

L3 dry-run event (one per pair):

```bash
printf '{"type":"thumb_candidate","decision":"thumb_l3_embed","path":"%s","keeper":"%s","group_id":"l3:%s","width":%d,"height":%d,"size_bytes":%d}\n' \
  "$_thumb_path" "$_big_path" "$_sha1_of_big_path" "$_w" "$_h" "$_sz"
```

L1 continues to write to `_review.csv` as today. No event emitted; the Go server reads the file after the run ends.

`thumb_confirm_review` gains a `decision` column parser. If the column is present, its value is used verbatim as the manifest decision. If absent (legacy five-column CSV), the function falls back to `thumb_confirmed`. Allowed values: `thumb_l2_exif`, `thumb_l3_embed`, `thumb_confirmed`. Any other value: row is rejected with a warning logged to stderr; the run continues with remaining rows.

`bin/twincut.sh` changes:

- Emit `_mode="thumbnail_detect_preview"` in `run_start` when `DO_THUMB=true && DRY_RUN=true`; emit `_mode="thumbnail_detect_apply"` when running `--thumb-confirm`. Go's `BuildResults` mode-prefix logic works on these automatically.
- Return a usage error if `--thumbnail-detect` and `--apply-list` are both set (preserves future extensibility; these are separate apply paths).

Untouched: `qmove`, manifest schema, restore logic, `thumb_run_l1`, non-dry-run direct `--thumbnail-detect` execution (CLI users' happy path is unchanged).

Change budget: ~80 lines in `lib/thumb.sh`, ~10 lines in `bin/twincut.sh`.

### Go server (`ui/server/`)

New file `ui/server/thumbnail.go` (~250 lines), structured to mirror `crosscheck.go`. Register all routes in `http.go`.

Routes:

| Path | Handler | Purpose |
|---|---|---|
| `GET /tab/thumbnails` | `handleThumbnailsTab` | HTMX fragment with form |
| `POST /api/thumbnails/preview` | `handleThumbnailsPreview` | Validate source, launch dry-run, return running panel |
| `GET /api/thumbnails/results/{id}` | `handleThumbnailsResults` | Build view from thumb_candidate events + _review.csv |
| `POST /api/thumbnails/apply` | `handleThumbnailsApply` | Compose CSV, launch --thumb-confirm |
| `GET /api/thumbnails/done/{id}` | `handleThumbnailsDone` | Done page |

Touched existing files:

- `ui/server/events.go`: new `ThumbCandidate` struct + parser case for `"thumb_candidate"` event type (~20 lines).
- `ui/server/runs.go`: `Run.Mode` allowlist gains `"thumbnail_detect_preview"` and `"thumbnail_detect_apply"`.
- `ui/server/results.go`: `BuildResults` adds a `"thumbnail_detect"` mode-prefix branch. Builds `ResultGroup`s from `ThumbCandidate` events — L2: one group per `group_id` with keeper + all thumbs as members; L3: one two-member group per event. After the event scan, reads `<source>/_review.csv` and builds one synthetic group with `GroupID = "l1-suspects"` populated from its rows. Sets `ApplyURL = /api/thumbnails/apply`. (~60 additional lines in results.go.)
- `ui/server/apply_list.go`: new helper `composeThumbnailConfirmCSV(view ResultView, form url.Values) ([]byte, error)` (~80 lines). Writes the six-column enhanced CSV. Not a reuse of `composeApplyList` — different output schema; separate function.
- `ui/templates/app.html`: add Thumbnails nav link; remove stale `disabled` + "soon" markers from Cross-check and History; bump footer label (~5 lines net change).

Key technical decisions:

- Apply CSV temp file path: `<stateDir>/runs/<apply-run-id>.thumb-confirm.csv`. Retained 14 days for debugging; GC is stage 9.
- Group ID stability: L2 = fingerprint SHA1 passed through from bash NDJSON; L3 = SHA1 of keeper path; L1 = fixed string `"l1-suspects"`. Used as form name prefixes (`group:<id>.member[i]`).
- Member roles: keeper → `Role="keeper"`, thumbnail candidates → `Role="thumbnail"` (new role string; template applies one CSS class). L1 rows → `Role="suspect"`. Cluster card header marks L1 group as "no peer."
- `handleThumbnailsPreview` validates source via the existing `IsAllowedPath` allowlist before launching any subprocess.

Change budget: ~250 lines `thumbnail.go` + ~80 lines `composeThumbnailConfirmCSV` + ~40 lines test scaffolding; existing files: `results.go` +60, `events.go` +20, `http.go` +5, `app.html` +1 line.

### Templates (`ui/templates/`)

| Template | Treatment |
|---|---|
| `thumbnails_form.html` | **New.** Source picker + max-edge field (default 512) + maybe-max-edge field (default 1024) + require-EXIF-match checkbox. Submit posts to `/api/thumbnails/preview`. |
| `thumbnails_results.html` | **New.** Summary header (N candidates across L1/L2/L3). Cluster cards for L2/L3 groups (keeper row read-only, thumbnail rows checked by default). Collapsible `<details>` block for the L1 group (unchecked by default, titled "L1 review (N suspects, no peer)"). Apply form posts checked rows to `/api/thumbnails/apply`. |
| `thumbnails_l1_row.html` | **New.** L1 row partial: 80×80 thumbnail preview via `/thumb?path=...&size=80`, path, reason badge (`l1_only_thumb` or `l1_only_maybe`), width × height display, checkbox (default unchecked). |
| `selfcheck_running.html` | **Reuse as-is.** Mode strings `thumbnail_detect_preview` / `thumbnail_detect_apply` drive title text via existing `{{if eq .Mode ...}}` branches (add two cases). |
| `selfcheck_done.html` | **Reuse as-is.** Wording "Moved N files to quarantine" is already mode-agnostic. |
| `app.html` | **Sidebar cleanup + new link.** Add `<a hx-get="/tab/thumbnails">Thumbnails</a>` nav link after Cross-check. Drop the `disabled` class and `<span class="muted-tag">soon</span>` from the Cross-check and History entries (stale since stages 7/6 merged). Update footer label from `stage 4` to `stage 8`. |

### End-to-end data flow

```
[User]                          [Go server]                        [bash]
  │                                │                                  │
  ├── GET /tab/thumbnails ────────►│                                  │
  │◄── thumbnails_form.html ───────┤                                  │
  │                                │                                  │
  ├── POST /api/thumbnails/preview►│                                  │
  │   {source, max_edge, …}        │                                  │
  │                                ├── runs.Start(mode=thumbnail_detect_preview)
  │                                │    args: --thumbnail-detect --dry-run --json-events
  │◄── selfcheck_running.html ─────┤                                  │
  │   (run_id, next_url)           │                                  ├── L1 → _review.csv
  │                                │                                  ├── L2 dry-run → thumb_candidate NDJSON
  │                                │                                  ├── L3 dry-run → thumb_candidate NDJSON
  ├── EventSource /sse/{id} ──────►│◄── NDJSON stream ────────────────┤
  │◄── progress / run_end ─────────┤                                  │
  │                                │                                  │
  ├── GET /api/thumbnails/results/{id} ►│                             │
  │                                ├── BuildResults                   │
  │                                │   ├─ parse thumb_candidate       │
  │                                │   ├─ read _review.csv (L1)       │
  │                                │   └─ build ResultGroups          │
  │◄── thumbnails_results.html ────┤                                  │
  │                                │                                  │
  ├── POST /api/thumbnails/apply ─►│                                  │
  │   group:<id>.member[i]=on …    │                                  │
  │                                ├── composeThumbnailConfirmCSV     │
  │                                │   → <stateDir>/runs/<id>.thumb-confirm.csv
  │                                ├── runs.Start(mode=thumbnail_detect_apply)
  │                                │    args: --thumb-confirm <csv> --assume-yes --json-events
  │◄── selfcheck_running.html ─────┤                                  ├── parse csv → qmove per row
  │                                │                                  ├── manifest decision = row.decision
  │                                │◄── run_end {manifest_path} ──────┤
  │                                │                                  │
  ├── GET /api/thumbnails/done/{id}►│                                 │
  │◄── selfcheck_done.html ────────┤                                  │
  │                                │                                  │
  │ Later: history tab shows apply run; restore reuses stage 6 wiring │
```

### CSV schema increment

```
existing (5 cols):  path,reason,width,height,note
extended (6 cols):  path,reason,width,height,note,decision
```

Allowed `decision` values: `thumb_l2_exif`, `thumb_l3_embed`, `thumb_confirmed`. Empty or absent decision column falls back to `thumb_confirmed` — backward-compatible with hand-edited review CSVs from CLI users.

`composeThumbnailConfirmCSV` derives `decision` from each checked row's `ResultGroup.Members[i].Decision` field (populated by `BuildResults` from the original `ThumbCandidate` event). The function handles CSV escaping for commas, double-quotes, and non-ASCII path characters.

## Testing

**Bash side (`tests/p1_thumb_smoke.sh` extension + new fixture):**

- L2 dry-run emits `thumb_candidate` NDJSON with `decision=thumb_l2_exif` and correct fields; assert no file is moved.
- L3 dry-run emits `thumb_candidate` NDJSON with `decision=thumb_l3_embed` and correct fields; assert no file is moved.
- Enhanced six-column review CSV processed by `--thumb-confirm` writes each row's `decision` value verbatim into the manifest entry.
- Legacy five-column review CSV processed by `--thumb-confirm` defaults decision to `thumb_confirmed` in the manifest.
- `--thumbnail-detect --apply-list <path>` returns a non-zero exit and prints a usage error; no run proceeds.
- `run_start` NDJSON `_mode` field is `thumbnail_detect_preview` for dry-run and `thumbnail_detect_apply` for `--thumb-confirm`; verified via grep.

**Go side (unit tests in `ui/server/`):**

- `events_test.go::TestParseThumbCandidate`: L2 event parses all fields; L3 event parses all fields; missing `decision` field returns parse error; malformed JSON returns parse error.
- New `thumbnail_test.go`:
  - `handleThumbnailsPreview`: valid form produces correct args slice; source path failing `IsAllowedPath` returns 422.
  - `BuildResults` thumbnail mode: single L2 `thumb_candidate` event produces one `ResultGroup` with keeper `Role="keeper"` and thumb `Role="thumbnail"`; L3 event produces two-member group; `_review.csv` presence produces `"l1-suspects"` group with `Role="suspect"` members.
  - `composeThumbnailConfirmCSV`: checked rows are included, unchecked are dropped; decision value matches the original event decision; paths containing commas and double-quotes are correctly escaped; Unicode paths round-trip without corruption.
  - `handleThumbnailsApply`: CSV is written to `<stateDir>/runs/<id>.thumb-confirm.csv`; run is launched with `--thumb-confirm <csv> --assume-yes --json-events` in args.
- `results_test.go::TestBuildResults_ThumbnailMode_ApplyURL`: `BuildResults` with `thumbnail_detect_preview` mode sets `ApplyURL = /api/thumbnails/apply`.
- `history_test.go`: apply run with `mode=thumbnail_detect_apply`, `dry_run=false`, `manifest_path` non-empty appears in history list; preview run (`dry_run=true`) does not.

**UI smoke (manual, fixture-driven):**

New `tests/fixtures/thumbnails/` directory: 3 files sharing EXIF fingerprint (1 large keeper + 2 small thumbnails for L2), 1 L3 pair (large file with embedded thumbnail + matching small file), 2 L1-only suspects (one `l1_only_thumb`, one `l1_only_maybe`), 3 clean large images with no duplicates.

End-to-end: open UI → Thumbnails tab → pick fixture dir → Preview → Results shows 2 cluster cards (L2 with 2 checked thumbs, L3 with 1 checked thumb) + collapsible L1 block with 2 unchecked rows → check all L1 rows → Apply → Done shows moved=5 → History tab contains one apply entry with correct mode → Restore → all 5 files return to fixture directory.

## Out of scope (deferred)

- **L2/L3 keeper override** — let user designate a different file as the keeper within a cluster. Stage 9 candidate; requires new form controls and bash-side re-routing.
- **Preview-time thumbnail prefetch warmup** — pre-loading `/thumb?path=...` for all candidates during the SSE stream. Performance polish; no correctness impact.
- **L1 perceptual-hash clustering** — grouping L1 suspects by visual similarity rather than listing them flat. P1 wave 2 with its own detection spec.
- **Concurrent multi-source detect in one run** — does not fit the current single-run model where one process owns one source directory.
- **Per-source threshold memory** — remembering last-used `max_edge`/`maybe_max_edge` per source path. Bundle with future recents extension work.
- **Apply CSV garbage collection** — `<stateDir>/runs/*.thumb-confirm.csv` files accumulate; 14-day GC policy defined but not implemented. Stage 9 sweeps this with a general run-dir GC pass.

## Open questions

None currently. Decisions of record:

- Flow model = preview → pick → apply, aligned with self-check and cross-check. (user's pick)
- L2/L3 rendering = cluster cards (keeper + thumbnail members). (user's pick)
- L1 rendering = collapsible `<details>` review table, default unchecked. (user's pick)
- Apply channel = extended `--thumb-confirm` with a `decision` column in review CSV. (user's pick)
- L2/L3 keeper override not exposed in v1. (design inference — kept simple; stage 9 candidate)
- Preview uses `thumb_candidate` NDJSON events from dry-run; no file moves until apply. (design inference)
- Apply CSV retained 14 days for debugging; GC deferred. (design inference)
- `--thumbnail-detect` + `--apply-list` combination is a usage error. (design inference — separate apply paths, preserves future extensibility)
- `thumb_run_l1` is untouched; L1 continues writing `_review.csv` as today; Go reads file post-run. (design inference)
