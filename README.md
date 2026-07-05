# twincut

A media de-duplication tool. Compares one folder against one or more backup
folders (cross-check) and/or finds duplicates within a single folder
(self-check), and quarantines the matches with an undoable manifest.

Hash-exact matches use md5/sha1; similar-video matches additionally consider
size, duration, fps, bitrate, and a frame-equality check via `ffprobe`.

Two front-ends:

- **`twincut`** — single-file bash CLI. The source of truth for matching
  logic; usable by itself.
- **`twincut-ui`** — local web UI (Go binary, `~8 MB`) that drives the bash
  CLI and renders the results in the browser. Optional.

## Install

```sh
installers/install.sh   # symlinks bin/twincut.sh → ~/.local/bin/twincut
                        # symlinks bin/vid_eq.sh   → ~/.local/bin/vid_eq
                        # symlinks bin/twincut-ui  → ~/.local/bin/twincut-ui (if built)
```

To build the Web UI binary first:

```sh
make build              # → bin/twincut-ui
```

Runtime deps: `bash`, `ffprobe` / `ffmpeg`, `jq`, and standard coreutils. The
Web UI additionally needs nothing at runtime — it embeds its assets and shells
out to `twincut.sh` for all matching. Optional thumbnail pHash pairing uses
Python with Pillow and imagehash.

## Use it

### CLI

```sh
# Self-check (recommended): find intra-folder duplicates, dry-run first.
twincut --self-check ~/photos --dry-run
twincut --self-check ~/photos                 # apply
twincut --restore ~/photos/_QUARANTINE/_manifest-<run>.tsv

# Cross-check: remove files in SOURCE that already exist in BACKUP.
twincut --source ~/inbox --backup /Volumes/Backup --dry-run
```

`twincut --help` for the full surface, including similar-video tuning,
extension filters, hash algo, and more.

### Web UI

```sh
twincut-ui              # opens http://localhost:7681 in your browser
```

Local-only (binds 127.0.0.1). Each scan runs `twincut.sh` as a subprocess
and streams progress over Server-Sent Events. The Apply step uses an
explicit move list so you can override which file in each cluster is
the keeper, and reveal the quarantine folder in Finder afterward.

State (run journals, recent folders, thumbnail cache) lives in
`~/.twincut-ui/`.

## Develop

```sh
make test                # runs the bash json_events suite + go test ./...
make build               # builds bin/twincut-ui
make install             # builds + installs symlinks
```

CI also runs the bash smoke suites for event contracts, P0 file-moving
behavior, Stage 11 event shapes, and macOS thumbnail detection.

The Web UI design lives in
[`docs/superpowers/specs/2026-05-15-twincut-web-ui-design.md`](docs/superpowers/specs/2026-05-15-twincut-web-ui-design.md).

Project notes for contributors and AI assistants are in `CLAUDE.md`.
