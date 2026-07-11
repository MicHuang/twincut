# Follow-up Hygiene Wave (F-H1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Clear the four non-blocking follow-ups recorded after the 2026-07-05 remediation milestone: (1) KEEP-policy determinism remainder, (2) TSV/path-guard extension, (3) stage9 D1/D1b `run_end` assertions, (4) `gofmt` the ui tree.

**Architecture:** All bash changes go into `bin/twincut.sh` as two tiny helpers (`pick_keep`, `tsv_path_safe`) plus call-site edits — no new files in `bin/` or `lib/`. One new smoke (`tests/keep_policy_smoke.sh`) pins keep-policy determinism; guard tests extend the two existing smokes that already own those flows. Task 4 is a mechanical `gofmt -w`.

**Tech Stack:** bash 3.2-compatible shell (macOS `/bin/bash` 3.2.57 must work), BSD **and** GNU coreutils (CI runs ubuntu + macOS), ffprobe/ffmpeg for video fixtures, Go toolchain for `ui/`.

## Global Constraints

- bash 3.2 compatible: no associative arrays, no `${var,,}`, no `mapfile`.
- Must pass on BSD (macOS) **and** GNU (ubuntu CI) coreutils: `touch -t`, `sort`, `find` usage must work on both.
- CI has a **shellcheck gate**: all new/edited shell code must be shellcheck-clean (`shellcheck bin/*.sh lib/*.sh tests/*.sh` per `.github/workflows/ci.yml`).
- NDJSON events use `"type":"<name>"` discriminator; emit only via typed helpers in `lib/events.sh` (here: only the existing `emit_warn`). No new event kinds in this wave.
- New smoke files must be wired into BOTH `Makefile` `test-smoke` and `.github/workflows/ci.yml` shell job (pattern: line ~47 `bash tests/backup_selfcheck_smoke.sh`).
- Smokes that need ffprobe must SKIP (exit 0 with a `SKIP:` line) when it's missing, unless `TWINCUT_REQUIRE_TOOLS=1` (copy the prologue from `tests/vid_eq_smoke.sh`).
- Video fixtures: `tests/fixtures/video/clip_high.mp4` (37555 B, 3.0 s, h264 320x240) and `clip_low.mp4` (35913 B, 3.0 s, h264 320x240). They are similar-video candidates only with `SIZE_PCT=5` (4.37 % size delta; default 0.5 rejects).
- `dup_group` events carry the keeper as top-level `keep_path` (see `tests/fixtures/events/*.ndjson`).
- Do NOT touch: vid_eq strict re-verify design, the plan-level deferred perf items (O(N²) loops, double dir walk, …), or anything in `ui/` beyond `gofmt` in Task 4.

---

### Task 1: KEEP-policy determinism remainder

Two leftovers from the Wave-3 determinism fix:

- **Real fix:** both similar-video keep decisions still break equal-mtime ties by scan order (`find` enumeration order — filesystem-dependent):
  - backup self-check, `bin/twincut.sh:1326-1328`:
    ```bash
    mt_bf=$(mtime "$bf")
    mt_cd=$(mtime "$cand")
    if (( mt_bf <= mt_cd )); then KEEP="$bf"; MOVE="$cand"; else KEEP="$cand"; MOVE="$bf"; fi
    ```
  - source/cross similar-video (source-self branch), `bin/twincut.sh:1537-1539`:
    ```bash
    mt_src=$(mtime "$f")
    mt_b=$(mtime "$b")
    if (( mt_src <= mt_b )); then KEEP="$f"; MOVE="$b"; else KEEP="$b"; MOVE="$f"; fi
    ```
  (`mt_bf`/`mt_cd`/`mt_src`/`mt_b` are used nowhere else — verified via grep — so the assignments can be replaced wholesale.)
- **Hygiene-only fix:** the two hash-dupe keep sorts restrict the path key to field 2 (`-k2,2`), which truncates a tab-containing path at its first embedded tab: `bin/twincut.sh:1256` (backup `MAP_KEYED`) and `bin/twincut.sh:1616` (source `SMAP_KEYED`). **This is not end-to-end reachable today** — the paths fed into those sorts come from `awk -F'\t' '{print $2}'` extractions that already truncate at the first tab — so there is no red test for it; change it for consistency with the `cut -f2-` extraction below each sort, and rely on existing suites staying green.

