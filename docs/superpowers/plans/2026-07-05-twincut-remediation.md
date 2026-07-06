# twincut Remediation Implementation Plan (post-assessment, no new features)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix every verified defect from the 2026-07-05 full-repo assessment — broken similar-video matching, crash-class bash bugs, Go UI security/panic gaps, and CI drift — without adding any new feature or touching the in-flight UI work.

**Architecture:** Three independent PR waves. Wave 1 repairs the bash matching engine (`bin/vid_eq.sh`, `bin/twincut.sh`, `lib/thumb.sh`) with new regression smokes. Wave 2 hardens the Go UI (`ui/server/`). Wave 3 closes CI/test drift and bash hygiene. Every fix lands test-first: reproduce → verify red → fix → verify green → commit.

**Tech Stack:** bash (must stay **bash 3.2 compatible** — macOS default; no associative arrays, no `${var,,}`), awk, ffprobe/ffmpeg, Go ≥ go.mod version, GitHub Actions, shellcheck.

## Global Constraints

- **No new features.** Only repair documented-but-broken behavior, remove dead code, harden, and test.
- **bash 3.2 compatibility** everywhere in `bin/*.sh`, `lib/*.sh`, `tests/*.sh` (no `declare -A`, no `${var^^}`/`${var,,}`, no `readarray`).
- **GNU/BSD portability:** every `stat` call needs the `-c`-then-`-f` fallback (use the existing `mtime`/`fsize` helpers in `bin/twincut.sh:227-228`).
- **Event contract is Go-owned** (Stage 11): any change to NDJSON shapes must go through `ui/server/events.go` + `tests/fixtures/events/` + `tests/events_contract.sh`. This plan does NOT change any event shape.
- **No new runtime dependencies.**
- **Repo handoff protocol** (CLAUDE.md "Agent-Team Peer Handoff" block): before starting, run `agent-team handoff-check`, read `PROGRESS.md`, claim the task there (owner + in-progress), and append a Handoff Log entry before stopping.
- **Branch/PR/review convention:** one branch + PR per wave (`<handle>/remediation-wave1..3`). Each PR is >50 changed lines of business logic ⇒ **Tier-1 cross-family review required** (default `reviewer-gemini`, `reviewer-grok` fallback) before merge, per `~/.agent-team/TEAM.md` §2. Never merge on a BLOCKED review result.
- **Baseline check before starting each wave:** `make test` must pass (currently: bash 12/12 + Go ok).

## Verified-fact appendix (used by test assertions below)

- `tests/fixtures/video/clip_high.mp4`: 37555 bytes, 3.000s, h264, 320x240.
- `tests/fixtures/video/clip_low.mp4`: 35913 bytes, 3.000s, h264, 320x240.
- Size delta between them: 1642 B ≈ **4.37%** of clip_high ⇒ rejected at SIZE_PCT=0.5, accepted at SIZE_PCT=5.
- `ffprobe -show_entries format=size,duration` prints **duration first, then size** (canonical field order, not request order). This caused bug A1.
- `ffprobe -show_entries stream=codec_name,width,height -of default=nw=1:nk=1` prints **three lines**; a single `read` consumes only the first. This caused bug A1b (w/h never compared).
- `read var < <(cmd-with-empty-output)` returns 1; under `set -e` this **kills the whole script**. This caused bug A3/A4.
- `shellcheck --severity=error` on all four shell files currently exits 0; `--severity=warning` reports 16 findings (mostly SC2034 dead vars).

---

# Wave 1 — Matching-engine correctness (branch `<handle>/remediation-wave1`)

### Task 1: Rewrite `bin/vid_eq.sh` (field swap, w/h compare, EQUAL mode, env propagation)

**Files:**
- Rewrite: `bin/vid_eq.sh` (whole file, ~70 lines)
- Modify: `bin/twincut.sh:1091` (add `export SIZE_PCT DUR_SEC` after strict re-apply)
- Modify: `bin/twincut.sh:786-787` (usage text: describe full mode as metadata-level)
- Create: `tests/vid_eq_smoke.sh`
- Modify: `.github/workflows/ci.yml` (shell-tests job: add the new smoke)
- Modify: `CLAUDE.md` (architecture notes: strict-mode sentence)

**Interfaces:**
- Consumes: `SIZE_PCT` / `DUR_SEC` env vars (twincut exports them after this task).
- Produces (contract relied on by `bin/twincut.sh:1251-1256,1469-1474`):
  - `vid_eq.sh --fast A B` → prints exactly `CANDIDATE:yes` (exit 0) or `CANDIDATE:no` (exit 1).
  - `vid_eq.sh A B` (no `--fast`) → prints exactly `EQUAL:yes` (exit 0) or `EQUAL:no` (exit 1). **Default mode must be full/EQUAL** — twincut's strict path calls it bare and greps `EQUAL:yes`.
  - Exit 2 + stderr message when either file is missing.

Background (why): the old script (a) read ffprobe's duration/size output into swapped variables, so the "duration window" compared byte sizes — similar-video only ever matched byte-identical files; (b) read only line 1 of the 3-line codec/w/h output, so resolution was never compared; (c) never printed `EQUAL:yes`, so `--video-fast-strict` confirmed zero pairs; (d) `FAST_MODE` was parsed but unused; (e) `SIZE_PCT`/`DUR_SEC` were not exported by twincut, so `--size-pct` never reached this helper.

- [ ] **Step 1: Write the failing smoke test**

Create `tests/vid_eq_smoke.sh` (executable, `chmod +x`):

