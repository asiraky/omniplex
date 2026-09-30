#!/usr/bin/env bash
# Build one bundled Omniplex server binary for a target OS and architecture.
#
#   scripts/build-release.sh <goos> <goarch> <version> <output-path>
#
# The release workflow calls this once per target, from whatever runner it is
# on: Go cross-compiles (CGO off, modernc sqlite) and `bun build --compile`
# cross-compiles the Claude sidecar, so every target builds anywhere.
#
# Expects `npm ci` and `npm run build:web` to have run, so the web bundle is
# already in cmd/omniplex/webdist and the sidecar's dependencies are installed.
set -euo pipefail

if [ "$#" -ne 4 ]; then
  echo "usage: $0 <goos> <goarch> <version> <output-path>" >&2
  exit 2
fi
goos=$1 goarch=$2 version=$3 out=$4

# Bun's target names. The x64 targets use the baseline build, which runs on
# CPUs without AVX2; the sidecar is an SDK bridge, so the speed difference is
# irrelevant and an old Intel Mac or VM still works.
case "$goos/$goarch" in
  darwin/arm64) bun_target=bun-darwin-arm64 ;;
  darwin/amd64) bun_target=bun-darwin-x64-baseline ;;
  linux/arm64) bun_target=bun-linux-arm64 ;;
  linux/amd64) bun_target=bun-linux-x64-baseline ;;
  windows/amd64) bun_target=bun-windows-x64-baseline ;;
  windows/arm64) bun_target=bun-windows-arm64 ;;
  *)
    echo "unsupported target $goos/$goarch" >&2
    exit 2
    ;;
esac

root=$(cd "$(dirname "$0")/.." && pwd)
sidecar="$root/internal/adapter/claudecode/sidecar"

rm -rf "$sidecar/dist"
(cd "$sidecar" && bun build --compile --target="$bun_target" sidecar.mjs --outfile dist/omniplex-claude-sidecar)

# Bun appends .exe for Windows targets. bundled.go embeds the extension-less
# name on every platform, so put it back where the embed looks for it.
if [ -f "$sidecar/dist/omniplex-claude-sidecar.exe" ]; then
  mv "$sidecar/dist/omniplex-claude-sidecar.exe" "$sidecar/dist/omniplex-claude-sidecar"
fi

mkdir -p "$(dirname "$out")"
(
  cd "$root"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build \
    -tags bundled_sidecar \
    -trimpath \
    -ldflags "-s -w -X main.version=$version" \
    -o "$out" \
    ./cmd/omniplex
)
echo "built $out ($goos/$goarch, $version)"
