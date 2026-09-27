#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
source "$ROOT/dependencies.lock"
SB="$ROOT/.msbx-dev/sandboxes/template"
for f in "$SB/disk.raw" "$SB/seed.iso"; do
  [[ -f "$f" ]] || { echo "Missing $f. Run ./scripts/prepare-cloud-guest.sh first." >&2; exit 1; }
done

echo "Provisioning Debian guest with msbx-agent and Docker Engine, Codex CLI, Claude Code and OpenCode."
echo "cloud-init will power the VM off when finished."
rm -f "$SB/template.ready"
PROVISION_LOG="$SB/provision.log"
"$ROOT/bin/msbx-vm" provision-cloud \
  --disk "$SB/disk.raw" \
  --seed "$SB/seed.iso" \
  --efi-store "$SB/efi-vars.fd" \
  --cpus 4 --memory-mib 4096 </dev/null 2>&1 | tee "$PROVISION_LOG"

if ! grep -Fq '[msbx-provision] TEMPLATE READY' "$PROVISION_LOG"; then
  echo "Provisioning did not report successful template completion; the template remains unusable." >&2
  exit 1
fi

MARKER_TMP="$SB/.template.ready.tmp"
printf 'schema=1\ncreated_utc=%s\ndebian_image_version=%s\n' \
  "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$DEBIAN_IMAGE_VERSION" > "$MARKER_TMP"
mv "$MARKER_TMP" "$SB/template.ready"
echo "Template ready: $SB"
