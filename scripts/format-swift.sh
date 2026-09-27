#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if ! command -v docker >/dev/null 2>&1; then
  echo "ERROR: Docker is required to format Swift sources." >&2
  exit 1
fi

docker run --rm \
  --volume "${repo_root}:/workspace" \
  --workdir /workspace \
  swift:6.2 \
  swift-format format --in-place --recursive \
  platform/darwin/vm/swift/Package.swift \
  platform/darwin/vm/swift/Sources \
  platform/darwin/vm/swift/Tests
