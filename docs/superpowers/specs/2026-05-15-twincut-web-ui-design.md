# twincut Web UI — Design

**Date:** 2026-05-15
**Status:** Approved (brainstorm complete)
**Author:** Yiqi Huang (with Claude)
**Branch:** `feature/web-ui`

---

## 1. Goals & non-goals

### Goals

- Lower the barrier to using `twincut` so that non-CLI users (or future-self in six months who has forgotten the flags) can deduplicate media without reading the help text.
- Cover three workflows in v1: **self-check**, **cross-check**, and **restore from history**.
- Run entirely on the user's own Mac. No network exposure, no cloud, no auth.
- Distribute as a single drop-in binary that complements the existing `twincut.sh`, not as a replacement.
- Support both English and Simplified Chinese, with a one-click language switcher.

### Non-goals

- LAN-hosted / multi-user service. Browsers cannot reach into a remote computer's filesystem; a centralized hosting model would force every device's media onto a shared host, which doesn't match the user's setup. Local-only sidesteps this.
- Thumbnail-detection workflow (deferred to v2 — has the largest option surface and the most edge cases).
- Cross-OS support beyond macOS in v1. Linux probably works, untested.
- Authentication, accounts, or session management.
- Re-implementing twincut's matching logic in Go. The bash script remains the source of truth.

---

## 2. Architecture overview

```
┌─────────────────────────────────────────────────────────┐
│  twincut-ui  (single Go binary, ~6–10 MB)               │
│                                                         │
│  ├── HTTP server (net/http, 127.0.0.1:7681)             │
│  │     ├── Static assets    ← embed.FS (HTML/CSS/HTMX)  │
│  │     ├── HTMX endpoints   → render HTML fragments     │
│  │     ├── /sse/{run_id}    → progress event stream     │
│  │     ├── /thumb?path=…    → on-the-fly thumbnails     │
│  │     └── /fs?path=…       → directory browser API     │
│  │                                                      │
│  ├── Run manager (in-memory map: run_id → process)      │
│  │     └─ exec twincut.sh --json-events …               │
│  │                                                      │
│  ├── State (~/.twincut-ui/)                             │
│  │     ├── recents.json     ← recent folders            │
│  │     ├── runs/<id>.ndjson ← captured event stream     │
│  │     ├── settings.json    ← prefs (port, theme, lang) │
│  │     └── cache/           ← thumbnail cache           │
│  │                                                      │
│  └── Browser auto-open (`open http://localhost:7681`)   │
└─────────────────────────────────────────────────────────┘
                          │ exec/pipe
                          ▼
┌─────────────────────────────────────────────────────────┐
│  twincut.sh  (existing bash, +new --json-events flag)   │
│  └── streams NDJSON to stdout when flag is set          │
└─────────────────────────────────────────────────────────┘
```

### Stack choices

| Layer       | Choice                          | Why                                                                |
|-------------|---------------------------------|--------------------------------------------------------------------|
| Backend     | Go (net/http, embed.FS)         | Single static binary, zero runtime deps, drop-in install           |
| Frontend    | HTMX + server-rendered HTML     | No build step, no JS framework, ~14 KB bundle, easy solo maintain  |
| Streaming   | Server-Sent Events (SSE)        | HTTP, no proxy quirks, perfect HTMX integration, one-way is enough |
| Storage     | Plain files under `~/.twincut-ui/` | No DB, survives crashes, easy to inspect/debug                  |
| i18n        | Embedded JSON catalogs, server-rendered | Each fragment arrives pre-translated, no client-side i18n lib  |
| Match logic | `twincut.sh` (unchanged)        | Bash remains source of truth; Go shells out                        |

### Process model

- One `twincut-ui` process while the browser is open. Ctrl+C cleanly shuts down (kills any in-flight bash subprocesses via process group).
- Multiple concurrent scans possible (each tab → its own `run_id`). v1 limits concurrent runs only by refusing overlapping folder paths.
- Bash subprocesses inherit a `TWINCUT_RUN_ID` env var so the server can correlate events.

---

## 3. UX walkthrough

### Layout

```
┌──────────────────────────────────────────────────────────────┐
│  twincut                  [中文 ▾]  [⚙ settings] [● ready]   │
├──────────────┬───────────────────────────────────────────────┤
│              │                                               │
│  Self-check  │   <main panel — current tab's content>        │
│  Cross-check │                                               │
│  ──────────  │                                               │
│  History     │                                               │
│              │                                               │
│  ──────────  │                                               │
│  ● running   │                                               │
│    self-chk  │                                               │
│    Pictures/ │                                               │
│    47%       │                                               │
└──────────────┴───────────────────────────────────────────────┘
```

- **Left sidebar (~220px, fixed):** workflow tabs (Self-check, Cross-check, History) and an "active runs" indicator at the bottom showing any in-progress scans with mini progress bars.
- **Top header:** language switcher (`EN` / `中文`), settings gear, server status dot.
- **Main panel:** the current tab's content. Each workflow tab follows the same three-state pattern below.
- **Mobile / narrow viewport:** sidebar collapses to a hamburger menu (HTMX + flexbox). iPhone is for monitoring runs, not driving them.

### Three-state pattern (per workflow tab)

```
[ FORM ]      → user fills folders + (collapsed) advanced options
   │  click Preview
   ▼
