# P1 wave 2 — L1 perceptual hash (design spec)

**Status**: approved 2026-05-21, ready for implementation plan.
**Predecessors**: P1 wave 1 (commit `e61c018`), Stage 8 (PR #7 merged at `af5f2c1`), Stage 8.5 (PR #7 bundle).
**Successor**: Stage 9 (Go-owned contract redesign — out of scope here).

## Goal

Add a perceptual-hash (pHash) signal to L1 thumbnail detection so each L1 suspect can carry a `keeper` (likely big-image original) and a `group_id` in its `thumb_candidate` event. The Web UI then renders matched L1 suspects as proper groups (like L2/L3) instead of dumping them all into the synthetic `l1-suspects` bucket for human eyeball.

## Non-goals

- **No auto-move.** L1 stays a review tier. pHash adds pairing metadata; the human still confirms in the UI before any file is moved.
- **No new `decision` value.** Events still use `decision=thumb_l1_review`. Matched-vs-unmatched is distinguished by `keeper` being non-empty.
- **No CLI flag surface.** Tuning is via env knobs only (`THUMB_PHASH_*`). Wave 3 can add `--phash-*` flags if anyone asks.
- **No Stage 9 architecture work.** bash remains the detector; Go remains the renderer. The current event contract is preserved (one additive field — see §3).
- **No cross-source pHash (source ↔ backup).** Thumbnail-detect today is single-source. Cross-side pHash is a wave 3 / Stage 9 question.
- **No perf optimization.** Plain-Python Hamming compare is fast enough for the target scale (≤ 50k images). No numpy / BK-tree / concurrency.
- **No legacy `_review.csv` cleanup.** Stage 8.5 already routed L1 through NDJSON under `--json-events`; the on-disk legacy path is unchanged by this wave.

## Architecture

```
┌────────────────────────────────────────────────────────────┐
│ bin/twincut.sh ── dispatch (unchanged)                     │
└─────┬──────────────────────────────────────────────────────┘
      │ sources
┌─────▼──────────────────────────────────────────────────────┐
│ lib/thumb.sh                                               │
│   thumb_build_l1_index       (unchanged)                   │
│   thumb_build_l2_index → l2  (unchanged)                   │
│   thumb_run_l3               (unchanged)                   │
│   ▶ thumb_run_l1_phash       NEW — cache / batch / pair    │
│   thumb_write_review         CHANGED — emit keeper+group   │
└─────┬───────────────────────┬──────────────────────────────┘
      │ stdin: paths          │ read / write
      ▼                       ▼
┌──────────────┐    ┌──────────────────────────────────────┐
│ bin/phash.py │    │ <SOURCE_DIR>/.thumb_phash_index.tsv  │
│ leaf prim    │    │   header: # meta: algo=dhash …       │
│ Pillow +     │    │   rows:   path \t mtime \t size      │
│ imagehash    │    │                  \t phash_hex        │
└──────────────┘    └──────────────────────────────────────┘
```

**Responsibility split**:
- `bin/phash.py` is pure computation. Reads paths, emits hashes. Knows nothing about twincut. Independently testable with `cat paths.txt | bin/phash.py`.
- `thumb_run_l1_phash` does index maintenance, batch dispatch to `phash.py`, and pairing.
- Go side gets **one new optional field** (`phash_distance`) plus a routing tweak in `results.go` (matched L1 → its own group; unmatched → existing synthetic group).

**Failure posture**: any failure in the pHash phase (python missing, deps missing, helper exits non-zero, index file unwritable) prints a warning and skips the phase. L1 detection falls back to today's behavior (flat suspects in synthetic group). Detection never blocks on pHash.

## §1 — `bin/phash.py` contract

### Invocation

```bash
bin/phash.py --algo dhash --hash-size 8 < paths.txt > hashes.tsv
```

| Flag | Default | Notes |
|---|---|---|
| `--algo` | `dhash` | `dhash` \| `phash` (passes through to `imagehash`) |
| `--hash-size` | `8` | imagehash hash_size; 8 → 64-bit hash, 16 hex chars |
| `--null-in` | off | When set, stdin paths are NUL-separated (defends against newlines in paths) |

### Protocol

- **stdin**: one absolute path per line (or NUL-separated with `--null-in`).
- **stdout**: one `path\thash_hex` per input path, in input order. Failed paths are **omitted from stdout** (bash sees only successful rows).
- **stderr**: one `path\tERROR\t<short reason>` per failed path.
- **Order**: preserved.
- **Streaming**: lines emitted as computed (no buffering until end), so a slow batch makes progress visible.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | Ran to completion. Per-file errors went to stderr; the bash side counts them. |
| 2 | Argument / usage error (e.g., unknown `--algo`). |
| 3 | Required dependency missing (`Pillow` or `imagehash` failed to import). stderr names the missing module. |

### Implementation constraints

- **No argv path list**. 50k paths would blow `ARG_MAX`. stdin is mandatory.
- **No threading / multiprocessing in wave 2**. Single-threaded loop; simpler to reason about. Wave 3 can add `--workers` if profiling shows it's worth it.
- **Pillow lazy load**: open the file with `Image.open(path)`, compute hash, close. Don't preload bytes into memory.
- **Format guard**: catch `Pillow.UnidentifiedImageError`, `OSError` (truncated files), `Pillow.Image.DecompressionBombError`. Emit stderr ERROR row; continue.
- **No recursion / glob expansion**. Caller (bash) decides what to hash.

## §2 — Persistent index `<SOURCE_DIR>/.thumb_phash_index.tsv`

### Schema

```
# meta: algo=dhash hash_size=8 version=1 created=2026-05-21T18:00:00Z
/abs/path/to/big.jpg	1716326400	2456789	a3f2c1d8e7b69054
/abs/path/to/photo.heic	1716326500	5678901	b2e1f0c7a8d59663
…
```

- **Line 1** is a single `# meta:` comment containing `key=value` pairs space-separated.
- **Body**: TSV, 4 columns — `path`, `mtime_epoch`, `size_bytes`, `phash_hex`.
- Path is absolute. mtime is integer Unix epoch (no fractional seconds; matches `stat` granularity guarantee across platforms).

### Drift handling

Any change in the meta header → entire index discarded and rebuilt. Drift triggers:
- `algo` change (e.g., dhash → phash)
- `hash_size` change (e.g., 8 → 16)
- `version` change (reserved for future schema breaks)

Single-row drift (caller-driven, not meta-driven):
- File's `mtime` or `size` no longer matches the row → that file is re-hashed; row replaced.
- File no longer exists → row dropped (prune-on-miss).

### Coverage

Index contains:
- All L1=ok images (these are the keeper candidate pool).
- All L1=thumb / L1=maybe images still present after L2 + L3 ran (these are the suspects we'll search for keepers).

Index does **not** contain:
- Files in extensions outside `{jpg, jpeg, png, heic, heif, webp}`. (Matches imagehash-readable formats. `.gif/.bmp/.tif` are read by sips for L1 dim detection but aren't worth the pHash bytes.)
- Files moved by L2 or L3 (they no longer exist).

### Env knobs

| Var | Default | Notes |
|---|---|---|
| `THUMB_PHASH_ENABLED` | `true` | `false` skips the whole phase silently (no warn). |
| `THUMB_PHASH_HAMMING` | `5` | Match threshold. `0` = exact-hash-match only. Clipped to `[0, 64]` for `hash_size=8`. |
| `THUMB_PHASH_ALGO` | `dhash` | `dhash` (default) or `phash`. Drift-invalidates the index on change. |
| `THUMB_PHASH_INDEX` | `$SOURCE_DIR/.thumb_phash_index.tsv` | Override location if user wants index out of source tree. |

## §3 — Pairing, grouping, event shape

### `thumb_run_l1_phash` algorithm

```
1. Bail if THUMB_PHASH_ENABLED != true.
2. Probe: python3 in PATH + bin/phash.py readable. Warn + return on failure.
3. Load $THUMB_PHASH_INDEX into an in-memory map (path → {mtime, size, hash}).
   - Parse meta line; on drift, drop the in-memory map and mark for full rebuild.
   - Prune rows where the file no longer exists.
   - Mark rows where mtime/size differ from the live stat as stale.
4. Scan $THUMB_INDEX_FILE (built by thumb_build_l1_index). For each row:
   - L1=ok AND not in map (or marked stale) → enqueue for hash.
   - L1=thumb/maybe AND file still exists AND not in map (or stale) → enqueue.
5. Pipe the enqueue list to bin/phash.py. Read stdout into the in-memory map.
   Read stderr; print a summary of unreadable files.
6. Write the updated index back to disk (atomic rename via tempfile).
7. Pairing pass — for each L1=thumb/maybe row still on disk:
   a. Look up its hash in the in-memory map.
   b. Iterate L1=ok entries; compute hamming = popcount(suspect_hash XOR keeper_hash).
   c. Track the (keeper_path, distance) with the smallest distance that's still ≤ THUMB_PHASH_HAMMING.
   d. On ties (same distance, multiple keepers): lexicographic-smallest keeper_path wins.
   e. If a winner exists: append `keeper`, `group_id` to that row's in-memory representation.
8. Print a summary: hashed N, cache hits H, recomputed R, paired P/total_suspects.
```

### `$THUMB_INDEX_FILE` schema evolution

This file is in-memory / tempfile during a run; it carries L1 classification between phases.

| Today | Wave 2 |
|---|---|
| `path \t w \t h \t l1class` | `path \t w \t h \t l1class \t keeper \t group_id \t phash_distance` |

Columns 5–7 are empty for L1=ok rows and unmatched suspect rows.

### `group_id` derivation

```
group_id = "l1ph:" + sha1(keeper_abs_path)[:16]
```

- `l1ph:` prefix mirrors L3's `l3:` — Go treats them as opaque strings; the prefix exists for log readability.
- First 16 hex chars of SHA1 (= 64 bits) is more than enough collision resistance within a single source tree (collision odds at 50k keepers ≈ 1 in 10¹⁵).
- **Multiple suspects matching the same keeper get the same `group_id`** → on the Go side, the existing `EventThumbCandidate` branch already merges members under a matching `StringGroupID`. No new Go logic needed for merging.

### Event shape

**Unmatched L1 suspect** (no change from today):

```json
{"type":"thumb_candidate","ts":1700000010,"run_id":"r1",
 "decision":"thumb_l1_review",
 "path":"/src/small.jpg","reason":"l1_only_thumb",
 "width":200,"height":150,"size_bytes":12345}
```

**Matched L1 suspect** (new fields: `keeper`, `group_id`, `phash_distance`; `reason` takes a new value):

```json
{"type":"thumb_candidate","ts":1700000011,"run_id":"r1",
 "decision":"thumb_l1_review",
 "path":"/src/small.jpg","keeper":"/src/photo.jpg",
 "group_id":"l1ph:a3f2c1d8e7b69054",
 "reason":"l1_phash_match",
 "width":200,"height":150,"size_bytes":12345,
 "phash_distance":2}
```

**New event fields**:
- `phash_distance` (int) — Hamming distance between suspect and keeper. Lets the UI show confidence ("distance 2" is stronger than "distance 5"). Added to `ThumbCandidate` struct as `PhashDistance int \`json:"phash_distance,omitempty"\``.
- `reason="l1_phash_match"` — new enum value. The existing `l1_only_thumb` / `l1_only_maybe` values remain for unmatched suspects.

### Go-side changes (minimum set)

1. **`ui/server/events.go`** — `ThumbCandidate` struct adds:
   ```go
   PhashDistance int `json:"phash_distance,omitempty"`
   ```
   Doc comment on `Reason` field extends the enum description to mention `l1_phash_match`.

2. **`ui/server/results.go::EventThumbCandidate` branch** — replace the unconditional "all L1 → synthetic `l1-suspects` group" with:
   ```
   if tc.Decision == "thumb_l1_review" {
       if tc.GroupID == "" {
           // unmatched — synthetic group (existing behavior)
       } else {
           // matched — find/create group by tc.GroupID (mirror L2/L3 path)
       }
   }
   ```

3. **`ui/server/results.go::ResultMember`** — add `PhashDistance int` field (optional display).

4. **UI templates** (`ui/server/templates/...`) — render `PhashDistance` next to matched L1 members as a small "Hamming X" badge. Unmatched L1 visuals unchanged.

### Logged output

Green path (cold first run):
```
[*] thumbnail-detect L1-pHash: hashing 4823 images …
[*] thumbnail-detect L1-pHash: cache hits 0, recomputed 4823 (cold)
[*] thumbnail-detect L1-pHash: 47/89 suspects paired with keeper (Hamming ≤ 5)
```

Green path (warm re-run):
```
[*] thumbnail-detect L1-pHash: hashing 4 changed images …
[*] thumbnail-detect L1-pHash: cache hits 4819, recomputed 4 (cold or modified)
[*] thumbnail-detect L1-pHash: 47/89 suspects paired with keeper (Hamming ≤ 5)
```

Skip path:
```
[!] thumbnail-detect L1-pHash: bin/phash.py exited 3 (imagehash/Pillow not installed)
    install with: pip3 install --user pillow imagehash
    skipping pHash pairing — L1 suspects fall back to flat review.
```

## §4 — Installer, failure modes, testing

### Installer changes

**`installers/install.sh`** additions:

```sh
# (existing) symlink twincut and vid_eq

# new: symlink phash.py as leaf primitive
ln -sfn "$REPO/bin/phash.py" "$HOME/.local/bin/phash"

# new: best-effort python deps
if command -v pip3 >/dev/null 2>&1; then
  pip3 install --user --quiet pillow imagehash || \
    echo "[!] could not install pillow/imagehash; L1 pHash pairing will be skipped at runtime"
else
  echo "[!] pip3 not found; install python3 + run 'pip3 install --user pillow imagehash' for L1 pHash"
fi
```

**`installers/uninstall.sh`** additions:
- Remove `$HOME/.local/bin/phash` symlink.
- Do **not** pip-uninstall pillow/imagehash (the user may rely on them for other tooling).

**Runtime deps added to CLAUDE.md**: `python3 ≥ 3.8` (optional), `Pillow ≥ 9.0` (optional), `imagehash ≥ 4.3` (optional). All optional — twincut continues to work without them, just without L1 pHash pairing.

### Failure mode table

| Trigger | Behavior | User-visible |
|---|---|---|
| `python3` not in PATH | Skip pHash phase | `[!] L1 pHash skipped: python3 not found` |
| `bin/phash.py` not found | Skip pHash phase | `[!] L1 pHash skipped: bin/phash.py not found` |
| `imagehash`/`Pillow` missing | `phash.py` exits 3; bash skips | `[!] L1 pHash skipped: install pillow imagehash` |
| Per-file Pillow read fail | stderr `path\tERROR\t…`; bash counts and continues | `[*] pHash: 3 files unreadable, see warnings above` |
| Index file corrupt / missing `# meta:` | Treat as drift → full rebuild | `[*] pHash index rebuild (meta drift)` |
| Index row has non-numeric mtime/size | Treat that row as stale → re-hash | (silent) |
| `THUMB_PHASH_ENABLED=false` | Skip phase silently | `[*] L1 pHash disabled by env` |
| Pairing tie (same distance, multiple keepers) | Lexicographic-smallest keeper wins; deterministic | (silent) |
| Index file path unwritable | Warn + skip persistence (in-memory pairing still runs) | `[!] cannot write $THUMB_PHASH_INDEX (read-only?); skipping cache` |
| `THUMB_PHASH_HAMMING=0` | Exact-hash-match only | (normal) |
| `THUMB_PHASH_HAMMING > 64` (overflow for hash_size=8) | Clip to 64 + warn | `[!] THUMB_PHASH_HAMMING > 64, clipped to 64` |

### Test plan

**New file**: `tests/p1_thumb_phash_smoke.sh` (independent from `p1_thumb_smoke.sh`, which is already 54 sections and would become unreadable if extended).

**Fixture** (generated by the test itself using Pillow, no external downloads):

```
fixture/
├── photo_a_big.jpg     # 2000×1500, "scene A"
├── photo_a_small.jpg   # 200×150, downscaled from photo_a_big
├── photo_b_big.jpg     # 2000×1500, "scene B"
├── photo_b_thumb1.jpg  # 300×225, downscaled from photo_b_big
├── photo_b_thumb2.jpg  # 150×113, downscaled from photo_b_big (different size)
├── orphan_small.png    # 100×100, no big counterpart
└── unrelated_big.jpg   # 2000×1500, totally different scene
```

**Sections** (final count to be set by the implementation plan; outline below):

1. **Fixture setup** — generate the seven test images via a Python one-liner using Pillow. Sanity-check dimensions with `sips`.
2. **Happy path: matched pair** — run twincut thumbnail-detect; assert the `thumb_candidate` event for `photo_a_small.jpg` carries `keeper=photo_a_big.jpg`, `group_id=l1ph:…`, `reason=l1_phash_match`, `phash_distance ≤ 5`.
3. **Multi-thumb same keeper merges** — assert both `photo_b_thumb1.jpg` and `photo_b_thumb2.jpg` emit events with the **same** `group_id`.
4. **Orphan stays flat** — assert `orphan_small.png` emits with empty `keeper`, empty `group_id`, `reason=l1_only_thumb` (or `l1_only_maybe`, depending on dims).
5. **Index file created with `# meta:` header** — assert `$SOURCE/.thumb_phash_index.tsv` exists, line 1 starts with `# meta:`, contains `algo=dhash`.
6. **Second run uses cache** — re-run; assert log line shows `recomputed 0` (or similar) and pairing result is identical.
7. **mtime invalidation** — `touch` one fixture file; re-run; assert exactly that one row is re-hashed (count: recomputed=1).
8. **Delete invalidation** — `rm` a fixture file; re-run; assert that file's row is no longer in the index.
9. **Meta drift triggers full rebuild** — manually edit `algo=dhash` to `algo=phash` in the header; re-run; assert full rebuild log line.
10. **`THUMB_PHASH_ENABLED=false`** — re-run with env; assert no `[*] L1-pHash:` log lines; assert index file is **not** created (or is left untouched if pre-existing); L1 suspects fall back to flat synthetic group.
11. **Simulated dep failure** — re-run with `PATH` stripped of `python3` (or with a fake `phash.py` that exits 3); assert the skip warning fires; assert L1 suspects fall back; assert run exits 0 (no detection failure).
12. **Legacy CLI** — run without `--json-events`; assert pHash phase still runs (it's not gated on event mode); assert the legacy `_review.csv` is written as today (no schema change in that file).

**Go test additions**:

- `ui/server/events_test.go` — `TestUnmarshalThumbCandidate_L1WithPhashFields`: a thumb_candidate event with `keeper`, `group_id`, `phash_distance` parses correctly; `PhashDistance` is read.
- `ui/server/results_test.go`:
  - `TestBuildResults_L1Phash_MatchedGoesToOwnGroup` — matched L1 lands in its `l1ph:…` group, not in `l1-suspects`.
  - `TestBuildResults_L1Phash_UnmatchedStaysInSyntheticGroup` — unmatched L1 still hits the synthetic group.
  - `TestBuildResults_L1Phash_MultipleSuspectsShareKeeper` — two events with the same `group_id` produce one group with two members.

### Performance budget (informational, no enforced limit)

- Per-image hash: dhash@8 ≈ 5–10 ms in Pillow + imagehash on modern hardware. Pillow open dominates (~50 ms for a 2000×1500 JPEG).
- Cold first run, 50k images: ≈ 8–12 min wall-clock single-threaded. Acceptable as one-time cost.
- Warm re-run: only modified files re-hash. Typically < 5 s for incremental change.
- Pairing: 100 suspects × 50k keepers = 5M Hamming compares in pure Python ≈ 1–2 s.

These are informational. No perf-regression test gates them in wave 2.

## Decision log

| # | Question | Answer | Rationale |
|---|---|---|---|
| 1 | What is L1's behavior model post wave 2? | **B — pair-only, no auto-move** | Conservative; preserves Stage 8.5's "L1 = review tier" invariant. |
| 2 | What's the pHash search scope? | **B — whole SOURCE_DIR tree** | Catches cross-directory exports; requires a persistent index but the index pays for itself on warm re-runs. |
| 3 | Which pHash implementation? | **A — Python helper + Pillow + imagehash** | Industry-standard algorithms, fast, clean stdin/stdout protocol; one new pip dep is acceptable. |
| 4 | Multiple suspects matching same keeper — how to group? | **A — merge into one group with shared keeper** | Mirrors L2 EXIF clustering; matches user mental model ("these are all thumbs of the same photo"). |

## Open questions deferred to implementation

These are concrete enough that the implementation plan will pin them, but they don't change the design:

- **HEIF in pHash**: imagehash + Pillow's HEIF support varies by platform. The implementation plan should test on a HEIC fixture; if Pillow can't read it, emit ERROR and continue (already handled by the failure mode table).
- **Atomic index write**: write to `<index>.tmp` then `mv -f` to the real path; the implementation plan locks the exact tempfile pattern.
- **Index file in `.gitignore`**: should be added to `.gitignore` if it isn't already covered by a broader rule.

## Out-of-scope follow-ups (post-wave-2)

These came up during brainstorming and are explicitly deferred:

- **`--phash-*` CLI flags** — only if env knobs prove insufficient.
- **Auto-move on very-high confidence (e.g., Hamming = 0)** — would change L1 from "review only" to a tiered model; warrants its own brainstorm.
- **pHash across source ↔ backup (cross-check use case)** — wave 3 / Stage 9.
- **numpy / BK-tree / multiprocessing** — only if profiling shows a real bottleneck.
- **Drop legacy on-disk `_review.csv` path** — independent cleanup, not bound to wave 2.
- **UI visual differentiation for L1 vs L2/L3 groups** — current shared rendering is acceptable; a colored / iconned variant can land later.

## References

- Stage 8.5 spec (replay safety, manifest keeper, TOCTOU fix): `docs/superpowers/specs/2026-05-21-twincut-stage8.5-p0-hygiene-design.md`
- Stage 8 follow-up (architectural backlog): `docs/superpowers/specs/2026-05-21-twincut-stage8-followup.md`
- L1/L2/L3 implementation today: `lib/thumb.sh`
- NDJSON event schema (Go side): `ui/server/events.go`
- L1 routing in BuildResults: `ui/server/results.go` `EventThumbCandidate` branch
- Existing index file precedents: `<source>/.video_meta_index.csv`, `<backup>/.backup_hashindex.txt`
