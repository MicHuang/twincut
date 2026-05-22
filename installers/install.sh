#!/usr/bin/env bash
set -euo pipefail
PREFIX="${HOME}/.local/bin"
SELF_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
ROOT_DIR="$(cd -- "${SELF_DIR}/.." && pwd -P)"
mkdir -p "${PREFIX}"
ln -sf "${ROOT_DIR}/bin/twincut.sh" "${PREFIX}/twincut"
ln -sf "${ROOT_DIR}/bin/vid_eq.sh"  "${PREFIX}/vid_eq"
echo "Installed:"
echo "  ${PREFIX}/twincut"
echo "  ${PREFIX}/vid_eq"
ln -sf "${ROOT_DIR}/bin/phash.py"   "${PREFIX}/phash"
echo "  ${PREFIX}/phash"
# Best-effort: install python deps for L1 perceptual hash.
# Failure is non-fatal — runtime will warn and skip the pHash phase.
if command -v pip3 >/dev/null 2>&1; then
  if pip3 install --user --quiet pillow imagehash 2>/dev/null; then
    echo "  installed pillow + imagehash (L1 pHash pairing enabled)"
  else
    echo "  NOTE: pip3 install pillow imagehash failed; L1 pHash pairing will be skipped at runtime"
    echo "  retry manually: pip3 install --user pillow imagehash"
  fi
else
  echo "  NOTE: pip3 not found; for L1 pHash pairing, install python3 then:"
  echo "    pip3 install --user pillow imagehash"
fi
if [[ -x "${ROOT_DIR}/bin/twincut-ui" ]]; then
  ln -sf "${ROOT_DIR}/bin/twincut-ui" "${PREFIX}/twincut-ui"
  echo "  ${PREFIX}/twincut-ui"
else
  echo "NOTE: bin/twincut-ui not built yet — run 'make build' to enable the Web UI"
fi
case ":$PATH:" in
  *":${PREFIX}:"*) : ;;
  *) echo "TIP: add to PATH -> export PATH=\"${PREFIX}:\$PATH\"" ;;
esac