[ RUNNING ]   → progress bar, "Hashing 1247 / 12580 — ETA 2m 14s"
   │            collapsible "Show log ▾" reveals raw bash stdout
   ▼          preview finishes
[ RESULTS ]   → grouped duplicate clusters with thumbnails
   │            summary stats at top (count, GB reclaimable)
   │            per-file checkboxes to exclude individual files
   │            [ Apply (3.4 GB · 142 files) ]   [ Cancel ]
   ▼          click Apply
[ DONE ]      → "Quarantined 142 files. [View in History]"
```

The four sub-panels are distinct HTMX-rendered fragments swapped into the same content area. **Always preview first** — there is no path to mutating files without seeing the dry-run results first.

### Folder picker (hybrid — used by Self-check + Cross-check forms)

Combined input that supports three interaction styles:

- **Path input** (top) — paste or type a path; autocomplete suggests subdirectories as you type.
- **Recent folders** — small list of last 5 folders the user scanned.
- **Tree browser** — Finder-style click-through, lazy-loaded per directory.

All three target the same hidden value submitted with the form.

### Per-workflow form contents

**Self-check form:**
- Folder picker (one folder).
- Advanced (collapsed): `--algo`, `--min-size`, `--ext`, `--include-similar-video` checkbox, custom quarantine path.

**Cross-check form:**
- Source folder picker.
- Backup folders list (one or more, with "+ Add backup" button).
- Advanced (collapsed): `--algo`, `--min-size`, `--ext`, `--video-fast-strict`, `--exact`, custom quarantine path.

**History tab:**
- Reverse-chronological list of past runs from `~/.twincut-ui/runs/*.ndjson` plus any manifest TSVs found in user's quarantine roots.
- Each entry: timestamp, mode, folder(s), files-affected count, status (success / interrupted / failed).
- Clicking a run shows the manifest as a list of moved files plus a **Restore** button that re-runs `twincut.sh --restore <manifest.tsv>`.
- This is the v1 home of restore — no separate Restore tab.

### Settings panel

Modal opened from the gear icon. Stored in `~/.twincut-ui/settings.json`.

- Port number (default 7681)
- Theme (auto / light / dark — auto follows OS via `prefers-color-scheme`)
- Language (en / zh-Hans)
- Default `--algo` / `--min-size` / `--ext`
- Default quarantine root
- "Open browser on launch" toggle

---

## 4. Internationalization

- **v1 languages:** English (`en`) and Simplified Chinese (`zh-Hans`). Architecture allows adding more by dropping a JSON file in `ui/locales/`.
- **Catalog format:** flat key-value JSON, e.g. `{"button.preview": "Preview"}` and `{"button.preview": "预览"}`. Both files share the same key set; missing keys fall back to English with a server-side warning.
- **Locale resolution:**
  1. `lang` cookie (set when user picks via header switcher)
  2. `~/.twincut-ui/settings.json` `language` field
  3. `Accept-Language` request header (`zh*` → `zh-Hans`, else `en`)
  4. Default `en`
- **Server-side rendering:** Every HTMX endpoint reads the cookie and passes a `t func(key string) string` into the template. No client-side i18n library.
- **Translated:** all UI chrome, run messages, summary text, error toasts, match-reason badges (md5 / video-fast / etc.).
- **Not translated:** raw `twincut.sh` log stream under "Show log ▾" — debug detail; translation cost not justified.
- **Switcher UX:** top-right header dropdown. Click → HTMX swaps page chrome without full reload.

---

## 5. `twincut.sh` changes — the `--json-events` flag

The only modification to the bash script. Opt-in: nothing about existing CLI behavior changes.

### Event types (NDJSON, one per line on stdout)

Every event has `{"type": ..., "ts": <unix>, "run_id": "<uuid>"}` plus type-specific fields.

```jsonc
// emitted at start
{"type": "run_start", "mode": "self_check" | "cross_check" | "restore",
 "args": {/* resolved CLI args */}}