Also note: the Wave-3 equal-mtime hash-dupe tie-break (sort by `(mtime, path)`) currently has **no test at all**; K1 below pins it.

**Files:**
- Modify: `bin/twincut.sh` (new helper near `qmove`, ~line 460; call sites 1256, 1326-1328, 1537-1539, 1616)
- Create: `tests/keep_policy_smoke.sh`
- Modify: `Makefile` (`test-smoke` target, after `backup_selfcheck_smoke.sh` line)
- Modify: `.github/workflows/ci.yml` (shell job, after line 47 `bash tests/backup_selfcheck_smoke.sh`)

**Interfaces:**
- Produces: `pick_keep A B` — bash function in `bin/twincut.sh`; sets globals `KEEP` and `MOVE`. Older mtime wins; equal mtimes fall back to `LC_ALL=C sort` path byte order. Task 2 does not depend on it; nothing else consumes it.

- [ ] **Step 1: Write the failing smoke**

Create `tests/keep_policy_smoke.sh`:

```bash
#!/usr/bin/env bash
# tests/keep_policy_smoke.sh — KEEP-policy determinism: equal-mtime ties must
# be broken by LC_ALL=C path byte order, never by find(1) scan order
# (directory enumeration is filesystem-dependent: ext4 htree vs APFS).
# K1 pins the Wave-3 hash-dupe (mtime, path) sort; K2/K3 pin the
# similar-video tie-break on the source and backup paths.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TC="$ROOT/bin/twincut.sh"
HI="$ROOT/tests/fixtures/video/clip_high.mp4"
LO="$ROOT/tests/fixtures/video/clip_low.mp4"
fail(){ echo "FAIL: $*" >&2; exit 1; }

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# --- K1. hash-dupe equal-mtime tie-break: keep = byte-smaller path ---------
# Create the byte-LARGER name first: on creation-ordered filesystems the old
# first-wins scan-order policy would have kept it, so this also guards
# against a regression back to scan order.
bk1="$work/k1"; mkdir -p "$bk1"
printf 'DUPDUP' > "$bk1/zz_dupe.jpg"
printf 'DUPDUP' > "$bk1/aa_dupe.jpg"
touch -t 202601011200 "$bk1/zz_dupe.jpg" "$bk1/aa_dupe.jpg"

ev1="$work/k1.ndjson"
TWINCUT_RUN_ID=r_keep_k1 "$TC" --backup "$bk1" --report-backup-dupes \
  --quarantine "$work/q1" --json-events >"$ev1" 2>"$work/k1.log" \
  || fail "K1: backup self-check exited nonzero"
grep -q "keep='$bk1/aa_dupe.jpg'" "$work/k1.log" \
  || fail "K1: equal-mtime hash-dupe keep is not the byte-smaller path"

# --- similar-video sections need ffprobe --------------------------------
if ! command -v ffprobe >/dev/null 2>&1; then
  if [[ "${TWINCUT_REQUIRE_TOOLS:-0}" == "1" ]]; then
    echo "FAIL: ffprobe required but missing" >&2; exit 1
  fi
  echo "keep_policy_smoke: K1 ok (SKIP K2/K3: ffprobe not installed)"; exit 0
fi

# --- K2. source similar-video equal-mtime tie-break ----------------------
# clip_high/clip_low differ in bytes (no hash dupe) but are similar-video
# candidates at SIZE_PCT=5. Byte-larger name created first (see K1 note).
src2="$work/k2"; mkdir -p "$src2"
cp "$HI" "$src2/zz_vid.mp4"
cp "$LO" "$src2/aa_vid.mp4"
touch -t 202601011200 "$src2/zz_vid.mp4" "$src2/aa_vid.mp4"

ev2="$work/k2.ndjson"
SIZE_PCT=5 DUR_SEC=0.3 TWINCUT_RUN_ID=r_keep_k2 \
  "$TC" --self-check "$src2" --include-similar-video --dry-run --json-events \
  >"$ev2" 2>"$work/k2.log" || fail "K2: self-check exited nonzero"
grep -q '"type":"dup_group"' "$ev2" || fail "K2: no dup_group emitted"
grep -q '"type":"dup_group".*"keep_path":"'"$src2"'/aa_vid.mp4"' "$ev2" \
  || fail "K2: equal-mtime similar-video keep is not the byte-smaller path"

# --- K3. backup similar-video equal-mtime tie-break ----------------------
bk3="$work/k3"; mkdir -p "$bk3"
cp "$HI" "$bk3/zz_vid.mp4"
cp "$LO" "$bk3/aa_vid.mp4"
touch -t 202601011200 "$bk3/zz_vid.mp4" "$bk3/aa_vid.mp4"

ev3="$work/k3.ndjson"
SIZE_PCT=5 DUR_SEC=0.3 TWINCUT_RUN_ID=r_keep_k3 \
  "$TC" --backup "$bk3" --report-backup-dupes --quarantine "$work/q3" \
  --json-events >"$ev3" 2>"$work/k3.log" \
  || fail "K3: backup self-check exited nonzero"
grep -q "BACKUP-SIMILAR" "$work/k3.log" || fail "K3: no similar-video pair found"
grep -q "keep='$bk3/aa_vid.mp4'" "$work/k3.log" \
  || fail "K3: equal-mtime similar-video keep is not the byte-smaller path"

echo "keep_policy_smoke: all ok"
```

