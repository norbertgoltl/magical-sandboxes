#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

echo "magical-sandboxes bootstrap"
echo

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "ERROR: v1 currently targets macOS." >&2
  exit 1
fi

if [[ "$(uname -m)" != "arm64" ]]; then
  echo "ERROR: v1 currently targets Apple Silicon (arm64)." >&2
  exit 1
fi

if ! xcode-select -p >/dev/null 2>&1; then
  echo "Xcode Command Line Tools are missing."
  echo "Install them with: xcode-select --install"
  exit 1
fi

if ! command -v swift >/dev/null 2>&1; then
  echo "ERROR: Swift is not available even though Xcode Command Line Tools were expected." >&2
  exit 1
fi

echo "✓ Xcode Command Line Tools"
echo "✓ Swift: $(swift --version | head -n 1)"

echo
echo "Building native macOS VM helper..."
mkdir -p bin
swift build -c release --package-path platform/darwin/vm/swift
SWIFT_BIN_DIR="$(swift build -c release --package-path platform/darwin/vm/swift --show-bin-path)"
cp "$SWIFT_BIN_DIR/msbx-vm" bin/msbx-vm
chmod +x bin/msbx-vm

ENTITLEMENTS="platform/darwin/vm/swift/Resources/msbx-vm.entitlements"
echo "Signing native VM helper with Virtualization entitlement..."
codesign --force --sign - --entitlements "$ENTITLEMENTS" bin/msbx-vm

if ! codesign --display --entitlements :- bin/msbx-vm 2>&1 | grep -q "com.apple.security.virtualization"; then
  echo "ERROR: msbx-vm was built but the virtualization entitlement is missing." >&2
  exit 1
fi

echo "✓ Built and signed ./bin/msbx-vm"

echo
if command -v go >/dev/null 2>&1; then
  echo "✓ Go: $(go version)"
  echo "Building Go components..."
  go build -o bin/msbx ./cmd/msbx
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/msbx-agent-linux-arm64 ./cmd/msbx-agent
  echo "✓ Built ./bin/msbx"
  echo "✓ Built ./bin/msbx-agent-linux-arm64"
  echo
  echo "Run: ./bin/msbx doctor"
else
  echo "! Go is not installed."
  echo
  echo "Recommended for development:"
  echo "  brew install go"
  echo
  echo "If Homebrew is not installed, install Go from https://go.dev/dl/ and rerun this script."
  echo
  echo "The native virtualization probe is already built. Test it with:"
  echo "  ./bin/msbx-vm probe"
fi