// during enumeration / hashing — throttled by --prog-step
{"type": "progress", "phase": "enumerate" | "hash" | "video_meta" | "match",
 "done": 1247, "total": 12580, "current_path": "/Pictures/2025/IMG_4421.jpg"}

// per duplicate group identified during preview
{"type": "dup_group", "group_id": 1,
 "match_reason": "md5" | "video_fast" | "video_strict",
 "keep": {"path": "...", "size": 4404928, "mtime": 1736889600},
 "remove": [{"path": "...", "size": 4404928, "mtime": 1736889700}, ...]}

// non-fatal warnings
{"type": "warn", "code": "bad_video" | "io_error" | "unreadable",
 "path": "...", "detail": "..."}

// emitted as quarantine/delete actions complete during apply
{"type": "action", "kind": "move" | "delete" | "skip",
 "src": "...", "dst": "...", "reason": "..."}

// fatal error — script will exit non-zero shortly after
{"type": "error", "code": "missing_dep" | "io_error" | "usage_error",
 "detail": "..."}

// emitted at the end
{"type": "run_end", "groups": 47, "files_affected": 142,
 "bytes_reclaimed": 3521708288, "duration_sec": 87.4,
 "manifest_path": "/.../_manifest-<id>.tsv", "cancelled": false}
```

### Implementation notes

- A single `emit_json()` helper handles JSON escaping (backslash, quote, newline, control chars). `jq` is not introduced as a runtime dep.
- When `--json-events` is on, the human-readable progress lines are suppressed on stdout (otherwise the server has to filter them). Real errors still go to stderr; Go captures both streams separately.
- Throttling: `progress` events respect the existing `--prog-step` (default 200). Sensible cadence for browser SSE.
- Orthogonal to `--dry-run`: dry-run emits `dup_group` only; full run emits `dup_group` + `action`.
- `run_id` is generated by `twincut.sh` if not supplied; server passes one in via `TWINCUT_RUN_ID` env var.
- Estimated change: ~80–120 lines added. Single helper + emit calls at existing log/progress points + one new entry in the arg parser. Existing flags untouched.
- New helper flag: `--exclude-path <path>` (repeatable) — used by Apply to honor the user's per-file unchecks. Path-based deny-list; missing paths are a no-op.

---

## 6. Request lifecycle

### Preview (dry-run)

```
Browser              Go server                  twincut.sh

POST /api/runs ────►│                          │
{mode, folder,      │ generate run_id (uuid)   │
 options}           │ register run             │
                    │ spawn process:           │
                    │  twincut.sh --self-check │
                    │   --dry-run              │
                    │   --json-events ────────►│
                    │                          │ start
◄── HTML fragment ──│                          │
   (running panel,  │                          │
    SSE subscribe)  │ ◄── NDJSON line ─────────│ run_start
                    │ append to runs/X.ndjson  │
                    │ broadcast on SSE topic X │
◄═ SSE: progress ══│ ◄── progress events ─────│
◄═ SSE: dup_group ═│ ◄── dup_group events ────│
   (groups append   │      ...                 │
    into hidden     │                          │
    buffer)         │                          │
◄═ SSE: run_end ═══│ ◄── run_end ─────────────│ exit 0
   HTMX swaps to    │ mark run "preview-done"  │
   RESULTS panel,   │                          │
   render groups    │                          │
   with thumbnails  │                          │
GET /thumb?path= ──►│ ffmpeg/image decode      │
◄── 128×128 jpeg ───│   (cached)               │
```

### Apply

```
POST /api/runs/X/apply ──►│ user clicked Apply
{exclude: [path,...]}     │ spawn second process:
                          │   twincut.sh --self-check
                          │     --json-events --assume-yes
                          │     --exclude-path … (per uncheck)
                          │     (no --dry-run) ────────────────►│
◄═ SSE: progress ════════│ ◄── progress ──────────────────────│
◄═ SSE: action move ════│ ◄── action ────────────────────────│ moved
   ...                   │                                    │
◄═ SSE: run_end ════════│ ◄── run_end ───────────────────────│ exit 0
   HTMX swaps to DONE   │ store final manifest path           │
                        │ in runs/X.ndjson                    │
