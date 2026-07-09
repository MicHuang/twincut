#!/usr/bin/env bash
# tests/backup_selfcheck_smoke.sh — backup self-check must survive non-video
# files (2026-07 regression: video-meta lookup crashed under set -e before
# the is_video_ext guard, killing the run with exit 1 and no run_end).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TC="$ROOT/bin/twincut.sh"
fail(){ echo "FAIL: $*" >&2; exit 1; }

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
bk="$work/backup"; mkdir -p "$bk"
printf 'AAA' > "$bk/a.jpg"
printf 'AAA' > "$bk/b.jpg"     # exact dupe of a.jpg
printf 'CCC' > "$bk/c.jpg"     # unique

# A real, good video: integration guard that the backup similar-video loop
# never condemns it as "bad". Note: this exercises the happy path only — the
# meta-row-missing race behind the self-heal-before-verdict ordering fix is
# not reproducible black-box (ensure_video_meta_index populates the row
# before the loop), so this assertion also passes on pre-fix code.
vid_src="$ROOT/tests/fixtures/video/clip_high.mp4"
vid_path="$bk/good.mp4"
cp "$vid_src" "$vid_path"

events="$work/events.ndjson"
if ! TWINCUT_RUN_ID=r_bkself \
    "$TC" --backup "$bk" --report-backup-dupes \
    --quarantine "$work/q" --json-events \
    >"$events" 2>"$work/stderr.log"; then
  echo "--- stderr tail ---" >&2; tail -5 "$work/stderr.log" >&2
  fail "backup self-check exited nonzero on a dir with non-video files"
fi

grep -q 'BACKUP-DUPE' "$work/stderr.log" || fail "expected [BACKUP-DUPE] report line"
grep -q '===== SUMMARY =====' "$work/stderr.log" || fail "run died before SUMMARY"
grep -q '"type":"run_end"' "$events" || fail "no run_end event emitted"
grep -q '"status":"succeeded"' "$events" || fail "run_end not succeeded"

[[ -f "$vid_path" ]] || fail "good video was moved/removed from its original path"
if find "$work/q" -type d -iname '_bad_video' 2>/dev/null | grep -q .; then
  fail "good video was quarantined under _bad_video"
fi

echo "backup_selfcheck_smoke: all ok"