- [ ] **Step 2: Run it — K2/K3 must fail (red)**

Run: `bash tests/keep_policy_smoke.sh`
Expected: `FAIL: K2: equal-mtime similar-video keep is not the byte-smaller path` (or the K3 variant). Pre-fix code keeps the outer-scan-loop file; because the byte-larger `zz_*` file is created first, creation-ordered filesystems (APFS) surface it first and keep it. K1 should already pass (Wave-3 fix, previously untested).
**If K2/K3 unexpectedly pass** (find order happened to match byte order on this filesystem): note it and proceed — these are determinism pins; CI's ext4 gives a second chance at catching pre-fix behavior. Do not contort the test further.

- [ ] **Step 3: Implement `pick_keep` and switch both similar-video sites**

In `bin/twincut.sh`, immediately above the `# qmove SRC DEST_DIR MATCHED HASH DECISION` comment block (~line 460), add:

```bash
# pick_keep A B — set KEEP/MOVE for a duplicate pair. Older mtime wins;
# equal mtimes fall back to LC_ALL=C path byte order (the same comparator
# as the hash-dupe MAP_KEYED/SMAP_KEYED sorts), NOT scan order — find(1)
# enumeration order is filesystem-dependent (ext4 htree vs APFS).
pick_keep(){
  local a="$1" b="$2" ma mb
  ma="$(mtime "$a")"; mb="$(mtime "$b")"
  if (( ma < mb )); then KEEP="$a"; MOVE="$b"; return 0; fi
  if (( mb < ma )); then KEEP="$b"; MOVE="$a"; return 0; fi
  if [[ "$(printf '%s\n%s\n' "$a" "$b" | LC_ALL=C sort | head -n1)" == "$a" ]]; then
    KEEP="$a"; MOVE="$b"
  else
    KEEP="$b"; MOVE="$a"
  fi
}
```

(Newline-containing paths would confuse the `printf | sort` comparator; that is the already-accepted "newline-in-path unsafe end-to-end" residual and such paths are refused by the TSV guards before any action.)

At `bin/twincut.sh:1326-1328`, replace:

```bash
          mt_bf=$(mtime "$bf")
          mt_cd=$(mtime "$cand")
          if (( mt_bf <= mt_cd )); then KEEP="$bf"; MOVE="$cand"; else KEEP="$cand"; MOVE="$bf"; fi
```

with:

```bash
          pick_keep "$bf" "$cand"
```

At `bin/twincut.sh:1537-1539` (inside the source-self `else` branch; keep the surrounding comment about oldest-keep), replace:

```bash
            mt_src=$(mtime "$f")
            mt_b=$(mtime "$b")
            if (( mt_src <= mt_b )); then KEEP="$f"; MOVE="$b"; else KEEP="$b"; MOVE="$f"; fi
```

with:

```bash
            pick_keep "$f" "$b"
```

- [ ] **Step 4: Widen the two keep-sort keys (hygiene)**

At `bin/twincut.sh:1256` and `bin/twincut.sh:1616`, change the sort key `-k2,2` → `-k2` (path key runs to end of line, matching the `cut -f2-` extraction below each sort):

```bash
        done < "$MAP_FILE" | LC_ALL=C sort -t "$(printf '\t')" -k1,1n -k2 > "$MAP_KEYED"
```

