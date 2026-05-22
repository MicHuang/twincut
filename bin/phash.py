#!/usr/bin/env python3
"""bin/phash.py — perceptual-hash leaf primitive for twincut.

Protocol:
  stdin:  one absolute path per line (or NUL-separated with --null-in).
  stdout: `path\thash_hex` per successful path (input order preserved).
  stderr: `path\tERROR\t<reason>` per failed path.
  exit:   0 ran to completion; 2 usage error; 3 missing pillow/imagehash.
"""

import argparse
import sys


def parse_args(argv):
    p = argparse.ArgumentParser(prog="phash", description="perceptual hash filter")
    p.add_argument("--algo", choices=("dhash", "phash"), default="dhash")
    p.add_argument("--hash-size", type=int, default=8)
    p.add_argument("--null-in", action="store_true",
                   help="stdin paths are NUL-separated")
    p.add_argument("--pair", action="store_true",
                   help="pairing mode: read path<TAB>hash<TAB>role from stdin, "
                        "emit suspect<TAB>keeper<TAB>distance for each match")
    p.add_argument("--hamming", type=int, default=5,
                   help="Hamming distance threshold for --pair mode (default 5)")
    return p.parse_args(argv)


def read_paths(null_in):
    if null_in:
        data = sys.stdin.buffer.read()
        for chunk in data.split(b"\x00"):
            if chunk:
                yield chunk.decode("utf-8", "surrogateescape")
    else:
        for line in sys.stdin:
            line = line.rstrip("\n")
            if line:
                yield line


def pair_mode(threshold):
    # surrogateescape on both ends so non-UTF-8 paths (legacy macOS
    # filenames, NFD/NFC mixups) don't crash the entire pairing pass.
    sys.stdout.reconfigure(errors="surrogateescape")
    sys.stderr.reconfigure(errors="surrogateescape")
    suspects = []
    keepers = []
    for lineno, raw in enumerate(sys.stdin.buffer, 1):
        line = raw.decode("utf-8", "surrogateescape").rstrip("\n")
        if not line:
            continue
        parts = line.split("\t")
        if len(parts) != 3:
            sys.stderr.write(f"phash --pair: line {lineno}: malformed (expected 3 tab-fields): {line!r}\n")
            continue
        path, hash_hex, role = parts
        try:
            h = int(hash_hex, 16)
        except ValueError:
            sys.stderr.write(f"phash --pair: line {lineno}: bad hash for {path!r}: {hash_hex!r}\n")
            continue
        if role == "suspect":
            suspects.append((path, h))
        elif role == "keeper":
            keepers.append((path, h))
        else:
            sys.stderr.write(f"phash --pair: line {lineno}: unknown role {role!r} for {path!r}, skipping\n")
            continue
    # Sort keepers by path for deterministic tie-break (lex-smallest keeper wins)
    keepers.sort(key=lambda kv: kv[0])
    for sp, sh in suspects:
        best_path = None
        best_dist = threshold + 1
        for kp, kh in keepers:
            d = bin(sh ^ kh).count("1")
            if d <= threshold:
                if d < best_dist or (d == best_dist and (best_path is None or kp < best_path)):
                    best_dist = d
                    best_path = kp
        if best_path is not None:
            sys.stdout.write(f"{sp}\t{best_path}\t{best_dist}\n")
    return 0


def main(argv):
    args = parse_args(argv)

    if args.pair:
        return pair_mode(args.hamming)

    try:
        from PIL import Image  # noqa: F401
        import imagehash
    except ImportError as e:
        sys.stderr.write(
            f"phash: missing dependency ({e.name}); "
            f"install with: pip3 install --user pillow imagehash\n"
        )
        return 3

    from PIL import Image as PILImage
    from PIL import UnidentifiedImageError

    hash_fn = imagehash.dhash if args.algo == "dhash" else imagehash.phash

    for path in read_paths(args.null_in):
        try:
            with PILImage.open(path) as im:
                im.load()
                h = hash_fn(im, hash_size=args.hash_size)
            sys.stdout.write(f"{path}\t{h}\n")
            sys.stdout.flush()
        except (UnidentifiedImageError, OSError, ValueError) as e:
            reason = type(e).__name__
            sys.stderr.write(f"{path}\tERROR\t{reason}\n")
            sys.stderr.flush()
        except PILImage.DecompressionBombError:
            sys.stderr.write(f"{path}\tERROR\tDecompressionBombError\n")
            sys.stderr.flush()
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
