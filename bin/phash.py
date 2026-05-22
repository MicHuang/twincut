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


def main(argv):
    args = parse_args(argv)
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
