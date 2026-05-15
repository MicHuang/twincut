#!/usr/bin/env bash
set -euo pipefail
PREFIX="${HOME}/.local/bin"
rm -f "${PREFIX}/twincut" "${PREFIX}/vid_eq" "${PREFIX}/twincut-ui"
echo "Uninstalled from ${PREFIX}"
