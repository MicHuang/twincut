# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository overview

`twincut` is a bash-only media de-duplication tool. It compares a "source" directory against one or more "backup" directories (cross-check) and/or scans either side for internal duplicates (self-check), removing/quarantining matches in the source.

The repo is tiny:
- `bin/twincut.sh` — the main script (~1000 lines, single file, all logic).
- `bin/vid_eq.sh` — helper invoked by twincut for video equivalence checks (uses `ffprobe`).
- `installers/install.sh` / `uninstall.sh` — symlink `bin/twincut.sh` → `~/.local/bin/twincut` and `bin/vid_eq.sh` → `~/.local/bin/vid_eq`.

External runtime deps: `bash`, `ffprobe`/`ffmpeg`, standard coreutils, `md5`/`sha1` tooling.

## Common commands

```sh
# Install / uninstall (creates symlinks in ~/.local/bin)
installers/install.sh
installers/uninstall.sh

# Help
bin/twincut.sh --help

# Cross-check (typical): remove files in SOURCE that already exist in BACKUP
bin/twincut.sh --source <SRC> --backup <BK> [--backup <BK2> ...] --dry-run

# Self-check modes (these SKIP cross-check and similar-video logic)
bin/twincut.sh --source <SRC> --backup <BK> --report-backup-dupes
bin/twincut.sh --source <SRC> --backup <BK> --fix-source-dupes
```

There is no build system, no test suite, and no linter configured. When changing `twincut.sh`, validate by running with `--dry-run` against a scratch directory.

## Architecture notes

Things that are non-obvious from skimming a single function:

- **Single-file design.** All state lives in globals at the top of `twincut.sh` (defaults block, lines ~7–100). CLI parsing (`while [[ $# -gt 0 ]]`) mutates these. Helpers are short and assume the globals — don't refactor a helper in isolation without checking which globals it reads.

- **Three run modes, mutually exclusive in effect:**
  - Cross-check (default): runs only when neither `--report/--fix-*-dupes` self-check flag is set.
  - Backup self-check (`--report-backup-dupes` / `--fix-backup-dupes`) — sets `DO_BACKUP_SELF`.
  - Source self-check (`--report-source-dupes` / `--fix-source-dupes`) — sets `DO_SOURCE_SELF`.
  Self-check modes intentionally skip cross-check AND similar-video detection.

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