```bash
        done < "$SMAP_FILE" | LC_ALL=C sort -t "$(printf '\t')" -k1,1n -k2 > "$SMAP_KEYED"
```

(No behavior change is observable today — sort inputs are built from `awk '{print $2}'`-extracted paths that are already tab-free; this future-proofs the key against legacy cache rows and matches the extraction semantics.)

- [ ] **Step 5: Run the smoke and the full suite — green**

Run: `bash tests/keep_policy_smoke.sh`
Expected: `keep_policy_smoke: all ok`

Run: `make test`
Expected: bash JSON-events suite + Go tests + all smokes pass.

Run: `shellcheck bin/twincut.sh tests/keep_policy_smoke.sh`
Expected: no output (warning-clean).

- [ ] **Step 6: Wire the smoke into Makefile and CI**

In `Makefile`, `test-smoke` target, add after the `backup_selfcheck_smoke.sh` line:

```make
	@bash tests/keep_policy_smoke.sh
```

In `.github/workflows/ci.yml`, shell job, add after line 47 (`bash tests/backup_selfcheck_smoke.sh`), same indentation:

```yaml
          bash tests/keep_policy_smoke.sh
```

- [ ] **Step 7: Commit**

```bash
git add bin/twincut.sh tests/keep_policy_smoke.sh Makefile .github/workflows/ci.yml
git commit -m "fix: break equal-mtime similar-video keep ties by path byte order, not scan order"
```

---

### Task 2: TSV/path guard extension (matched column, hash-index writes, CR)

The Wave-3 tab/newline guard covers only the `src`(+`dir`) argument of `qmove`/`qdelete` (`bin/twincut.sh:468-473` and `516-521`). Three gaps, all recorded follow-ups:

1. The manifest `matched` column (arg 3 of `qmove`, arg 2 of `qdelete`) is written unguarded into the manifest TSV (`manifest_append`, column 5) — a tab/newline in the *keeper* path corrupts the row.
2. Hash-index writes are unguarded: a tab in a path produces a 3-field row that every `awk -F'\t' '$2'`/`NF>=2` reader mis-parses. Write sites: backup cache loop `bin/twincut.sh:1219-1220` (`LOCAL_CACHE` + `TMP_CACHE`), source cache `bin/twincut.sh:1425` and `1431` (`SOURCE_CACHE`), and the per-run source list `bin/twincut.sh:1437` (`SRC_HASH_RUN_FILE`).
3. `\r` is not treated as unsafe anywhere (a trailing CR is invisible in logs and breaks path round-trips through line-oriented consumers).

Fix shape: one predicate helper + fail-closed skips, mirroring the existing guard's behavior (warn `io_error` + stderr `[!] skip` + file left untouched + run continues).

**Files:**
- Modify: `bin/twincut.sh` (new helper next to `pick_keep` ~line 460; `qmove` ~468; `qdelete` ~516; backup cache loop ~1212; source loop ~1416)
- Test: `tests/p1_stage9_smoke.sh` (new sections D1c, D1d after D1b, before D2 ~line 192)
- Test: `tests/backup_selfcheck_smoke.sh` (new section at end, before the final `echo`)

**Interfaces:**
- Consumes: nothing from Task 1 (independent; both add helpers in the same region of `bin/twincut.sh` — if Task 1 already landed, place `tsv_path_safe` directly below `pick_keep`).
- Produces: `tsv_path_safe P` — returns 0 iff `P` contains no tab, newline, or CR. Used only within `bin/twincut.sh`.

- [ ] **Step 1: Write the failing tests**

**(a)** In `tests/p1_stage9_smoke.sh`, insert after the D1b section (after the `D1b: original source file left untouched` assert, ~line 191) and before `# === D2.`:

