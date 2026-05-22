# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository overview

`twincut` is a bash-only media de-duplication tool. It compares a "source" directory against one or more "backup" directories (cross-check) and/or scans either side for internal duplicates (self-check), removing/quarantining matches in the source.

The repo is tiny:
- `bin/twincut.sh` — the main script (~1000 lines, single file, all logic).
- `bin/vid_eq.sh` — helper invoked by twincut for video equivalence checks (uses `ffprobe`).
- `installers/install.sh` / `uninstall.sh` — symlink `bin/twincut.sh` → `~/.local/bin/twincut` and `bin/vid_eq.sh` → `~/.local/bin/vid_eq`.

External runtime deps: `bash`, `ffprobe`/`ffmpeg`, standard coreutils, `md5`/`sha1` tooling, `jq` (for Stage 9 apply `--json-in` mode). Optional for L1 perceptual-hash pairing (P1 wave 2): `python3 ≥ 3.8`, `Pillow ≥ 9.0`, `imagehash ≥ 4.3` — install via `pip3 install --user pillow imagehash`. Without them, L1 falls back to flat suspect-list behavior.

## Common commands

```sh
# Install / uninstall (creates symlinks in ~/.local/bin)
installers/install.sh
installers/uninstall.sh

# Help
bin/twincut.sh --help

# Cross-check (typical): remove files in SOURCE that already exist in BACKUP
bin/twincut.sh --source <SRC> --backup <BK> [--backup <BK2> ...] --dry-run

# Self-check modes (legacy; these SKIP cross-check; source path still runs similar-video)
bin/twincut.sh --source <SRC> --backup <BK> --report-backup-dupes
bin/twincut.sh --source <SRC> --backup <BK> --fix-source-dupes

# Self-check (recommended; role-agnostic; hash-only by default)
bin/twincut.sh --self-check <DIR> [--dry-run]
bin/twincut.sh --self-check <DIR> --include-similar-video   # opt-in similar-video
bin/twincut.sh --restore <DIR>/_QUARANTINE/_manifest-<RUN_ID>.tsv  # roll back
```

There is no build system, no test suite, and no linter configured. When changing `twincut.sh`, validate by running with `--dry-run` against a scratch directory.

## Architecture notes

Things that are non-obvious from skimming a single function:

- **Single-file design.** All state lives in globals at the top of `twincut.sh` (defaults block, lines ~7–100). CLI parsing (`while [[ $# -gt 0 ]]`) mutates these. Helpers are short and assume the globals — don't refactor a helper in isolation without checking which globals it reads.

- **Four run modes, mutually exclusive in effect:**
  - Cross-check (default): runs only when no self-check flag is set.
  - Backup self-check (`--report-backup-dupes` / `--fix-backup-dupes`) — sets `DO_BACKUP_SELF`.
  - Source self-check (`--report-source-dupes` / `--fix-source-dupes`) — sets `DO_SOURCE_SELF`. Source path runs similar-video detection by default.
  - **Self-check `--self-check <DIR>`** (recommended, role-agnostic) — sugar over source self-check. Sets `SELF_CHECK_MODE=true`, translates at mode-resolution time into `SOURCE_DIR + DO_SOURCE_SELF + FIX_SOURCE_DUPES`. Quarantine defaults to `<DIR>/_QUARANTINE/_self_dupes/`. Hash-only by default (`EXACT=true`); `--include-similar-video` re-enables video-fast. Prints a copy-paste `--restore` command at the end. Mutually exclusive with `--source` / `--backup` / `--report/--fix-*-dupes` / `--thumbnail-detect`.
  Self-check modes skip cross-check. Similar-video runs by default in the legacy source path but is OFF by default for `--self-check` (opt-in via `--include-similar-video`).

- **Hash caches.** Backup side uses `<backup>/.backup_hashindex.txt`; source side uses `<source>/.source_hashindex.txt` (created during `--dry-run`, removed after a successful non-dry run unless `--keep-source-cache`). Caches carry a `# meta:` header (algo/min_size/exts/created); `should_rebuild_cache` invalidates on meta drift. `prune_cache_missing` keeps the header and only live files.

