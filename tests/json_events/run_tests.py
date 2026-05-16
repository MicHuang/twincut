#!/usr/bin/env python3
"""
Validator + test runner for `twincut.sh --json-events`.

Each test case is a Python function that:
  1. Sets up a fixture folder under a tmp dir.
  2. Invokes twincut.sh with a chosen flag set.
  3. Captures stdout (NDJSON) and asserts on the parsed event stream.
"""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import sys
import tempfile
import traceback
from dataclasses import dataclass
from pathlib import Path
from typing import Callable, Iterable

REPO_ROOT = Path(__file__).resolve().parents[2]
TWINCUT = REPO_ROOT / "bin" / "twincut.sh"

# Event types we know about. Anything else is an error.
KNOWN_TYPES = {
    "run_start",
    "run_end",
    "progress",
    "dup_group",
    "action",
    "warn",
    "error",
}

# Required fields per event type. Stage 1 contract.
REQUIRED_FIELDS = {
    "run_start": {"mode"},
    "run_end": {"cancelled"},
    "progress": {"phase", "done"},
    "dup_group": {"group_id", "match_reason", "keep_path", "remove"},
    "action": {"kind", "src"},
    "warn": {"code"},
    "error": {"code", "detail"},
}


@dataclass
class TestResult:
    name: str
    ok: bool
    detail: str = ""


