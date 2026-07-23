#!/usr/bin/env bash
# Native build for Linux/macOS CPA plugin (.so / .dylib).
# Usage:
#   ./build.sh                 # native GOOS/GOARCH
#   ./build.sh linux/amd64
#   ./build.sh linux/arm64
#   ./build.sh darwin/arm64
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"
VERSION="0.1.16"
export CGO_ENABLED=1

TARGET="${1:-$(go env GOOS)/$(go env GOARCH)}"
GOOS="${TARGET%%/*}"
GOARCH="${TARGET##*/}"
export GOOS GOARCH

case "$GOOS" in
  windows) EXT=".dll" ;;
  darwin)  EXT=".dylib" ;;
  *)       EXT=".so" ;;
esac

echo "tidy + test (host)"
# Run tests without forcing foreign GOOS for cgo test binaries when possible
(
  unset GOOS GOARCH || true
  export CGO_ENABLED=1
  go mod tidy
  go test ./...
)

mkdir -p "dist/${GOOS}/${GOARCH}"
OUT="dist/grok-quota${EXT}"
VER="dist/grok-quota-v${VERSION}-${GOOS}-${GOARCH}${EXT}"
PLAT="dist/${GOOS}/${GOARCH}/grok-quota${EXT}"

echo "build ${GOOS}/${GOARCH} -> ${OUT}"
go build -buildvcs=false -buildmode=c-shared -trimpath -ldflags='-s -w' -o "${OUT}" .
cp -f "${OUT}" "${VER}"
cp -f "${OUT}" "${PLAT}"
rm -f dist/grok-quota.h "dist/grok-quota-v${VERSION}.h" "dist/${GOOS}/${GOARCH}/grok-quota.h" || true

if command -v sha256sum >/dev/null 2>&1; then
  sha256sum "${OUT}"
elif command -v shasum >/dev/null 2>&1; then
  shasum -a 256 "${OUT}"
fi
echo "OK ${VER}"
echo "Install to CPA: plugins/${GOOS}/${GOARCH}/grok-quota${EXT}"
