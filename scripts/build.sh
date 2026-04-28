#!/bin/bash
set -euo pipefail

VERSION=${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo "dev")}
LDFLAGS="-s -w -X main.Version=${VERSION}"
OUTPUT="bin/shark-mqtt"

echo "Building shark-mqtt ${VERSION}..."
mkdir -p bin
go build -ldflags "${LDFLAGS}" -o "${OUTPUT}" ./cmd/...
echo "Built: ${OUTPUT}"
