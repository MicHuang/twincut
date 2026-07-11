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


# --- hash-index guard: a tab-in-name file must not be indexed (a raw tab
# in the hash index makes every awk -F'\t' reader mis-parse the row) ---
tabf="$(printf 'evil%bname' '\t').jpg"
printf 'TABTAB' > "$bk/$tabf"

events2="$work/events2.ndjson"
if ! TWINCUT_RUN_ID=r_bkself_tab \
    "$TC" --backup "$bk" --report-backup-dupes \
    --quarantine "$work/q" --json-events \
    >"$events2" 2>"$work/stderr2.log"; then
  fail "backup self-check exited nonzero with a tab-in-name file present"
fi
grep -q '"type":"run_end"' "$events2" || fail "tab-in-name: no run_end emitted"
grep -q '"status":"succeeded"' "$events2" || fail "tab-in-name: run_end not succeeded"
grep -q 'unsafe for hash index' "$work/stderr2.log" \
  || fail "tab-in-name: expected hash-index skip warning on stderr"
if awk -F '\t' 'NF>2 && $0 !~ /^#/ {exit 1}' "$bk/.backup_hashindex.txt"; then :; else
  fail "tab-in-name: hash index contains a corrupt (>2 field) row"
fi

echo "backup_selfcheck_smoke: all ok"