```bash
# === D1c. tab-in-KEEPER — the matched path is written into the manifest
# TSV `matched` column; the guard must refuse the move when the keeper
# (not just the src) cannot be represented in the TSV. ===
KTAB_NAME="$(printf 'keep%bafter' '\t')"
KTAB_SRC="$TMP/srcD1c"; mkdir -p "$KTAB_SRC"
cp "$SRC/keeper.jpg" "$KTAB_SRC/$KTAB_NAME.jpg"
cp "$SRC/unrelated_big.jpg" "$KTAB_SRC/victim.jpg"
KTAB_QUAR="$KTAB_SRC/_QUARANTINE/_thumbs"

APPLY_KTAB="$TMP/apply_ktab.ndjson"
jq -cn --arg src "$KTAB_SRC/victim.jpg" \
      --arg dst "$KTAB_QUAR" \
      --arg keep "$KTAB_SRC/$KTAB_NAME.jpg" \
  '{type:"apply_move", src:$src, dst_dir:$dst, keeper:$keep, decision:"thumb_l2_exif"}' \
  > "$APPLY_KTAB"

APPLY_KTAB_NDJSON="$TMP/apply_ktab_result.ndjson"
"$TWINCUT" --thumbnail-detect-apply --json-events --json-in --source "$KTAB_SRC" \
  >"$APPLY_KTAB_NDJSON" 2>/dev/null < "$APPLY_KTAB" || true

assert "D1c: tab-in-keeper apply emits no action kind=move (TSV guard blocks it)" \
  '[[ $(grep -c "\"type\":\"action\".*\"kind\":\"move\"" "$APPLY_KTAB_NDJSON") -eq 0 ]]'

assert "D1c: tab-in-keeper apply emits warn io_error" \
  '[[ $(grep -c "\"type\":\"warn\".*\"code\":\"io_error\"" "$APPLY_KTAB_NDJSON") -eq 1 ]]'

assert "D1c: tab-in-keeper: victim file left untouched" \
  '[[ -e "$KTAB_SRC/victim.jpg" ]]'

assert "D1c: tab-in-keeper apply ends with run_end status=succeeded" \
  'grep -q "\"type\":\"run_end\".*\"status\":\"succeeded\"" "$APPLY_KTAB_NDJSON"'

# === D1d. CR-in-path — carriage returns are invisible in logs and break
# path round-trips through line-oriented consumers; guard must refuse. ===
CR_NAME="$(printf 'cr%bafter' '\r')"
CR_SRC="$TMP/srcD1d"; mkdir -p "$CR_SRC"
cp "$SRC/keeper.jpg" "$CR_SRC/keeper.jpg"
cp "$SRC/unrelated_big.jpg" "$CR_SRC/$CR_NAME.jpg"
CR_QUAR="$CR_SRC/_QUARANTINE/_thumbs"

APPLY_CR="$TMP/apply_cr.ndjson"
jq -cn --arg src "$CR_SRC/$CR_NAME.jpg" \
      --arg dst "$CR_QUAR" \
      --arg keep "$CR_SRC/keeper.jpg" \
  '{type:"apply_move", src:$src, dst_dir:$dst, keeper:$keep, decision:"thumb_l2_exif"}' \
  > "$APPLY_CR"

APPLY_CR_NDJSON="$TMP/apply_cr_result.ndjson"
"$TWINCUT" --thumbnail-detect-apply --json-events --json-in --source "$CR_SRC" \
  >"$APPLY_CR_NDJSON" 2>/dev/null < "$APPLY_CR" || true

assert "D1d: CR-in-path apply emits no action kind=move (TSV guard blocks it)" \
  '[[ $(grep -c "\"type\":\"action\".*\"kind\":\"move\"" "$APPLY_CR_NDJSON") -eq 0 ]]'

assert "D1d: CR-in-path apply emits warn io_error" \
  '[[ $(grep -c "\"type\":\"warn\".*\"code\":\"io_error\"" "$APPLY_CR_NDJSON") -eq 1 ]]'

assert "D1d: CR-in-path: original source file left untouched" \
  '[[ -e "$CR_SRC/$CR_NAME.jpg" ]]'

assert "D1d: CR-in-path apply ends with run_end status=succeeded" \
  'grep -q "\"type\":\"run_end\".*\"status\":\"succeeded\"" "$APPLY_CR_NDJSON"'
```

**(b)** In `tests/backup_selfcheck_smoke.sh`, insert before the final `echo "backup_selfcheck_smoke: all ok"`:

```bash
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
```

- [ ] **Step 2: Run both — new sections must fail (red)**

Run: `bash tests/p1_stage9_smoke.sh`
Expected: D1c and D1d asserts FAIL (pre-fix code happily moves when only the keeper has a tab, and `\r` is not guarded at all).

Run: `bash tests/backup_selfcheck_smoke.sh`
Expected: FAIL at "expected hash-index skip warning" (pre-fix code writes the corrupt row silently).

- [ ] **Step 3: Implement `tsv_path_safe` + guard extensions**

