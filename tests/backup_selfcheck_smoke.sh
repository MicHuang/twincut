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

# --- vmeta-index guard: a tab-in-name *video* must not be written into the
# vmeta TSV either (a raw tab in the path splits the row into >11 fields, and
# the liveness/dedup checks never match it, so it re-appends every run) ---
tabvid="$(printf 'evilvid%bclip' '\t').mp4"
cp "$vid_src" "$bk/$tabvid"

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

vmeta="$bk/.video_meta_index.csv"
[[ -f "$vmeta" ]] || fail "tab-in-name: vmeta index missing (test premise broken)"
grep -q 'unsafe for video meta index' "$work/stderr2.log" \
  || fail "tab-in-name: expected vmeta skip warning on stderr"
if awk -F '\t' 'NR>2 && NF!=11 {exit 1}' "$vmeta"; then :; else
  fail "tab-in-name: vmeta index contains a corrupt (!=11 field) row"
fi
grep -q 'evilvid' "$vmeta" && fail "tab-in-name: tab-named video path leaked into vmeta index"
if grep '"code":"bad_video"' "$events2" | grep -q 'evilvid'; then
  fail "tab-in-name: good video falsely reported bad_video just because its path is TSV-unsafe"
fi

# --- vmeta liveness must tolerate rows whose file has since been deleted
# (a dead path in the LAST retained row made the retention pipeline exit 1
# and killed the whole run under set -e, before run_end/SUMMARY) ---
rm -f "$vid_path"
events3="$work/events3.ndjson"
if ! TWINCUT_RUN_ID=r_bkself_del \
    "$TC" --backup "$bk" --report-backup-dupes \
    --quarantine "$work/q" --json-events \
    >"$events3" 2>"$work/stderr3.log"; then
  echo "--- stderr tail ---" >&2; tail -5 "$work/stderr3.log" >&2
  fail "backup self-check exited nonzero after an indexed video was deleted"
fi
grep -q '"type":"run_end"' "$events3" || fail "deleted-video: no run_end emitted"
grep -q '"status":"succeeded"' "$events3" || fail "deleted-video: run_end not succeeded"
grep -q 'good\.mp4' "$vmeta" && fail "deleted-video: dead row not pruned from vmeta index"

# --- fix-mode: quarantining an indexed video mid-run leaves its vmeta row
# dead; the end-of-run refresh must survive that (a dead path in the LAST
# retained row made the retention pipeline exit 1 under set -e, killing the
# run after the moves but before run_end/SUMMARY). The keeper is a .jpg
# (same bytes, oldest mtime) so BOTH mp4 rows are dead at refresh time,
# regardless of find/append order. ---
bk2="$work/backup2"; mkdir -p "$bk2"
cp "$vid_src" "$bk2/keep.jpg"
cp "$vid_src" "$bk2/b1.mp4"
cp "$vid_src" "$bk2/z2.mp4"
touch -t 202001010000 "$bk2/keep.jpg"   # oldest ⇒ KEEP; both mp4s get moved
events4="$work/events4.ndjson"
if ! TWINCUT_RUN_ID=r_bkself_fix \
    "$TC" --backup "$bk2" --fix-backup-dupes \
    --quarantine "$work/q2" --json-events \
    >"$events4" 2>"$work/stderr4.log"; then
  echo "--- stderr tail ---" >&2; tail -5 "$work/stderr4.log" >&2
  fail "fix-mode exited nonzero after quarantining indexed videos"
fi
grep -q '"type":"run_end"' "$events4" || fail "fix-mode: no run_end emitted"
grep -q '"status":"succeeded"' "$events4" || fail "fix-mode: run_end not succeeded"
[[ -f "$bk2/keep.jpg" ]] || fail "fix-mode: keeper was moved"
[[ ! -f "$bk2/b1.mp4" && ! -f "$bk2/z2.mp4" ]] || fail "fix-mode: dupe mp4s not quarantined"
if grep -q '\.mp4' "$bk2/.video_meta_index.csv"; then
  fail "fix-mode: dead mp4 rows not pruned from vmeta index"
fi

echo "backup_selfcheck_smoke: all ok"