```bash
#!/usr/bin/env bash
# tests/vid_eq_smoke.sh — contract test for bin/vid_eq.sh fast/full modes.
# Guards against the 2026-07 field-swap regression (duration/size read into
# swapped vars => similar-video only matched byte-identical files).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VE="$ROOT/bin/vid_eq.sh"
TC="$ROOT/bin/twincut.sh"
HI="$ROOT/tests/fixtures/video/clip_high.mp4"   # 37555 B, 3.0s, h264 320x240
LO="$ROOT/tests/fixtures/video/clip_low.mp4"    # 35913 B, 3.0s, h264 320x240

fail(){ echo "FAIL: $*" >&2; exit 1; }

if ! command -v ffprobe >/dev/null 2>&1; then
  if [[ "${TWINCUT_REQUIRE_TOOLS:-0}" == "1" ]]; then
    echo "FAIL: ffprobe required but missing" >&2; exit 1
  fi
  echo "SKIP: ffprobe not installed"; exit 0
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# 1. Same file twice → fast candidate.
out="$("$VE" --fast "$HI" "$HI")" || fail "self-compare exited nonzero"
[[ "$out" == "CANDIDATE:yes" ]] || fail "self-compare: got '$out'"

# 2. 4.37% size delta → rejected at default 0.5%, accepted at 5%.
out="$("$VE" --fast "$HI" "$LO" || true)"
[[ "$out" == "CANDIDATE:no" ]] || fail "default SIZE_PCT should reject 4.37% delta: got '$out'"
out="$(SIZE_PCT=5 "$VE" --fast "$HI" "$LO")" || fail "SIZE_PCT=5 exited nonzero"
[[ "$out" == "CANDIDATE:yes" ]] || fail "SIZE_PCT=5 should accept: got '$out'"

# 3. Resolution mismatch → rejected even with a huge size window.
scaled="$work/scaled.mp4"
if command -v ffmpeg >/dev/null 2>&1; then
  ffmpeg -y -loglevel error -i "$HI" -vf scale=160:120 -t 3 "$scaled"
  out="$(SIZE_PCT=99 "$VE" --fast "$HI" "$scaled" || true)"
  [[ "$out" == "CANDIDATE:no" ]] || fail "resolution mismatch should reject: got '$out'"
fi

# 4. Full mode (bare call): EQUAL label, honors env.
out="$(SIZE_PCT=5 "$VE" "$HI" "$LO")" || fail "full mode exited nonzero"
[[ "$out" == "EQUAL:yes" ]] || fail "full SIZE_PCT=5: got '$out'"
out="$("$VE" "$HI" "$LO" || true)"
[[ "$out" == "EQUAL:no" ]] || fail "full default should reject: got '$out'"

# 5. End-to-end: twincut similar-video actually detects a non-byte-identical
#    pair (this is the assertion that fails on the pre-fix field swap).
src="$work/src"; mkdir -p "$src"
cp "$HI" "$src/a.mp4"; cp "$LO" "$src/b.mp4"
events="$work/events.ndjson"
SIZE_PCT=5 DUR_SEC=0.3 TWINCUT_RUN_ID=r_videq_e2e \
  "$TC" --self-check "$src" --include-similar-video --dry-run --json-events \
  >"$events" 2>"$work/stderr.log" || fail "twincut e2e exited nonzero (see $work/stderr.log)"
grep -q '"type":"dup_group"' "$events" || fail "e2e: no dup_group emitted"
grep -q '"match_reason":"video_fast"' "$events" || fail "e2e: no video_fast match"

echo "vid_eq_smoke: all ok"
```

- [ ] **Step 2: Run it to verify it fails against current code**

Run: `bash tests/vid_eq_smoke.sh`
Expected: `FAIL: SIZE_PCT=5 should accept: got 'CANDIDATE:no'` (assertion 2 — the swapped duration-slack check demands byte-equal sizes). If it fails even earlier that is also acceptable red.

- [ ] **Step 3: Rewrite `bin/vid_eq.sh`**

Replace the entire file with:

```bash
#!/usr/bin/env bash
# vid_eq.sh — metadata-level video equivalence check for twincut.
#
# Modes:
#   --fast (fast prefilter)  → prints CANDIDATE:yes|no, exit 0|1
#   default (full check)     → prints EQUAL:yes|no,     exit 0|1
#
# "Full" is a stricter-threshold METADATA verification (size window,
# duration window, codec, WxH). This tool never decodes frames.
# twincut.sh --video-fast-strict calls the bare form and greps EQUAL:yes,
# so the default mode MUST stay full/EQUAL.
#
# Env knobs (twincut.sh exports these so --size-pct/--dur-sec propagate):
#   SIZE_PCT  size window in percent   (default 0.5, matches twincut)
#   DUR_SEC   duration slack in seconds (default 0.3, matches twincut)
set -euo pipefail
export LC_ALL=C

SIZE_PCT=${SIZE_PCT:-0.5}
DUR_SEC=${DUR_SEC:-0.3}
MODE="full"

_fsize(){ stat -c %s -- "$1" 2>/dev/null || stat -f %z -- "$1" 2>/dev/null || echo 0; }
# One value per call — avoids depending on ffprobe's canonical field order,
# which bit us before (duration prints before size regardless of request order).
_probe_dur(){ ffprobe -v error -show_entries format=duration -of default=nw=1:nk=1 -- "$1" 2>/dev/null | head -n1; }
# codec,width,height on ONE line via csv (a 3-line default=nw=1 output would
# need three reads; one read used to silently drop width/height).
_probe_cwh(){ ffprobe -v error -select_streams v:0 -show_entries stream=codec_name,width,height -of csv=p=0:s=, -- "$1" 2>/dev/null | head -n1; }

vid_eq(){
  local A="$1" B="$2"
  [[ -f "$A" && -f "$B" ]] || { echo "file missing" >&2; return 2; }
  local sa sb da db ca cb label
  sa="$(_fsize "$A")"; sb="$(_fsize "$B")"
  da="$(_probe_dur "$A")"; db="$(_probe_dur "$B")"
  ca="$(_probe_cwh "$A")"; cb="$(_probe_cwh "$B")"
  [[ "$da" =~ ^[0-9]+(\.[0-9]+)?$ ]] || da=0
  [[ "$db" =~ ^[0-9]+(\.[0-9]+)?$ ]] || db=0
  label="EQUAL"; [[ "$MODE" == "fast" ]] && label="CANDIDATE"
  awk -v sa="$sa" -v sb="$sb" -v da="$da" -v db="$db" \
      -v ca="$ca" -v cb="$cb" -v pct="$SIZE_PCT" -v dslack="$DUR_SEC" -v label="$label" '
    function abs(x){return x<0?-x:x}
    BEGIN{
      ok = (sa+0 > 0) \
        && (abs(sa-sb) <= (pct/100.0)*sa) \
        && (abs(da-db) <= dslack) \
        && (ca != "") && (ca == cb);
      print label (ok ? ":yes" : ":no");
      exit ok ? 0 : 1;
    }'
}

if [[ ${BASH_SOURCE[0]} == "$0" ]]; then
  ARGS=()
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --fast) MODE="fast"; shift;;
      --size-pct) SIZE_PCT="$2"; shift 2;;
      --dur-sec) DUR_SEC="$2"; shift 2;;
      *) ARGS+=("$1"); shift;;
    esac
  done
  if [[ ${#ARGS[@]} -ne 2 ]]; then
    echo "Usage: $(basename "$0") [--fast] [--size-pct N] [--dur-sec SEC] <fileA> <fileB>" >&2
    exit 2
  fi
  vid_eq "${ARGS[0]}" "${ARGS[1]}"
fi
```

- [ ] **Step 4: Export the env knobs from twincut.sh**

In `bin/twincut.sh`, directly after the strict re-apply line (currently line 1091, `if $VIDEO_FAST_STRICT; then SIZE_PCT=0.2; DUR_SEC=0.15; fi`), add:

```bash
# vid_eq.sh runs as a child process; export so --size-pct/--dur-sec (and the
# strict-tightened values above) actually reach it.
export SIZE_PCT DUR_SEC
```

- [ ] **Step 5: Update the two doc mentions of the "full vid_eq check"**