In `bin/twincut.sh`, directly below `pick_keep` (Task 1) — or at ~line 460 if executing this task first — add:

```bash
# tsv_path_safe P — 0 iff P can be stored in the line-oriented TSV files
# (manifest, hash indexes): no tab, newline, or carriage return.
tsv_path_safe(){
  case "$1" in
    *$'\t'*|*$'\n'*|*$'\r'*) return 1 ;;
  esac
  return 0
}
```

In `qmove` (~line 468), replace the existing guard:

```bash
  case "$src$dir" in
    *$'\t'*|*$'\n'*)
      emit_warn --code io_error --path "$src" --detail "path contains tab/newline; unsafe for manifest TSV — skipped"
      echo "[!] skip (tab/newline in path): $src" >&2
      return 2 ;;
  esac
```

with:

```bash
  if ! tsv_path_safe "$src$dir$matched"; then
    emit_warn --code io_error --path "$src" --detail "path contains tab/newline/CR; unsafe for manifest TSV — skipped"
    echo "[!] skip (tab/newline/CR in path): $src" >&2
    return 2
  fi
```

In `qdelete` (~line 516), replace the existing guard:

```bash
  case "$src" in
    *$'\t'*|*$'\n'*)
      emit_warn --code io_error --path "$src" --detail "path contains tab/newline; unsafe for manifest TSV — skipped"
      echo "[!] skip (tab/newline in path): $src" >&2
      return 2 ;;
  esac
```

with:

```bash
  if ! tsv_path_safe "$src$matched"; then
    emit_warn --code io_error --path "$src" --detail "path contains tab/newline/CR; unsafe for manifest TSV — skipped"
    echo "[!] skip (tab/newline/CR in path): $src" >&2
    return 2
  fi
```

In the **backup cache loop** (~line 1212), after the `ALREADY_INDEXED_SET` reuse-check `continue` and before `H=$(hash_file "$f") || continue`, add:

```bash
    if ! tsv_path_safe "$f"; then
      emit_warn --code io_error --path "$f" --detail "path contains tab/newline/CR; unsafe for hash index — not indexed"
      echo "[!] skip (unsafe for hash index): $f" >&2
      continue
    fi
```

In the **main source loop** (~line 1416), after the AppleDouble block (`fi` closing `if ${IGNORE_APPLEDOUBLE:-true}`) and before the `# --- get/append source hash` comment, add:

```bash
  if ! tsv_path_safe "$f"; then
    emit_warn --code io_error --path "$f" --detail "path contains tab/newline/CR; unsafe for hash index — skipped"
    echo "[!] skip (unsafe for hash index): $f" >&2
    continue
  fi
```

(This skips such files from cross/self matching entirely. Net behavior is unchanged — the qmove/qdelete guard already refused to act on them — but the corrupt rows in `SOURCE_CACHE`/`SRC_HASH_RUN_FILE` and the wasted hash work are gone. The guard sits *after* the AppleDouble sidecar so `._*` handling is unaffected.)

- [ ] **Step 4: Run the tests — green**

Run: `bash tests/p1_stage9_smoke.sh`
Expected: all asserts pass, including D1/D1b (their `io_error` count-`eq`-1 asserts must still hold — the scan phase of `--thumbnail-detect-apply` may now emit an extra warn for the tab file; if a D1/D1b count assert breaks, inspect the NDJSON: the intended behavior is unchanged for D1/D1b — adjust only if the *new source-loop* warn genuinely double-fires in apply mode, and say so in the commit message).

Run: `bash tests/backup_selfcheck_smoke.sh`
Expected: `backup_selfcheck_smoke: all ok`

Run: `bash tests/p0_smoke.sh`
Expected: 28 passed. (p0 gained 3 TSV-guard tests in Wave 3; if any of them asserts on the old `tab/newline in path` stderr text or on warn counts, update those expectations to the new earlier-firing guard — the observable contract, skip + warn + run continues, is unchanged.)

Run: `make test`
Expected: green.

Run: `shellcheck bin/twincut.sh tests/backup_selfcheck_smoke.sh tests/p1_stage9_smoke.sh`
Expected: no output.

- [ ] **Step 5: Commit**

```bash
git add bin/twincut.sh tests/p1_stage9_smoke.sh tests/backup_selfcheck_smoke.sh
git commit -m "fix: extend TSV path guard to keeper column, hash-index writes, and CR"
```

