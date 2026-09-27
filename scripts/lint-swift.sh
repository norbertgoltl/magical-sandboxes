#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if ! command -v docker >/dev/null 2>&1; then
  echo "ERROR: Docker is required to run the Swift formatter." >&2
  exit 1
fi

docker run --rm \
  --platform linux/arm64 \
  --volume "${repo_root}:/workspace:ro" \
  --workdir /workspace \
  swift:6.2 \
  swift-format lint --strict --recursive \
  platform/darwin/vm/swift/Package.swift \
  platform/darwin/vm/swift/Sources \
  platform/darwin/vm/swift/Tests