In `bin/twincut.sh` usage text (line ~787), change:
`  - In --video-fast-strict, join also compares fps/bitrate (if present) and does a full vid_eq check.`
to:
`  - In --video-fast-strict, join also compares fps/bitrate (if present) and re-verifies via vid_eq's metadata-level EQUAL check.`

In `CLAUDE.md`, in the video-matching tier list, change "runs `vid_eq.sh` for a final check" to "re-verifies via `vid_eq.sh`'s metadata-level EQUAL check".

- [ ] **Step 6: Run the smoke to verify green**

Run: `bash tests/vid_eq_smoke.sh`
Expected: `vid_eq_smoke: all ok`

- [ ] **Step 7: Run the full existing suite (no regressions)**

Run: `make test && bash tests/p0_smoke.sh && bash tests/p1_stage11_smoke.sh`
Expected: bash 12/12, Go ok, both smokes ok.

- [ ] **Step 8: Wire the smoke into CI**

In `.github/workflows/ci.yml`, shell-tests job, add to the run block after the stage11 line:

```yaml
          bash tests/vid_eq_smoke.sh
```

- [ ] **Step 9: Commit**

```bash
git add bin/vid_eq.sh bin/twincut.sh tests/vid_eq_smoke.sh .github/workflows/ci.yml CLAUDE.md
git commit -m "fix(vid_eq): correct ffprobe field order, compare WxH, implement EQUAL mode, export SIZE_PCT/DUR_SEC

Similar-video previously only matched byte-identical sizes (duration/size
read into swapped vars) and --video-fast-strict could never confirm a pair
(EQUAL:yes was never emitted). Adds contract + e2e smoke."
```

---

### Task 2: Kill the `read < <(empty)` crash class + backup self-check guard order

**Files:**
- Modify: `bin/twincut.sh:197-198, 1222-1247, 1423-1441, 1500`
- Modify: `lib/thumb.sh:177`
- Create: `tests/backup_selfcheck_smoke.sh`
- Modify: `.github/workflows/ci.yml` (shell-tests job: add the new smoke)

**Interfaces:**
- Consumes: nothing new.
- Produces: `--report-backup-dupes` / `--fix-backup-dupes` complete successfully (exit 0, SUMMARY printed, `run_end` emitted) on directories containing non-video files.

Background (why): `read vars < <(awk lookup)` returns 1 when the lookup misses; under `set -euo pipefail` this kills the whole script with exit 1, no message, no `run_end` — verified live. Worst case: the backup self-check loop does a video-meta lookup (line 1223) **before** the `is_video_ext` guard (line 1239), so any jpg/mp3 in the backup dir crashes the run. All the `[[ -z "$var" ]]` fallback branches after these reads are currently unreachable dead paths.

- [ ] **Step 1: Write the failing smoke**

Create `tests/backup_selfcheck_smoke.sh` (executable):

```bash
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

echo "backup_selfcheck_smoke: all ok"
```

- [ ] **Step 2: Run it to verify it fails**

Run: `bash tests/backup_selfcheck_smoke.sh`
Expected: `FAIL: backup self-check exited nonzero ...` (crash at the meta lookup for a.jpg).

- [ ] **Step 3: Reorder the guard in the backup similar-video loop**

In `bin/twincut.sh`, inside the `while IFS= read -r -d '' bf` loop (starts line ~1220): move the line `is_video_ext "$bf" || continue` (currently line ~1239) to the **top of the loop body**, before the `if ${BAD_VIDEO_DETECT:-true}; then` block. Result shape:

```bash
    while IFS= read -r -d '' bf; do
      is_video_ext "$bf" || continue

      if ${BAD_VIDEO_DETECT:-true}; then
        ...
```

(The cross/source path at line 1421 already checks `is_video_ext` first — this makes the backup path consistent.)

- [ ] **Step 4: Make every lookup-read miss-tolerant**

Append ` || true` to each of these `read ... < <(...)` statements (exact lines may shift ±3 after Step 3; match on content):

- `bin/twincut.sh:197` `read -r _kdur _kw _kh _kfps _kbps < <(_video_meta_lookup "$_kcsv" "$_keep")`
- `bin/twincut.sh:198` `read -r _ddur _dw _dh _dfps _dbps < <(_video_meta_lookup "$_rcsv" "$_rm")`
- `bin/twincut.sh:1223` `read s_bad < <( awk ... )` (backup bad-video lookup)
- `bin/twincut.sh:1226` `read bcod bw bh bdur < <( awk ... )`
- `bin/twincut.sh:1241` `read bsz bdur bcod bw bh bmt bdb < <( awk ... )`
- `bin/twincut.sh:1244` `read bsz bdur bcod bw bh bmt bdb < <( awk ... "$VMETA_FILE" )`
- `bin/twincut.sh:1247` `read bfps bbps < <( awk ... )`
- `bin/twincut.sh:1423-1425` `read s_size s_dur s_codec s_w s_h s_mtime s_dbuck < <( ... )` (append after the closing `)`)
- `bin/twincut.sh:1433-1435` same shape, second occurrence
- `bin/twincut.sh:1438` `read s_fps s_bps < <( awk ... )`
- `bin/twincut.sh:1441` `read s_bad < <( awk ... )` (source bad-video lookup)
- `lib/thumb.sh:177` `read -r _ w h _ < <(awk -F'\t' -v pp="$p" '$1==pp{print $0; exit}' "$THUMB_INDEX_FILE")`

Example result: `read s_bad < <( awk -F'\t' -v p="$bf" 'NR>2 && $1==p {print $11; exit}' "${VMETA_FILE:-/dev/null}" ) || true`

Note: `read`'s failure still leaves the variables **set to empty**, which is exactly what the pre-existing `[[ -z "${var:-}" ]]` fallback branches expect — this change makes those fallbacks reachable for the first time.

- [ ] **Step 5: Fix the seen-pair dedup to skip, not abandon**

`bin/twincut.sh:1500`: in the source-self similar-video branch, change

```bash
            case ":${_SOURCE_SIM_SEEN:-}:" in *":${_pkey}:"*) break ;; esac
```

to

```bash
            # Pair already handled from the other side — skip this candidate
            # but keep scanning the rest (break abandoned the remaining
            # candidates for $f entirely).
            case ":${_SOURCE_SIM_SEEN:-}:" in *":${_pkey}:"*) continue ;; esac
```

- [ ] **Step 6: Run the smoke to verify green**

Run: `bash tests/backup_selfcheck_smoke.sh`
Expected: `backup_selfcheck_smoke: all ok`

- [ ] **Step 7: Full regression pass**

Run: `make test && bash tests/p0_smoke.sh && bash tests/p1_stage9_smoke.sh && bash tests/p1_stage11_smoke.sh && bash tests/vid_eq_smoke.sh`
Expected: everything green.

- [ ] **Step 8: Wire the smoke into CI**

In `.github/workflows/ci.yml`, shell-tests job run block, add:

```yaml
          bash tests/backup_selfcheck_smoke.sh
```