- **Video matching has three tiers**, controlled by `VIDEO_FAST` / `VIDEO_FAST_STRICT` / `EXACT`:
  1. `--exact`: hash-only (disables fast).
  2. Default fast: join candidates by size window (`SIZE_PCT`, ±0.5%) and duration bucket (`DUR_SEC`, 0.3s steps via `duration_bucket`).
  3. `--video-fast-strict`: tightens windows (size 0.2%, dur 0.15s), additionally compares fps/bitrate, and runs `vid_eq.sh` for a final check. Strict defaults are reapplied at startup (lines ~92–95).
  Video metadata (size/dur/fps/bitrate/codec/wh) is cached in `<dir>/.video_meta_index.csv` (TSV despite the extension). `--rebuild-video-meta` forces rebuild.

- **`vid_eq.sh` is located via `V_EQ_BIN`**, resolved at startup in this order: env override → sibling `vid_eq` → sibling `vid_eq.sh` → `PATH`. Script aborts immediately if not found. Both fast helpers share the same `SIZE_PCT` / `DUR_SEC` env knobs.

- **Sidecar handling.** AppleDouble (`._*`) files and "bad" videos (unreadable by ffprobe) are sorted to their own subdirs (`_appledouble`, `_bad_video`) with configurable actions (`move|list|delete|ignore`). These run independently of dedupe and are on by default.

- **Quarantine vs delete.** `DEST_ACTION` (`move|delete|list`) controls what happens to source-side duplicates; `move` (default) goes to `QUAR_DIR` (default `./_QUARANTINE`). `move_unique` handles name collisions in the quarantine.

- **Similar-video output** lives under per-side subdirs: `_similar_video`, `_similar_video_backup`, `_similar_video_source`, with a CSV map `_similar_video_map.csv`.

- **Stage 9 (`thumbnail_detect` only): Go-owned contract.** The web-UI
  `--json-events` channel uses a single typed schema rooted in Go structs
  (`ui/server/events.go`). bash emits via per-type helpers in
  `lib/events.sh` (e.g. `emit_thumb_candidate`, `emit_action_move`);
  7 legacy `emit_event` call sites remain in `bin/twincut.sh` for
  cross-check / restore / similar-video flows (out of Stage 9 scope —
  those workflows will migrate in a future stage). Apply input flows
  from Go to bash as stdin JSON-lines (`ApplyCommand` records via
  `--json-in`); no more `.thumb-confirm.tsv` round-trip. Drift between
  Go and bash is caught by `ui/server/events_roundtrip_test.go`, which
  decodes every fixture in `tests/fixtures/events/` with
  `json.Decoder.DisallowUnknownFields`. New runtime dep: `jq` (used by
  `--json-in` apply mode for stdin parsing). New smoke:
  `tests/p1_stage9_smoke.sh` exercises the end-to-end scan→apply
  pipeline via the typed contract.

## Agent operational notes

These rules emerged from real session failures on this codebase. Follow them when working here.

### Subagent output token limit (~32k)

Dispatching a single subagent to produce a large markdown/code deliverable (>~800 lines) often fails: the agent streams content as prose, exceeds the 32k output-token cap, and the call returns an error with no `Write`/`Edit` ever called — the work is lost.

When the deliverable would be that large:
- Split the work across 2-3 sequential dispatches (e.g., a 15-task plan as header+tasks 1-5 / tasks 6-10 / tasks 11-15+closers).
- Instruct each agent to `Write` (chunk 1) or `Edit` (chunks 2+) directly to the target file. Cap the agent's text reply at ~150 words.

### NDJSON / schema naming follows the codebase, not the spec

Every NDJSON event emitted by `bin/twincut.sh` (and `lib/*.sh` via the `emit_event` helper) uses `"type":"<name>"` as the discriminator key — never `"event":"<name>"`. Before adding a new event kind to a spec or plan, `grep '"type":' bin/*.sh lib/*.sh ui/server/events.go` to confirm the schema and adopt it. The Go-side parser (`ui/server/events.go`) is keyed on `"type"`.

The same principle applies to other naming (struct fields, routes, helpers): search the codebase first, then write the spec.