```

### Key choices

- **SSE, not WebSocket.** Server→client only; HTMX has first-class support.
- **Server-authoritative state.** Browser is a thin renderer. Closing the tab does not kill a scan; reopen → reattach via `Last-Event-ID` replay from `runs/X.ndjson`.
- **Two process spawns** (preview + apply) rather than holding state in Go between them. Twincut's hash cache makes the second run nearly free, exclusion lists may differ, and it preserves the script's existing semantics.
- **Apply uses `--assume-yes`.** User confirmed in the UI; the script must not prompt.
- **Thumbnails on demand.** `/thumb?path=…` shells out to `ffmpeg` for video first frames, uses `golang.org/x/image` for stills. Cached in `~/.twincut-ui/cache/<sha1-of-path>.jpg`. Mtime-based invalidation.

---

## 7. Error handling, edge cases, concurrency

### Script-emitted errors
- `missing_dep` → error panel with install instructions for the missing tool.
- `bad_video`, `io_error`, `unreadable` → warnings accordion at top of results; do not block Apply.
- I/O failure during apply → per-file `warn`; the run continues. DONE panel reports "M of N succeeded."

### File races (preview → apply gap)
- Apply re-runs `twincut.sh`, which re-validates each pair before action. If a file changed or was deleted, the script naturally skips and emits `action.kind = "skip"` with a reason.
- `--exclude-path` is safe against missing paths (no-op).

### Process / Go server crashes
- Each run is journaled to `~/.twincut-ui/runs/<id>.ndjson` in real time. History reads these — past runs survive crashes.
- If the server dies mid-scan, the bash subprocess is killed (process group). Run reappears in History as `interrupted` with whatever events were captured.
- Browser disconnect mid-stream → HTMX SSE auto-reconnects; server replays from `Last-Event-ID`.

### Concurrent runs
- Multiple scans allowed. Each tab maintains its own `run_id`.
- **Path-based lock:** server refuses to start a new run on a folder that overlaps with an active run's folder (in-memory map keyed by absolute, canonicalized path). Preview-on-same-folder is also blocked because the bash hash cache is the same file.
- v1 ships without an explicit concurrent-run limit; if disk thrashing becomes a problem, add a "Run after current finishes" toggle in v2.

### Folder safety
- Directory-browser endpoint `/fs` uses an allowlist: starts at `$HOME` and any explicitly mounted volume under `/Volumes/*`. Refuses `/`, `/System`, `/private`, `/etc`, `/usr`, etc.
- Scanning inside `~/.twincut-ui/` is rejected with a clear error.

### Stop button
- Each running scan has a Stop button → SIGTERM to bash process group → `run_end` with `cancelled: true`. Files already moved are NOT rolled back; user can use Restore for that.

---

## 8. Testing

### `twincut.sh --json-events` (the new bash code)
- New `tests/json_events/` with fixture folders (known dupes, thumbnail pair, bad video, AppleDouble files).
- Each test runs `twincut.sh ... --json-events` and pipes stdout through a Python validator that:
  - Parses each line as JSON (catches escape bugs).
  - Validates the event sequence (`run_start` first, `run_end` last, no events after run_end).
  - Validates each event against `tests/json_events/schema.json`.
  - Asserts known fixture results (e.g. exactly N `dup_group` events).
- `make test-script` target. Runs in seconds. **First automated tests in the repo.**

### Go server (unit)
- Standard `go test`. Coverage targets:
  - JSON event parser (canned NDJSON fixtures → assert state).
  - Locale resolver (Accept-Language → catalog selection, cookie/settings precedence).
  - Directory-browser allowlist (`/`, `/System` rejected; `~`, `/Volumes/X` allowed).
  - Path-based run-lock (overlapping paths → second run rejected).
  - Thumbnail cache key derivation + mtime invalidation.

### Go server (integration)
- `testscript`-style suite. Spins up the real Go server on a random port pointed at a fixture folder.
- Exercises full preview → SSE → apply → restore round-trip. Uses the real `twincut.sh`.

### Browser / UI
- No automated browser tests in v1.
- `tests/ui-checklist.md` lists manual flows. ~5 minutes per release.
- If UI grows, add Playwright in v2.

### Out of scope for v1
- Load testing (single-user local).
- Cross-OS testing (macOS-first; Linux untested).
- Visual regression tests.

---

## 9. Install & distribution

### Repo layout (post-feature)

```
twincut/
├── bin/
│   ├── twincut.sh              ← existing, +--json-events, +--exclude-path
│   └── vid_eq.sh               ← unchanged
├── ui/                         ← NEW: Go module
│   ├── go.mod
│   ├── main.go                 ← entry: parse flags, start server, open browser
│   ├── server/
│   │   ├── http.go             ← routes
│   │   ├── runs.go             ← run manager + process spawn
│   │   ├── events.go           ← NDJSON parser
│   │   ├── thumbs.go           ← thumbnail cache + ffmpeg shell-out
│   │   ├── fs.go               ← directory browser (allowlist)
│   │   └── i18n.go             ← locale resolution
│   ├── templates/              ← html/template files (HTMX fragments)
│   ├── static/                 ← htmx.min.js, css
│   ├── locales/
│   │   ├── en.json
│   │   └── zh-Hans.json
│   └── tests/
├── installers/
│   ├── install.sh              ← updated: also symlinks twincut-ui
│   └── uninstall.sh            ← updated: removes twincut-ui symlink
├── tests/
│   └── json_events/            ← NEW: bash-script tests
└── Makefile                    ← NEW: build + test targets
```

### Build

- `make build` → `go build -o bin/twincut-ui ./ui` → ~6–10 MB static binary.
- Cross-compile targets: `darwin/arm64` + `darwin/amd64`. Linux trivial to add.

### Install

- `installers/install.sh` symlinks `twincut.sh` and `twincut-ui` into `~/.local/bin/`.
- First launch creates `~/.twincut-ui/` with default `settings.json`.

### `twincut-ui` CLI surface

```
twincut-ui [--port 7681] [--no-open] [--state-dir <path>] [--lang en|zh-Hans]
```

- Default behavior: pick port 7681, fall back to a free port if taken, start server, run `open http://localhost:<port>`, block until Ctrl+C. Clean shutdown kills bash subprocesses.
- `--no-open` for re-launch case (tab already open).
- Logs to stderr, timestamped.

### Distribution channels

- v1: source build only (clone → `make install`).
- v2: prebuilt binaries on GitHub Releases + Homebrew tap.

### Uninstall

- `installers/uninstall.sh` removes both symlinks. `--purge` flag also wipes `~/.twincut-ui/` (off by default — preserves history).

---

## 10. Implementation stages

Suggested incremental ordering. Each stage is independently shippable / verifiable.

1. **`twincut.sh --json-events` flag + tests.** Foundation for everything else. Includes `--exclude-path`. New `tests/json_events/` suite + Makefile. Branch-only; no Go yet.
2. **Go module skeleton + binary.** `ui/main.go`, embed.FS, `go build` produces `bin/twincut-ui` that serves a "hello" page on 127.0.0.1:7681 and opens the browser. No real functionality, but proves the install/launch story.
3. **Run manager + SSE plumbing.** Spawn `twincut.sh` as a subprocess, parse NDJSON, broadcast over SSE. Test via a debug page that streams raw events.
4. **Self-check workflow (form → preview → results → apply → done).** End-to-end, English-only, minimal styling. First user-visible feature.
5. **Thumbnail endpoint + grouped-cluster results UI.** Replaces the bare list from stage 4 with the real designed output.
6. **History tab + Restore.** Reads `~/.twincut-ui/runs/*.ndjson` and quarantine manifests; renders entries; wires Restore button.
7. **Cross-check workflow.** Mostly form + multi-backup picker; reuses the run/results pipeline.
8. **i18n.** Add locale catalogs, switcher, server-side translation pass over all templates.
9. **Settings panel + persistence.** Theme, port, defaults.
10. **Install & polish.** Update `installers/`, write README section, ship.

---

## 11. Out of scope (v2+)

- Thumbnail-detection workflow (largest option surface; needs its own design pass).
- LAN / multi-user mode.
- Linux + Windows builds.
- Prebuilt binary distribution (GitHub Releases, Homebrew tap).
- Browser automation tests (Playwright).
- Background launchd service / always-on mode.
- Per-user accounts.

---

## 12. Open questions / risks

- **Bash JSON escaping.** Hand-rolled escaper has a non-trivial test surface. Mitigation: aggressive fixture coverage in `tests/json_events/`.
- **Thumbnail generation speed.** `ffmpeg` per-video first-frame extraction is ~100–500 ms each; for a results screen with 200 thumbnails this could feel slow on initial render. Mitigation: lazy-load (`loading="lazy"`), cache aggressively, render placeholders first.
- **HTMX SSE reconnect semantics.** Need to verify the extension correctly resumes from `Last-Event-ID` against our NDJSON journal. If not, fall back to "missed events shown as a banner: 'Some progress events were missed during reconnect, full state restored.'"
- **Concurrent-run thrash.** Two scans on different folders may saturate disk I/O. v1 has no limiter; revisit if it bites.