---

### Task 3: stage9 D1/D1b assert `run_end` status

D1/D1b verify the guard blocks the move but never assert the run itself ends `succeeded` — the exact "crash with no run_end" class Wave 1 fixed elsewhere. (D1c/D1d from Task 2 already carry these asserts; this backfills the two originals.)

**Files:**
- Test: `tests/p1_stage9_smoke.sh` (D1 section ~line 159, D1b section ~line 191)

**Interfaces:** none (test-only; independent of Tasks 1-2).

- [ ] **Step 1: Verify current behavior, then add the asserts**

First confirm what the guard-skip run actually emits (characterization — expected `succeeded`, since the apply loop counts the skip and calls `emit_run_end --status succeeded` unconditionally at the end). Reproduce D1's invocation by hand against a scratch dir and inspect the `run_end` line:

```bash
T="$(mktemp -d)"; mkdir -p "$T/src"
printf 'X' > "$T/src/keeper.jpg"
tabn="$(printf 'tab%bafter' '\t').jpg"; printf 'Y' > "$T/src/$tabn"
jq -cn --arg src "$T/src/$tabn" --arg dst "$T/src/_QUARANTINE/_thumbs" \
      --arg keep "$T/src/keeper.jpg" \
  '{type:"apply_move", src:$src, dst_dir:$dst, keeper:$keep, decision:"thumb_l2_exif"}' \
  | bin/twincut.sh --thumbnail-detect-apply --json-events --json-in --source "$T/src" \
  2>/dev/null | grep run_end
rm -rf "$T"
```

Expected: one `run_end` event with `"status":"succeeded"`. If the status is anything other than `succeeded` (or `run_end` is missing), STOP and report — that would be a product bug, not a test gap.

In `tests/p1_stage9_smoke.sh`, after the `D1: tab-in-path: original source file left untouched` assert (~line 159), add:

```bash
assert "D1: tab-in-path apply ends with run_end status=succeeded" \
  'grep -q "\"type\":\"run_end\".*\"status\":\"succeeded\"" "$APPLY_TAB_NDJSON"'
```

After the `D1b: newline-in-path: original source file left untouched` assert (~line 191), add:

```bash
assert "D1b: newline-in-path apply ends with run_end status=succeeded" \
  'grep -q "\"type\":\"run_end\".*\"status\":\"succeeded\"" "$APPLY_NL_NDJSON"'
```

- [ ] **Step 2: Run the smoke — green**

Run: `bash tests/p1_stage9_smoke.sh`
Expected: all asserts pass, including the two new ones.

- [ ] **Step 3: Commit**

```bash
git add tests/p1_stage9_smoke.sh
git commit -m "test: stage9 D1/D1b assert run_end status=succeeded after guard skip"
```

---

### Task 4: gofmt the ui tree

`gofmt -l` currently flags 9 files under `ui/`: `main.go`, `server/apply_list.go`, `server/events.go`, `server/fs.go`, `server/recents.go`, `server/results.go`, `server/results_test.go`, `server/runs.go`, `server/selfcheck_test.go`. Pure formatting — no logic may change.

**Files:**
- Modify: the 9 files above (mechanical, via `gofmt -w`)

**Interfaces:** none.

- [ ] **Step 1: Format**

```bash
cd ui && gofmt -w .
```

- [ ] **Step 2: Verify formatting-only + tests green**

Run: `cd ui && gofmt -l .`
Expected: no output.

Run: `cd ui && git diff -w --stat`
Expected: empty or near-empty (gofmt changes are whitespace/alignment; any non-whitespace hunk means something is wrong — STOP and inspect).

Run: `cd ui && go vet ./... && go test ./... -count=1`
Expected: pass.

- [ ] **Step 3: Commit**

```bash
git add ui
git commit -m "style: gofmt ui tree (9 pre-existing unformatted files)"
```

---

## Wave close-out (leader, not a subagent task)

- `make test` on the branch tip; push; open PR referencing the four "Next up" items.
- Tier-1 review per TEAM.md §2 (leader Anthropic, subagents Anthropic ⇒ eligible {gemini, grok}; default `reviewer-gemini`, `reviewer-grok` on BLOCKED only with user authorization).
- Update PROGRESS.md Status Board (F-H1 → in-review/done), remove the four cleared items from "Next up", append Handoff Log entry.
