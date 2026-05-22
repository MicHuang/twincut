#!/usr/bin/env bash
set -euo pipefail
PREFIX="${HOME}/.local/bin"
rm -f "${PREFIX}/twincut" "${PREFIX}/vid_eq" "${PREFIX}/twincut-ui" "${PREFIX}/phash"
# Note: we do NOT pip-uninstall pillow / imagehash — user may rely on them.
echo "Uninstalled from ${PREFIX}"
