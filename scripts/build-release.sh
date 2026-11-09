#!/bin/bash
# Custom build script for multi-platform builds of awmg
# Pass optional platform arguments to build a subset in parallel release jobs.
set -euo pipefail

VERSION="${1:-}"

if [ -z "$VERSION" ]; then
  echo "error: VERSION argument is required" >&2
  exit 1
fi

platforms=(
  linux-amd64
  linux-arm
  linux-arm64
)

if [ "$#" -gt 1 ]; then
  platforms=("${@:2}")
fi

for p in "${platforms[@]}"; do
  case "$p" in
    linux-amd64|linux-arm|linux-arm64) ;;
    *)
      echo "error: unsupported release platform $p" >&2
      exit 1
      ;;
  esac
done

echo "Building binaries with version: $VERSION"

# Create dist directory if it doesn't exist
mkdir -p dist

IFS=$'\n' read -d '' -r -a supported_platforms < <(go tool dist list) || true

for p in "${platforms[@]}"; do
  goos="${p%-*}"
  goarch="${p#*-}"
  
  # Check if platform is supported
  if [[ " ${supported_platforms[*]} " != *" ${goos}/${goarch} "* ]]; then
    echo "warning: skipping unsupported platform $p" >&2
    continue
  fi
  
  ext=""
  if [ "$goos" = "windows" ]; then
    ext=".exe"
  fi
  
  echo "Building awmg for $p..."
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build \
    -trimpath \
    -ldflags="-s -w -X main.Version=${VERSION}" \
    -o "dist/awmg-${p}${ext}" \
    .
  
done

echo "Build complete. Binaries:"
ls -lh dist/

# Generate checksums file
echo ""
echo "Generating checksums..."
cd dist
# Use sha256sum if available (Linux), otherwise use shasum (macOS)
if command -v sha256sum &> /dev/null; then
  sha256sum awmg-* > checksums.txt
elif command -v shasum &> /dev/null; then
  shasum -a 256 awmg-* > checksums.txt
else
  echo "error: neither sha256sum nor shasum is available" >&2
  exit 1
fi
cd ..

echo "Checksums generated:"
cat dist/checksums.txt
