#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd -P)"
if [[ $# -ne 1 ]]; then
  echo "Usage: ./scripts/debug-agent.sh <target-project-directory>" >&2
  exit 2
fi

PROJECT_DIR="$(cd "$1" && pwd -P)"
PROJECT_ID="$(printf '%s' "$PROJECT_DIR" | shasum -a 256 | awk '{print $1}')"
SB="$ROOT/.msbx-dev/sandboxes/projects/$PROJECT_ID"
for f in "$SB/disk.raw" "$SB/efi-vars.fd"; do
  [[ -f "$f" ]] || { echo "Missing $f" >&2; exit 1; }
done
if [[ "$PROJECT_DIR" == "$ROOT" || "$PROJECT_DIR" == "$ROOT/"* || "$ROOT" == "$PROJECT_DIR/"* ]]; then
  echo "Refusing to share a directory that overlaps the msbx installation tree." >&2
  exit 1
fi
echo "Booting Debian debug console."
echo "Inside the guest, useful commands are:"
echo "  systemctl status msbx-agent --no-pager"
echo "  journalctl -u msbx-agent -b --no-pager -n 100"
exec "$ROOT/bin/msbx-vm" boot-disk \
  --disk "$SB/disk.raw" \
  --efi-store "$SB/efi-vars.fd" \
  --share "$PROJECT_DIR" \
  --cpus 4 --memory-mib 4096