def write_file(path: Path, content: bytes | str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    if isinstance(content, str):
        path.write_text(content)
    else:
        path.write_bytes(content)


def run_twincut(args: list[str]) -> tuple[list[dict], str, int]:
    """Run twincut.sh with --json-events and return (events, stderr, exit_code)."""
    proc = subprocess.run(
        ["bash", str(TWINCUT), *args, "--json-events"],
        capture_output=True,
        text=True,
        timeout=60,
    )
    events: list[dict] = []
    for i, line in enumerate(proc.stdout.splitlines(), 1):
        line = line.strip()
        if not line:
            continue
        try:
            events.append(json.loads(line))
        except json.JSONDecodeError as e:
            raise AssertionError(
                f"stdout line {i} is not valid JSON: {e!r}\n  line: {line!r}"
            )
    return events, proc.stderr, proc.returncode


def validate_structure(events: Iterable[dict]) -> None:
    """Sequence + per-event field checks shared by every test."""
    events = list(events)
    if not events:
        raise AssertionError("no events emitted")
    if events[0]["type"] != "run_start":
        raise AssertionError(f"first event is {events[0]['type']!r}, expected 'run_start'")
    if events[-1]["type"] != "run_end":
        raise AssertionError(f"last event is {events[-1]['type']!r}, expected 'run_end'")

    run_id = events[0].get("run_id", "")
    if not run_id:
        raise AssertionError("run_start event has no run_id")

    for i, ev in enumerate(events, 1):
        t = ev.get("type")
        if t not in KNOWN_TYPES:
            raise AssertionError(f"event {i}: unknown type {t!r}")
        if "ts" not in ev:
            raise AssertionError(f"event {i} ({t}): missing 'ts'")
        if ev.get("run_id") != run_id:
            raise AssertionError(
                f"event {i} ({t}): run_id mismatch {ev.get('run_id')!r} != {run_id!r}"
            )
        for field in REQUIRED_FIELDS.get(t, set()):
            if field not in ev:
                raise AssertionError(f"event {i} ({t}): missing required field {field!r}")


# --------------------------------- Tests ------------------------------------

def test_self_check_dry_run_emits_dup_group(tmp: Path) -> None:
    write_file(tmp / "a.jpg", b"duplicate-content-here")
    write_file(tmp / "b.jpg", b"duplicate-content-here")
    write_file(tmp / "c.jpg", b"duplicate-content-here")
    write_file(tmp / "unique.jpg", b"different-content-here")

    events, _, ec = run_twincut(["--self-check", str(tmp), "--dry-run"])
    assert ec == 0, f"expected exit 0, got {ec}"
    validate_structure(events)

    dup_groups = [e for e in events if e["type"] == "dup_group"]
    assert len(dup_groups) == 1, f"expected 1 dup_group, got {len(dup_groups)}"
    g = dup_groups[0]
    assert g["match_reason"] == "md5"
    assert isinstance(g["remove"], list) and len(g["remove"]) == 2
    paths_in_group = {g["keep_path"], *(r["path"] for r in g["remove"])}
    assert paths_in_group == {
        str(tmp / "a.jpg"),
        str(tmp / "b.jpg"),
        str(tmp / "c.jpg"),
    }, f"group paths mismatch: {paths_in_group}"

    # Dry-run must NOT emit any non-skip action events.
    actions = [e for e in events if e["type"] == "action" and e.get("kind") != "skip"]
    assert not actions, f"dry-run should emit no move/delete actions, got {actions!r}"


def test_self_check_apply_emits_actions_and_moves_files(tmp: Path) -> None:
    write_file(tmp / "a.jpg", b"another-duplicate")
    write_file(tmp / "b.jpg", b"another-duplicate")

    events, _, ec = run_twincut(["--self-check", str(tmp), "--assume-yes"])
    assert ec == 0, f"expected exit 0, got {ec}"
    validate_structure(events)

    moves = [e for e in events if e["type"] == "action" and e["kind"] == "move"]
    assert len(moves) == 1, f"expected 1 move action, got {len(moves)}"

    end = events[-1]
    assert end["moved"] == 1
    assert end["dupes"] == 1
    assert end["manifest_path"], "run_end should report manifest_path on apply"

    # File system effect: exactly one of a.jpg/b.jpg remains; the other moved.
    remaining = sorted(p.name for p in tmp.iterdir() if p.is_file())
    assert remaining in (["a.jpg"], ["b.jpg"]), f"unexpected remaining files: {remaining}"


def test_exclude_path_emits_skip_and_keeps_file(tmp: Path) -> None:
    write_file(tmp / "a.jpg", b"xx-dupe")
    write_file(tmp / "b.jpg", b"xx-dupe")
    write_file(tmp / "c.jpg", b"xx-dupe")

    events, _, ec = run_twincut(
        ["--self-check", str(tmp), "--assume-yes", "--exclude-path", str(tmp / "c.jpg")]
    )
    assert ec == 0, f"expected exit 0, got {ec}"
    validate_structure(events)

    skips = [
        e for e in events
        if e["type"] == "action" and e["kind"] == "skip" and e.get("reason") == "excluded"
    ]
    assert len(skips) == 1, f"expected 1 excluded-skip, got {len(skips)}"
    assert skips[0]["src"] == str(tmp / "c.jpg")

    # c.jpg must still exist on disk.
    assert (tmp / "c.jpg").exists(), "excluded file was moved/deleted"


def test_run_id_env_override_is_respected(tmp: Path) -> None:
    write_file(tmp / "a.jpg", b"x")
    write_file(tmp / "b.jpg", b"x")

    proc = subprocess.run(
        ["bash", str(TWINCUT), "--self-check", str(tmp), "--dry-run", "--json-events"],
        capture_output=True,
        text=True,
        timeout=30,
        env={**os.environ, "TWINCUT_RUN_ID": "fixture-run-id-xyz"},
    )
    assert proc.returncode == 0
    events = [json.loads(l) for l in proc.stdout.splitlines() if l.strip()]
    validate_structure(events)
    for ev in events:
        assert ev["run_id"] == "fixture-run-id-xyz", (
            f"run_id mismatch: {ev['run_id']!r}"
        )


def test_no_dupes_yields_no_dup_group(tmp: Path) -> None:
    write_file(tmp / "a.jpg", b"unique-1")
    write_file(tmp / "b.jpg", b"unique-2")
    write_file(tmp / "c.jpg", b"unique-3")

    events, _, ec = run_twincut(["--self-check", str(tmp), "--dry-run"])
    assert ec == 0
    validate_structure(events)
    assert not [e for e in events if e["type"] == "dup_group"], "expected no dup_groups"


def test_apply_list_executes_listed_moves(tmp: Path) -> None:
    # Three identical files; apply-list says "quarantine b, keep a" and
    # ignores c entirely. We expect exactly one move action and c untouched.
    write_file(tmp / "a.jpg", b"apply-list-content")
    write_file(tmp / "b.jpg", b"apply-list-content")
    write_file(tmp / "c.jpg", b"apply-list-content")

    apply_list = tmp / "apply.tsv"
    apply_list.write_text(
        f"{tmp / 'b.jpg'}\t{tmp / 'a.jpg'}\t1\tmd5\tdeadbeef\n"
    )

    events, _, ec = run_twincut(
        ["--self-check", str(tmp), "--apply-list", str(apply_list), "--assume-yes"]
    )
    assert ec == 0, f"expected exit 0, got {ec}"
    validate_structure(events)

    moves = [e for e in events if e["type"] == "action" and e["kind"] == "move"]
    assert len(moves) == 1, f"expected 1 move, got {len(moves)}"
    assert moves[0]["src"] == str(tmp / "b.jpg")
    assert moves[0]["matched"] == str(tmp / "a.jpg")
    assert moves[0]["decision"] == "apply_list_md5"

    # No dup_group should be emitted (we skipped scan).
    assert not [e for e in events if e["type"] == "dup_group"], \
        "apply-list mode should skip scan/match"

    # File system: a.jpg + c.jpg remain in source; b.jpg is in quarantine.
    remaining = sorted(p.name for p in tmp.iterdir() if p.is_file())
    assert remaining == ["a.jpg", "apply.tsv", "c.jpg"], (
        f"unexpected remaining files: {remaining}"
    )
    quar_files = list((tmp / "_QUARANTINE" / "_self_dupes").iterdir())
    assert len(quar_files) == 1 and quar_files[0].name == "b.jpg"


def test_apply_list_dry_run_keeps_files(tmp: Path) -> None:
    write_file(tmp / "a.mp4", b"video-bytes-a")
    write_file(tmp / "b.mp4", b"video-bytes-b")
    apply_list = tmp / "apply.tsv"
    apply_list.write_text(f"{tmp / 'b.mp4'}\t{tmp / 'a.mp4'}\t1\tvideo_fast\t\n")

    events, _, ec = run_twincut(
        ["--self-check", str(tmp), "--apply-list", str(apply_list),
         "--dry-run", "--assume-yes"]
    )
    assert ec == 0
    validate_structure(events)

    moves = [e for e in events if e["type"] == "action" and e["kind"] == "move"]
    assert len(moves) == 1
    assert moves[0]["dry_run"] is True
    # Decision string carries the reason → similar-video subdir intent.
    assert moves[0]["decision"] == "apply_list_video_fast"
    assert "_similar_video_source" in moves[0]["dst"]

    # Files should still be in place.
    assert (tmp / "a.mp4").exists() and (tmp / "b.mp4").exists()


def test_apply_list_warns_on_missing_source(tmp: Path) -> None:
    # File listed in apply-list doesn't exist; expect a warn and no action.
    write_file(tmp / "a.jpg", b"x")
    apply_list = tmp / "apply.tsv"
    apply_list.write_text(
        f"{tmp / 'ghost.jpg'}\t{tmp / 'a.jpg'}\t1\tmd5\t\n"
    )

    events, _, ec = run_twincut(
        ["--self-check", str(tmp), "--apply-list", str(apply_list), "--assume-yes"]
    )
    assert ec == 0
    validate_structure(events)
    warns = [e for e in events if e["type"] == "warn" and e.get("code") == "missing_file"]
    assert len(warns) == 1
    assert not [e for e in events if e["type"] == "action" and e["kind"] == "move"]


def test_special_chars_in_paths_are_json_escaped(tmp: Path) -> None:
    name1 = 'tricky "quotes" \\ and tabs.jpg'
    name2 = "another 'one'.jpg"
    write_file(tmp / name1, b"escape-test-content")
    write_file(tmp / name2, b"escape-test-content")

    # If escaping is broken, json.loads will throw inside run_twincut.
    events, _, ec = run_twincut(["--self-check", str(tmp), "--dry-run"])
    assert ec == 0
    validate_structure(events)

    dup = [e for e in events if e["type"] == "dup_group"]
    assert len(dup) == 1
    paths = {dup[0]["keep_path"], *(r["path"] for r in dup[0]["remove"])}
    assert paths == {str(tmp / name1), str(tmp / name2)}


def test_restore_dry_run_emits_action_events(tmp: Path) -> None:
    # Seed a manifest TSV that mimics one written by a self-check apply,
    # then run --restore --restore-dry-run --json-events and assert the
    # event stream covers run_start/progress/action/run_end with the
    # restore-specific kinds and counts.
    manifest = tmp / "_manifest.tsv"
    quar_dir = tmp / "_QUARANTINE" / "_self_dupes"
    quar_dir.mkdir(parents=True)
    # File still present in quarantine, original missing → restorable
    write_file(quar_dir / "ok.jpg", b"data-ok")
    # File that we'll claim is in quarantine but actually isn't → missing
    # File whose original already exists at restore-target → conflict
    write_file(tmp / "conflict_target.jpg", b"already-here")
    write_file(quar_dir / "conflict.jpg", b"data-conflict")

    manifest.write_text(
        "# header noise twincut tolerates\n"
        "run_id\tts\torig\tquar\tmatched\talgo\thash\tdecision\tsize\tmtime\n"
        f"R1\t1\t{tmp / 'ok.jpg'}\t{quar_dir / 'ok.jpg'}\t-\tmd5\tDEAD\tself:moved\t0\t0\n"
        f"R1\t1\t{tmp / 'gone.jpg'}\t{quar_dir / 'gone.jpg'}\t-\tmd5\tBEEF\tself:moved\t0\t0\n"
        f"R1\t1\t{tmp / 'conflict_target.jpg'}\t{quar_dir / 'conflict.jpg'}\t-\tmd5\tCAFE\tself:moved\t0\t0\n"
        f"R1\t1\t{tmp / 'deleted_orig.jpg'}\t\t-\tmd5\tFADE\tself:deleted\t0\t0\n"
    )

    events, _, ec = run_twincut(["--restore", str(manifest), "--restore-dry-run"])
    assert ec == 0, f"expected exit 0, got {ec}"
    validate_structure(events)

    starts = [e for e in events if e["type"] == "run_start"]
    assert len(starts) == 1 and starts[0]["mode"] == "restore", starts
    assert starts[0]["source"] == str(manifest)

    ends = [e for e in events if e["type"] == "run_end"]
    assert len(ends) == 1, ends
    end = ends[0]
    assert end["restored"] == 1, f"restored count: {end}"
    assert end["missing"] == 1, f"missing count: {end}"
    assert end["skipped"] == 1, f"skipped count: {end}"
    assert end["unrecoverable"] == 1, f"unrecoverable count: {end}"
    assert end["cancelled"] is False

    actions = [e for e in events if e["type"] == "action"]
    kinds = sorted(a["kind"] for a in actions)
    assert kinds == ["restore", "restore_conflict", "restore_missing", "restore_unrecoverable"], kinds


def test_restore_executes_and_emits_run_end(tmp: Path) -> None:
    # Real (non-dry-run) restore of one file: emits a restore action with
    # dry_run=false and the file actually moves back.
    manifest = tmp / "_manifest.tsv"
    quar_dir = tmp / "_QUARANTINE"
    quar_dir.mkdir()
    write_file(quar_dir / "back.jpg", b"data-back")
    manifest.write_text(
        "run_id\tts\torig\tquar\tmatched\talgo\thash\tdecision\tsize\tmtime\n"
        f"R\t0\t{tmp / 'back.jpg'}\t{quar_dir / 'back.jpg'}\t-\tmd5\tHH\tself:moved\t0\t0\n"
    )

    events, _, ec = run_twincut(["--restore", str(manifest)])
    assert ec == 0
    validate_structure(events)

    actions = [e for e in events if e["type"] == "action" and e["kind"] == "restore"]
    assert len(actions) == 1
    assert actions[0]["dry_run"] is False
    assert actions[0]["src"] == str(quar_dir / "back.jpg")
    assert actions[0]["dst"] == str(tmp / "back.jpg")
    assert (tmp / "back.jpg").exists()
    assert not (quar_dir / "back.jpg").exists()


# ------------------------------ Test runner ---------------------------------


def discover_tests() -> list[Callable[[Path], None]]:
    g = globals()
    return [g[n] for n in sorted(g) if n.startswith("test_") and callable(g[n])]


def main() -> int:
    if not TWINCUT.exists():
        print(f"FATAL: twincut.sh not found at {TWINCUT}", file=sys.stderr)
        return 2

    results: list[TestResult] = []
    for fn in discover_tests():
        with tempfile.TemporaryDirectory(prefix="twincut-test-") as td:
            tmp = Path(td)
            try:
                fn(tmp)
                results.append(TestResult(fn.__name__, True))
                print(f"  ok  {fn.__name__}")
            except Exception as e:
                detail = "".join(traceback.format_exception_only(type(e), e)).strip()
                results.append(TestResult(fn.__name__, False, detail))
                print(f"  FAIL {fn.__name__}: {detail}", file=sys.stderr)
                if "-v" in sys.argv:
                    traceback.print_exc()

    failed = [r for r in results if not r.ok]
    print(f"\n{len(results) - len(failed)}/{len(results)} passed")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
