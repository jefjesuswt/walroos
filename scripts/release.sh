#!/usr/bin/env bash
# Builds the release binaries into dist/: the web UI first (it's embedded), then one static
# binary per platform, and SHA256SUMS. Linux binaries run the hub and nodes; macOS and Windows
# ones are the CLI for your laptop (walroos ssh-config). Usage: scripts/release.sh v0.2.0
set -euo pipefail

version="${1:?usage: scripts/release.sh <version, e.g. v0.2.0>}"
cd "$(dirname "$0")/.."

(cd web && bun install --frozen-lockfile && bun run build)

rm -rf dist && mkdir dist
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
	os="${target%/*}" arch="${target#*/}"
	out="dist/walroos_${os}_${arch}"
	[ "$os" = windows ] && out="$out.exe"
	echo "building $out"
	GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 go build -trimpath \
		-ldflags "-s -w -X github.com/jefjesuswt/walroos/internal/version.Version=${version#v}" \
		-o "$out" ./cmd/walroos
done
(cd dist && sha256sum walroos_* > SHA256SUMS)
cat dist/SHA256SUMS
