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