- [ ] **Step 9: Commit**

```bash
git add bin/twincut.sh lib/thumb.sh tests/backup_selfcheck_smoke.sh .github/workflows/ci.yml
git commit -m "fix(engine): survive video-meta lookup misses under set -e; guard order in backup self-check

read < <(empty) returns 1 and killed the whole run (no run_end, exit 1)
whenever a meta lookup missed — trivially triggered by any non-video file
in --report/--fix-backup-dupes. Adds || true at all 12 lookup sites (making
the existing self-heal fallbacks reachable), moves the is_video_ext guard
above the bad-video check, and fixes seen-pair dedup to continue instead of
break. Adds regression smoke."
```

- [ ] **Step 10: Open PR for Wave 1 + request Tier-1 review**

```bash
git push -u origin <handle>/remediation-wave1
gh pr create --title "Wave 1: matching-engine correctness (vid_eq rewrite + crash-class fix)" --body "..."
```
Then dispatch `reviewer-gemini` (Tier-1) on the PR diff. Address findings before merge. Update `PROGRESS.md` Status Board + Handoff Log.

---

# Wave 2 — Go UI security & robustness (branch `<handle>/remediation-wave2`)

### Task 3: Origin/Host guard middleware (CSRF / DNS-rebinding)

**Files:**
- Modify: `ui/server/http.go` (add middleware; wrap mux in `Handler()`)
- Create: `ui/server/origin_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `func originGuard(next http.Handler) http.Handler` (package-private), applied to the entire mux. `Server.Handler()` return value changes from bare `*http.ServeMux` to the wrapped handler (signature `http.Handler` — unchanged).

Background (why): the server binds 127.0.0.1 but performs **no** Origin or Host validation. `POST /api/runs` accepts arbitrary argv for bash, `/api/*/apply` moves files, `/api/open` spawns `open`. Any webpage in the user's browser can fire `fetch("http://127.0.0.1:7681/...", {method:"POST", mode:"no-cors", ...})` (Go's json.Decoder ignores Content-Type), and DNS rebinding defeats the loopback assumption. Policy: reject non-loopback `Host`; for state-changing methods reject any present non-loopback `Origin` (absent Origin stays allowed so curl/CLI keep working).

- [ ] **Step 1: Write the failing tests**

Create `ui/server/origin_test.go`:

```go
package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func guardedEcho(t *testing.T) http.Handler {
	t.Helper()
	return originGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
}

func TestOriginGuardAllowsLoopbackGET(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "http://127.0.0.1:7681/tab/self-check", nil)
	guardedEcho(t).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("loopback GET: got %d, want 200", rr.Code)
	}
}

func TestOriginGuardRejectsForeignHost(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "http://evil.example.com/tab/self-check", nil)
	req.Host = "evil.example.com"
	guardedEcho(t).ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("foreign Host: got %d, want 403", rr.Code)
	}
}

func TestOriginGuardRejectsForeignOriginOnPOST(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "http://127.0.0.1:7681/api/runs", strings.NewReader("{}"))
	req.Header.Set("Origin", "https://evil.example.com")
	guardedEcho(t).ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("foreign Origin POST: got %d, want 403", rr.Code)
	}
}

func TestOriginGuardAllowsLoopbackOriginOnPOST(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "http://127.0.0.1:7681/api/runs", strings.NewReader("{}"))
	req.Header.Set("Origin", "http://127.0.0.1:7681")
	guardedEcho(t).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("loopback Origin POST: got %d, want 200", rr.Code)
	}
}

func TestOriginGuardAllowsAbsentOriginOnPOST(t *testing.T) {
	// curl / CLI clients send no Origin; must keep working.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "http://localhost:7681/api/runs", strings.NewReader("{}"))
	guardedEcho(t).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("absent Origin POST: got %d, want 200", rr.Code)
	}
}
```

- [ ] **Step 2: Run to verify compile failure**

Run: `cd ui && go test ./server/ -run TestOriginGuard -v`
Expected: FAIL — `undefined: originGuard`.

- [ ] **Step 3: Implement the middleware**

In `ui/server/http.go`, add imports `net`, `net/url`, and:

```go
// loopbackHost reports whether host (no port) is a loopback name we serve.
func loopbackHost(host string) bool {
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

// originGuard rejects (a) requests whose Host is not loopback (DNS-rebinding
// defense — we only ever bind 127.0.0.1) and (b) state-changing requests
// bearing a non-loopback Origin (CSRF defense; browsers attach Origin to
// cross-site POSTs, while curl/CLI send none and stay allowed).
func originGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if !loopbackHost(host) {
			http.Error(w, "forbidden: non-loopback Host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if o := r.Header.Get("Origin"); o != "" {
				u, err := url.Parse(o)
				if err != nil || !loopbackHost(u.Hostname()) {
					http.Error(w, "forbidden: cross-origin request", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}
```

At the end of `Handler()` (http.go:133), change `return mux` to `return originGuard(mux)`.

- [ ] **Step 4: Run tests to verify green**

Run: `cd ui && go test ./server/ -run TestOriginGuard -v`
Expected: 5/5 PASS.

- [ ] **Step 5: Full Go suite**

Run: `cd ui && go test ./...`
Expected: PASS (existing handler tests use httptest with loopback hosts; if any test constructs a foreign Host and now 403s, fix the test's request host to `127.0.0.1` — the production behavior is the spec here).

- [ ] **Step 6: Commit**

```bash
git add ui/server/http.go ui/server/origin_test.go
git commit -m "fix(ui): reject non-loopback Host and cross-origin state-changing requests

POST /api/runs executes bash with caller-controlled argv; without an
Origin/Host guard any webpage could CSRF the localhost server (and DNS
rebinding defeats the 127.0.0.1 bind). Absent-Origin requests stay allowed
so curl keeps working."
```

---

### Task 4: Go fixes — nil-deref panic, apply preview validation, healthz JSON, stderr drain, dead code

**Files:**
- Modify: `ui/server/history.go:237-243`
- Modify: `ui/server/selfcheck.go` (handleSelfCheckApply, ~line 133; delete handleTabPlaceholder ~line 273-281)
- Modify: `ui/server/crosscheck.go` (handleCrossCheckApply, ~line 127)
- Modify: `ui/server/http.go:160-163` (healthz)
- Modify: `ui/server/runs.go:422-428` (drainStderr split)
- Modify: `ui/server/events.go:241-244` (delete IsTerminal)
- Modify: `ui/server/twincut.go` (delete ErrTwincutNotFound)
- Modify: `ui/server/thumbnail.go:45` (drop duplicated `--json-events` — RunManager.Start prepends it)
- Test: `ui/server/history_test.go`, `ui/server/selfcheck_test.go`, `ui/server/crosscheck_test.go`, `ui/server/runs_test.go`

**Interfaces:**
- Consumes: `RunStatusRunning/Succeeded` consts (runs.go), `writeJSON` (http.go), existing `SetTestSpawnHook` seam (testutil_test.go).
- Produces: no exported-surface changes; `IsTerminal` and `ErrTwincutNotFound` are removed (verified: zero callers in repo).

- [ ] **Step 1: Failing test — history preview must not panic on a zero-move run**

Add to `ui/server/history_test.go`, using this package's existing helpers — `newHistoryTestServer(t, stateDir)`, `writeNDJSON(t, path, lines...)`, direct handler calls with `req.SetPathValue` (see `TestHandleHistoryTab_RendersEntries` at history_test.go:218 for the arrangement style). The manifest must live under `$HOME` to pass `IsAllowedPath` — `t.TempDir()` resolves under /var/folders which is outside the allowlist (this constraint is documented at history_test.go:174-176):

```go
func TestHandleHistoryPreview_ZeroMoveRunReturns404NotPanic(t *testing.T) {
	state := t.TempDir()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	scratch, err := os.MkdirTemp(home, ".twincut-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(scratch) })
	manifest := filepath.Join(scratch, "_manifest-r1.tsv")
	if err := os.WriteFile(manifest, []byte("# twincut manifest v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Journal: apply run whose run_end has a manifest but moved=0 →
	// resolveManifest succeeds, loadHistoryEntry returns ok=false, err=nil.
	// The old code called err.Error() on that path and panicked.
	writeNDJSON(t, filepath.Join(state, "runs", "r1.ndjson"),
		`{"type":"run_start","ts":1,"run_id":"r1","mode":"self_check","source":"`+scratch+`","dry_run":false}`,
		`{"type":"run_end","ts":2,"run_id":"r1","status":"succeeded","moved":0,"manifest_path":"`+manifest+`"}`,
	)

	s := newHistoryTestServer(t, state)
	req := httptest.NewRequest("GET", "/history/r1/preview", nil)
	req.SetPathValue("id", "r1")
	w := httptest.NewRecorder()
	s.handleHistoryPreview(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404 (and no panic)", w.Code)
	}
}
```

- [ ] **Step 2: Run to verify it panics/fails**

Run: `cd ui && go test ./server/ -run TestHistoryPreviewZeroMove -v`
Expected: FAIL with a nil-pointer panic in `handleHistoryPreview`.

- [ ] **Step 3: Fix the handler**

`ui/server/history.go`, replace lines 239-243:

```go
	entry, ok, err := loadHistoryEntry(ndjsonPath)
	if err != nil {
		http.Error(w, "load history entry: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "run has nothing to restore", http.StatusNotFound)
		return
	}
```

- [ ] **Step 4: Failing tests — apply endpoints must validate the preview run**

Add to `ui/server/selfcheck_test.go` (and the analogous pair to `crosscheck_test.go` posting to `/api/cross-check/apply` with args `{"--source", src, "--backup", bk, "--dry-run"}` and wrong-mode `"self_check_preview"`). Reuse the in-package helpers that `thumbnail_test.go` already established: `newThumbTestServer(t)` (any Server-constructing helper in the package works — it only needs templates + a RunManager), `runFromEvents(t, lines)`, `storeRun(srv.runs, id, r)`. Reference behavior: `thumbnail.go:110-121`.

```go
func TestHandleSelfCheckApply_RejectsWrongModePreview(t *testing.T) {
	srv := newThumbTestServer(t)
	srcPath := os.Getenv("HOME")
	r := runFromEvents(t, []string{
		`{"type":"run_start","ts":1,"run_id":"prev-wrongmode","mode":"thumbnail_detect_preview","source":"` + srcPath + `"}`,
		`{"type":"run_end","ts":2,"run_id":"prev-wrongmode","cancelled":false}`,
	})
	r.Mode = "thumbnail_detect_preview" // not a self_check_preview
	r.Args = []string{"--thumbnail-detect", "--source", srcPath, "--dry-run"}
	storeRun(srv.runs, "prev-wrongmode", r)

	form := url.Values{"preview_run_id": {"prev-wrongmode"}}
	req := httptest.NewRequest("POST", "/api/self-check/apply", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.handleSelfCheckApply(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("wrong-mode preview: got %d, want 422", w.Code)
	}
}

func TestHandleSelfCheckApply_RejectsRunningPreview(t *testing.T) {
	srv := newThumbTestServer(t)
	srcPath := os.Getenv("HOME")
	// Construct a Run directly (in-package, same idiom as thumbnail_test.go)
	// so we can pin status to running.
	r := &Run{
		ID:     "prev-running",
		Mode:   "self_check_preview",
		Args:   []string{"--self-check", srcPath, "--dry-run"},
		status: RunStatusRunning,
		done:   make(chan struct{}),
	}
	storeRun(srv.runs, "prev-running", r)

	form := url.Values{"preview_run_id": {"prev-running"}}
	req := httptest.NewRequest("POST", "/api/self-check/apply", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.handleSelfCheckApply(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("running preview: got %d, want 409", w.Code)
	}
}
```

(If `runFromEvents`/`storeRun`/`newThumbTestServer` live unexported in `thumbnail_test.go`, they are already visible package-wide to these new tests — do not duplicate them. Adjust the `*Run` literal's field names only if the struct differs; check `runs.go:35-51`.)

- [ ] **Step 5: Implement validation in both apply handlers**

In `ui/server/selfcheck.go` `handleSelfCheckApply`, after the `previewRun == nil` check (line ~137), insert (and reuse `prevSnap` for the later `extractArgValue` call instead of calling `Snapshot()` twice):

```go
	prevSnap := previewRun.Snapshot()
	if prevSnap.Mode != "self_check_preview" {
		http.Error(w, "preview_run_id refers to a non-self-check-preview run (mode="+prevSnap.Mode+")", http.StatusUnprocessableEntity)
		return
	}
	if prevSnap.Status == RunStatusRunning {
		http.Error(w, "preview run is still in progress; wait for it to finish before applying", http.StatusConflict)
		return
	}
	if prevSnap.Status != RunStatusSucceeded {
		http.Error(w, "preview run did not succeed (status="+string(prevSnap.Status)+"); cannot apply", http.StatusUnprocessableEntity)
		return
	}
	folder, ok := extractArgValue(prevSnap.Args, "--self-check")
```

Mirror in `ui/server/crosscheck.go` `handleCrossCheckApply` with `"cross_check_preview"` and `prevSnap.Args` feeding the existing `extractArgValue/extractArgValues` calls.

- [ ] **Step 6: healthz via writeJSON**

`ui/server/http.go:160-163`, replace `handleHealth` body:

```go
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "twincut": s.opts.TwincutPath})
}
```

- [ ] **Step 7: drainStderr — split on \r as well as \n**

`ui/server/runs.go`: in `--json-events` mode twincut's progress goes to stderr as `\r`-separated text with no newline until phase end; the Scanner's 256KB line cap can overflow on huge scans, the drain goroutine exits, the pipe fills, and the child blocks forever. Add import `bytes` and:

```go
// scanCRorLF splits on \r or \n so twincut's \r progress spinner can't
// accumulate into one giant "line" and overflow the scanner (which would
// stop draining stderr and eventually deadlock the child on a full pipe).
func scanCRorLF(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func drainStderr(runID string, stderr io.ReadCloser) {
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 4096), 256*1024)
	scanner.Split(scanCRorLF)
	for scanner.Scan() {
		if line := scanner.Text(); line != "" {
			log.Printf("[%s/stderr] %s", shortID(runID), line)
		}
	}
}
```

Add a unit test in `ui/server/runs_test.go`:

```go
func TestScanCRorLFSplitsCarriageReturns(t *testing.T) {
	in := "a\rbb\rccc\nlast"
	sc := bufio.NewScanner(strings.NewReader(in))
	sc.Split(scanCRorLF)
	var got []string
	for sc.Scan() {
		got = append(got, sc.Text())
	}
	want := []string{"a", "bb", "ccc", "last"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
```

- [ ] **Step 8: Delete dead code + duplicate flag**

- `ui/server/events.go:241-244`: delete `IsTerminal` (zero callers; also semantically wrong — bash emits `error` events mid-run and continues).
- `ui/server/twincut.go`: delete `ErrTwincutNotFound` (zero callers) and its `errors` import if now unused.
- `ui/server/selfcheck.go:273-281`: delete `handleTabPlaceholder` (zero callers) and the `fmt` import if now unused.
- `ui/server/thumbnail.go:45`: change `args := []string{"--thumbnail-detect", "--source", source, "--dry-run", "--json-events"}` to drop `"--json-events"` (RunManager.Start prepends it — currently the child gets the flag twice). **Check** `thumbnail_test.go` for SpawnHook assertions on this handler's argv and update the expected slice if present (the literals at thumbnail_test.go:175 etc. construct Run fixtures directly and are unaffected).

- [ ] **Step 9: Full Go suite green**

Run: `cd ui && go test ./...`
Expected: PASS. `go vet ./...` clean.

- [ ] **Step 10: Commit + PR + review**

```bash
git add ui/server/
git commit -m "fix(ui): history nil-deref, apply preview validation, healthz JSON, CR-aware stderr drain, dead code

- handleHistoryPreview panicked (err.Error() on nil) for zero-move runs
- self-check/cross-check apply now validate preview mode+status like
  thumbnails apply already did
- healthz emitted raw string-concat JSON
- drainStderr now splits on \r so progress spam can't overflow the scanner
  and deadlock the child on a full stderr pipe
- remove dead IsTerminal / ErrTwincutNotFound / handleTabPlaceholder,
  drop duplicated --json-events in thumbnail preview argv"
git push -u origin <handle>/remediation-wave2
gh pr create --title "Wave 2: Go UI security & robustness" --body "..."
```
Dispatch `reviewer-gemini` Tier-1 on the PR. Update `PROGRESS.md`.

---

# Wave 3 — CI/test drift + bash hygiene (branch `<handle>/remediation-wave3`)

### Task 5: CI runs the full suite; Makefile gets `test-smoke`; orphan test adopted

**Files:**
- Modify: `.github/workflows/ci.yml` (shell-tests job)
- Modify: `Makefile`
- Modify: `tests/legacy_event_ts_seam.sh:1-5` (stale header comment only)

**Interfaces:**
- Produces: `make test` ≙ CI shell coverage (single source of truth for "green").

Background (why): CI never runs `tests/json_events/run_tests.py` — the 12-case suite that is the core of `make test` — so regressions it guards pass CI. Conversely `make test` skips the 4 smoke suites. `tests/legacy_event_ts_seam.sh` is run by nobody and its header still describes the `emit_event` helper deleted in Stage 11 (it actually tests the `TWINCUT_TEST_TS` seam end-to-end through the real script, which the unit-level `events_contract.sh` does not — keep it, adopt it).

- [ ] **Step 1: Add the json-events suite + seam test to CI**

`.github/workflows/ci.yml` shell-tests job run block — final state (includes the two smokes added by Tasks 1-2):

```yaml
        run: |
          python3 tests/json_events/run_tests.py
          bash tests/events_contract.sh
          bash tests/legacy_event_ts_seam.sh
          bash tests/p1_stage9_smoke.sh
          bash tests/p0_smoke.sh
          bash tests/p1_stage11_smoke.sh
          bash tests/vid_eq_smoke.sh
          bash tests/backup_selfcheck_smoke.sh
```

- [ ] **Step 2: Makefile `test-smoke` target**

In `Makefile`: add `test-smoke` to `.PHONY`, extend `test`, and append the target:

```make
test: test-script test-go test-smoke

test-smoke:
	@bash tests/events_contract.sh
	@bash tests/legacy_event_ts_seam.sh
	@bash tests/p0_smoke.sh
	@bash tests/p1_stage9_smoke.sh
	@bash tests/p1_stage11_smoke.sh
	@bash tests/vid_eq_smoke.sh
	@bash tests/backup_selfcheck_smoke.sh
```

Also add a help line: `@echo "  test-smoke      run shell smoke suites"`.

(The sips-dependent `p1_thumb_smoke.sh` / `p1_thumb_phash_smoke.sh` stay out of `test-smoke` deliberately — they self-skip off-macOS and CI covers them on the macos job; listing them locally is fine on a Mac but keep parity with the linux CI list.)

- [ ] **Step 3: Fix the stale header of the seam test**

`tests/legacy_event_ts_seam.sh` lines 1-5: replace the comment block with:

```bash
#!/usr/bin/env bash
# tests/legacy_event_ts_seam.sh — end-to-end check that the real twincut.sh
# honors the TWINCUT_TEST_TS seam on its first emitted event (run_start).
# Complements tests/events_contract.sh, which exercises the lib/events.sh
# emitters in isolation rather than through the full CLI entry path.
```

- [ ] **Step 4: Verify locally**

Run: `make test`
Expected: test-script 12/12, Go ok, all 7 smoke suites ok.

- [ ] **Step 5: Commit**

```bash
git add .github/workflows/ci.yml Makefile tests/legacy_event_ts_seam.sh
git commit -m "ci: run json-events suite + all smokes in CI; add make test-smoke; adopt orphaned seam test"
```

---

### Task 6: bash hygiene — dead code, trap cleanup, deduplication, ext-list alignment

**Files:**
- Modify: `bin/twincut.sh` (multiple sites, listed per step)
- Modify: `lib/thumb.sh`, `lib/events.sh`
- Test: existing suites (`make test`) — hygiene must be behavior-neutral except where noted.

**Interfaces:**
- Produces: `vmeta_write_header FILE` and `vmeta_join_candidates META SELF CODEC W H DBUCK SIZE FPS BPS` helper functions in `bin/twincut.sh` (used only internally).

- [ ] **Step 1: Delete dead code**

- `bin/twincut.sh:106-110`: delete the pre-parser strict block (`if ${VIDEO_FAST_STRICT:-false}; then SIZE_PCT=0.2; DUR_SEC=0.15; fi` and its comment) — it runs before the CLI parser and can never fire; line ~1091 is the live copy.
- `bin/twincut.sh:710-714`: delete `is_bad_video_row()` (zero callers).
- `bin/twincut.sh:18`: delete `USE_CACHE=true`. `bin/twincut.sh:929`: change `--use-cache) USE_CACHE=true; REBUILD_CACHE=false; shift;;` to `--use-cache) REBUILD_CACHE=false; shift;;` (the variable was never read; the flag's real effect is un-setting a rebuild).
- `lib/thumb.sh:483` and `:529,:553`: delete every `THUMB_PHASH_LIVE_INDEX=…` assignment and the "Expose the live index" comment (consumers read the temp files directly; the variable is never read).

- [ ] **Step 2: Temp-file cleanup trap**

`bin/twincut.sh:47`, after `TMP_CACHE="$(mktemp)"`, add:

```bash
trap 'rm -f "$TMP_CACHE"' EXIT
```

(Loop-local mktemps are already rm'd inline; this covers the one unconditional leak plus every early-exit path.)

- [ ] **Step 3: Extract the duplicated vmeta header writer**

Add near the other small helpers (after `duration_bucket`, ~line 613):

```bash
# vmeta_write_header FILE — (re)create a video-meta TSV with meta + column header.
vmeta_write_header(){
  printf '# vmeta: size_pct=%s; dur_sec=%s; created=%s\n' \
    "$SIZE_PCT" "$DUR_SEC" "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" > "$1"
  printf 'path\tsize\tduration\tcodec\twidth\theight\tmtime\tdur_bucket\tfps\tbitrate\tbad\n' >> "$1"
}
```

Replace both creation sites with `vmeta_write_header "$vcsv"` / `vmeta_write_header "$SVMETA_FILE"`:
- `ensure_video_meta_index` (~lines 660-663; note the `meta=` local and the `printf/echo -e` pair collapse into the helper call — keep the separate "prepend header if missing" branch at 664-666 as-is, it can also call the helper into a tempfile then append the old body).
- the inline block at ~lines 1428-1431.

- [ ] **Step 4: Extract the duplicated candidate-join awk**

The two ~20-line awk programs at `bin/twincut.sh:1277-1297` and `:1517-1537` are identical except the source-side program excludes the probe path (`$1!=sp`). Add helper:

```bash
# vmeta_join_candidates META SELF CODEC W H DBUCK SIZE FPS BPS
# Prints candidate paths from META matching codec/WxH/duration-bucket and the
# SIZE_PCT window; in strict mode additionally filters by fps/bitrate windows.
# SELF (may be empty) is excluded from candidates.
vmeta_join_candidates(){
  local meta="$1" self="$2" cc="$3" w="$4" h="$5" db="$6" ssz="$7" sfps="$8" sbps="$9"
  awk -F'\t' -v cc="$cc" -v w="$w" -v h="$h" -v db="$db" -v spct="$SIZE_PCT" -v ssz="$ssz" \
      -v strict="$VIDEO_FAST_STRICT" -v sfps="$sfps" -v sbps="$sbps" -v sp="$self" \
      -v fps_pct="$FPS_PCT" -v fps_min="$FPS_ABS_MIN" -v bps_pct="$BPS_PCT" '
    NR<=2{next}
    (sp=="" || $1!=sp) && $4==cc && $5==w && $6==h && $8==db {
      bsz=$2+0; diff=(bsz>ssz?bsz-ssz:ssz-bsz)
      ok=(diff*100 <= spct*ssz)
      if (ok && strict=="true") {
        cfps=$9+0;
        if (sfps!="" && cfps>0) {
          fps_tol = (sfps>0 ? sfps*(fps_pct/100.0) : fps_min);
          if (fps_tol < fps_min) fps_tol=fps_min;
          d=cfps-sfps; if (d<0) d=-d;
          if (d > fps_tol) ok=0;
        }
        cbps=$10+0;
        if (sbps!="" && cbps>0 && sbps+0>0) {
          rel = (cbps>sbps ? cbps-sbps : sbps-cbps) / sbps;
          if (rel > (bps_pct/100.0)) ok=0;
        }
      }
      if (ok) print $1
    }' "$meta"
}
```

Replace the backup-side process substitution (`done < <( awk … "$VMETA_FILE" )` at ~1276-1298) with:

```bash
      done < <( vmeta_join_candidates "$VMETA_FILE" "$bf" "$bcod" "$bw" "$bh" "$bdb" "$bsz" "$bfps" "$bbps" )
```

and the source-side one (~1516-1538) with:

```bash
      done < <( vmeta_join_candidates "$CAND_META_FILE" "$f" "$s_codec" "$s_w" "$s_h" "$s_dbuck" "$s_size" "$s_fps" "$s_bps" )
```

(Passing `$bf` as SELF on the backup side is a strict improvement — the bash loop's `[[ "$cand" == "$bf" ]] && continue` stays as belt-and-suspenders.)

- [ ] **Step 5: thumb.sh uses the shared stat helpers**

`lib/thumb.sh:388-389` and `:442-443`: replace the four inline `stat -c … || stat -f …` calls with the helpers that twincut.sh (which sources this lib) already defines:

```bash
    _live_mt="$(mtime "$_f")"
    _live_sz="$(fsize "$_f")"
```
```bash
      _mt2="$(mtime "$_p2")"
      _sz2="$(fsize "$_p2")"
```

Note: `mtime`/`fsize` echo `0` on failure instead of empty — so change the guards that followed (`[[ -z "$_live_mt" || -z "$_live_sz" ]] && continue` at :390 and the `_mt2/_sz2` one at :444) to `[[ "$_live_mt" == 0 || "$_live_sz" == 0 ]] && continue` (and respectively for `_mt2/_sz2`).

- [ ] **Step 6: Align EXTS with VIDEO_EXTS**

`bin/twincut.sh:16`: extensions in `VIDEO_EXTS` but missing from `EXTS` (`m4v,3gp,mts,m2ts,hevc,h265`) are indexed into the video-meta TSV but **never scanned** as source/backup files — an asymmetry, not a choice. Change EXTS to:

```bash
EXTS="jpg,jpeg,png,dng,mp4,mov,m4v,avi,mkv,webm,3gp,mts,m2ts,hevc,h265,mp3,wav,flac,aac,ogg,m4a,heic,heif,rmvb"
```

Note for the PR description: hash caches embed `exts=` in their `# meta:` header, so existing caches will report param drift on next run (auto-rebuild when non-interactive / `--assume-yes`; prompt otherwise). That is `should_rebuild_cache` working as designed.

- [ ] **Step 7: Reject TSV-breaking paths at the move boundary**

In `bin/twincut.sh` `qmove()` (after the `is_excluded` check, ~line 473) add:

```bash
  case "$src$dir" in
    *$'\t'*|*$'\n'*)
      emit_warn --code io_error --path "$src" --detail "path contains tab/newline; unsafe for manifest TSV — skipped"
      echo "[!] skip (tab/newline in path): $src" >&2
      return 2 ;;
  esac
```

and the same block (with `$src` only) at the top of `qdelete()` after its `is_excluded` check. (A tab in a path previously corrupted the manifest row silently, breaking `--restore` for that file.)

- [ ] **Step 8: events.sh comment + tiny lints**

- `lib/events.sh:9-10`: change "Unknown args are fatal (return 2)." to "Unknown args log to stderr and the event is dropped (return 0 — an emit bug must never kill a run)."
- `lib/events.sh:489-511` (`emit_progress`): rename the local `done` to `done_cnt` (its `--done` case arm currently trips shellcheck SC1010) — update the two uses at :507.
- `lib/events.sh:416` (SC2155): split `local o='…'` declare-and-assign only if shellcheck still flags it after the other edits; otherwise leave.

- [ ] **Step 9: Full regression pass**

Run: `make test` (now includes all smokes per Task 5)
Expected: all green. Also spot-check strict mode still works end-to-end:

```bash
SIZE_PCT=5 bash tests/vid_eq_smoke.sh   # env override must not break assertions 2/5's defaults
```
(Expected: `vid_eq_smoke: all ok` — the script sets its own SIZE_PCT per assertion.)

- [ ] **Step 10: Commit**

```bash
git add bin/twincut.sh lib/thumb.sh lib/events.sh
git commit -m "chore(hygiene): remove dead code, add tmp trap, dedup vmeta helpers, align EXTS, guard TSV-breaking paths"
```

---

### Task 7: shellcheck — warning-clean + CI gate

**Files:**
- Modify: `bin/twincut.sh`, `lib/thumb.sh` (targeted `# shellcheck disable` directives for false positives)
- Modify: `.github/workflows/ci.yml` (new job)

Background: `--severity=error` is already clean; `--severity=warning` had 16 findings. Task 6 removes the genuinely-dead variables; what remains are false positives (globals consumed across `source` boundaries) and unused read placeholders.

- [ ] **Step 1: Run shellcheck and fix the remainder**

Run: `shellcheck --severity=warning bin/twincut.sh bin/vid_eq.sh lib/events.sh lib/thumb.sh installers/install.sh installers/uninstall.sh`

Expected leftovers and their fixes:
- `THUMB_ACTION/THUMB_DIR/THUMB_MAX_EDGE/THUMB_MAYBE_MAX_EDGE/THUMB_REQUIRE_EXIF_MATCH/THUMB_REVIEW_CSV/THUMB_DETECT` flagged SC2034 in twincut.sh (they are consumed by the sourced `lib/thumb.sh`): add one directive line above the thumb CLI-parse block (~line 988):
  ```bash
  # shellcheck disable=SC2034  # consumed by lib/thumb.sh (sourced above)
  ```
  If shellcheck only honors per-line directives here, place the directive on each flagged assignment line instead.
- `QUAR_DIR` SC2034 in `lib/thumb.sh:655` (assignment consumed by the sourcing script): same treatment with comment `# consumed by bin/twincut.sh (sourcing script)`.
- Unused read placeholders `bmt` (twincut.sh:1241/1244 reads), `s_mtime` (:1423/:1433), `cw ch` (thumb.sh:257): these are positional placeholders in multi-field reads; add `# shellcheck disable=SC2034` on those read lines with comment `# positional TSV placeholders`.

- [ ] **Step 2: Verify clean**

Run: `shellcheck --severity=warning bin/twincut.sh bin/vid_eq.sh lib/events.sh lib/thumb.sh installers/install.sh installers/uninstall.sh; echo "exit=$?"`
Expected: `exit=0`, no output.

- [ ] **Step 3: Add the CI job**

`.github/workflows/ci.yml`, new top-level job:

```yaml
  shellcheck:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: shellcheck (warning severity)
        run: |
          sudo apt-get update -qq && sudo apt-get install -y -qq shellcheck
          shellcheck --severity=warning \
            bin/twincut.sh bin/vid_eq.sh \
            lib/events.sh lib/thumb.sh \
            installers/install.sh installers/uninstall.sh \
            tests/*.sh
```

Note `tests/*.sh` is included — run locally first and fix/disable any test-script findings the same way.

- [ ] **Step 4: Final full pass + commit + PR + review**

Run: `make test && shellcheck --severity=warning bin/*.sh lib/*.sh installers/*.sh tests/*.sh`
Expected: all green, shellcheck silent.

```bash
git add bin/ lib/ tests/ .github/workflows/ci.yml
git commit -m "ci: shellcheck gate at warning severity (with annotated false-positive directives)"
git push -u origin <handle>/remediation-wave3
gh pr create --title "Wave 3: CI/test drift + bash hygiene + shellcheck gate" --body "..."
```
Dispatch `reviewer-gemini` Tier-1. Update `PROGRESS.md` Status Board + Handoff Log.

---

## Explicitly deferred (out of scope for this plan — do NOT do these now)

Documented so nobody "helpfully" scope-creeps mid-execution:

1. **O(N²) membership loops** (`grep -Fqx` per file in cache reuse at twincut.sh:1152/1375, vmeta prune at :670-674, thumb.sh Step B per-file awk at :394): a real fix wants associative arrays, which bash 3.2 forbids; the sort/comm restructure is invasive. Revisit only if a real library (>50k files) hurts.
2. **Double directory walk** for TOTAL_B/TOTAL_SRC counting (:1131, :1348).
3. **vid_eq re-probing ffprobe per candidate pair** instead of reusing the vmeta TSV.
4. **thumb_embed_md5 memoization** (thumb.sh:262) — re-extracts the same keeper's embedded thumb per suspect.
5. **Thumb cache pruning** (`~/.twincut-ui/cache/thumbs` grows unboundedly).
6. **CLI missing-value guard** (`--source` as last arg dies silently via `shift 2` under set -e) — mechanical but touches ~40 parser arms.
7. **`_SOURCE_SIM_SEEN` colon-in-path edge** in the seen-pair key encoding.
8. **Run/event memory eviction** in the Go RunManager (unbounded per-process; fine for a local single-user tool).

## Execution order & dependencies

- Wave 1 (Tasks 1→2) has no dependency on Waves 2/3. **Do it first** — it fixes user-facing data-loss-adjacent behavior.
- Wave 2 (Tasks 3→4) is independent of Wave 1.
- Wave 3 (Tasks 5→6→7 **in that order**: Task 5's CI list references Wave 1's smokes, so Wave 1 must be merged first; Task 7's clean gate depends on Task 6's dead-code removal).
- Each wave: branch → tasks → PR → Tier-1 `reviewer-gemini` → merge → `git-sync`.
