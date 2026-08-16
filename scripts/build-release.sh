#!/usr/bin/env bash
# Builds onebox for Windows/Linux/macOS (amd64 + arm64) into dist/, plus a
# checksums file. Pure Go, no cgo (see ROADMAP.md's Month 3 decision note)
# is what makes single-command cross-compilation like this possible.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

VERSION="${1:-dev}"
OUT_DIR="dist"
rm -rf "$OUT_DIR"
mkdir -p "$OUT_DIR"

export CGO_ENABLED=0

# Best-effort: a source tarball / shallow clone without .git still builds
# fine, just reporting "unknown" for the commit (see main.go's commit
# var doc comment) rather than failing the whole release build.
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"

targets=(
  "windows amd64 .exe"
  "linux   amd64 "
  "linux   arm64 "
  "darwin  amd64 "
  "darwin  arm64 "
)

for target in "${targets[@]}"; do
  read -r goos goarch ext <<< "$target"
  name="onebox-${VERSION}-${goos}-${goarch}${ext}"
  echo "building $name..."
  GOOS="$goos" GOARCH="$goarch" go build -ldflags "-s -w -X main.version=$VERSION -X main.commit=$COMMIT" -o "$OUT_DIR/$name" ./cmd/onebox
done

echo "checksums..."
(
  cd "$OUT_DIR"
  if command -v sha256sum >/dev/null; then
    sha256sum onebox-* > checksums.txt
  else
    shasum -a 256 onebox-* > checksums.txt
  fi
)

echo "done — binaries in $OUT_DIR/"
ls -la "$OUT_DIR"
