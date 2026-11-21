#!/bin/sh
# Installs the walroos binary from the latest GitHub release, after checking its SHA-256.
#
#   curl -fsSL https://raw.githubusercontent.com/jefjesuswt/walroos/main/install.sh | sh
#
# WALROOS_VERSION=v0.2.0 picks a release; WALROOS_INSTALL_DIR (default ~/.local/bin) where it goes.
set -eu

repo="jefjesuswt/walroos"
version="${WALROOS_VERSION:-latest}"
dir="${WALROOS_INSTALL_DIR:-$HOME/.local/bin}"

fail() { echo "walroos install: $*" >&2; exit 1; }

case "$(uname -s)" in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	*) fail "unsupported system $(uname -s); on Windows download walroos_windows_amd64.exe from https://github.com/$repo/releases" ;;
esac
case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) fail "unsupported CPU $(uname -m)" ;;
esac
file="walroos_${os}_${arch}"

if [ "$version" = latest ]; then
	base="${WALROOS_BASE_URL:-https://github.com/$repo/releases/latest/download}"
else
	base="${WALROOS_BASE_URL:-https://github.com/$repo/releases/download/$version}"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
echo "Downloading $file ($version)..."
curl -fsSL -o "$tmp/$file" "$base/$file" || fail "can't download $base/$file"
curl -fsSL -o "$tmp/SHA256SUMS" "$base/SHA256SUMS" || fail "can't download the checksums"

want="$(grep " $file\$" "$tmp/SHA256SUMS" | cut -d' ' -f1)"
[ -n "$want" ] || fail "$file isn't in SHA256SUMS"
if command -v sha256sum >/dev/null; then
	got="$(sha256sum "$tmp/$file" | cut -d' ' -f1)"
else
	got="$(shasum -a 256 "$tmp/$file" | cut -d' ' -f1)"
fi
[ "$got" = "$want" ] || fail "checksum mismatch for $file: the download is corrupt or was tampered with"

mkdir -p "$dir"
install -m 0755 "$tmp/$file" "$dir/walroos"
echo "Installed $("$dir/walroos" --version) to $dir/walroos"
case ":$PATH:" in
	*":$dir:"*) ;;
	*) echo "Add it to your PATH:  export PATH=\"$dir:\$PATH\"" ;;
esac
echo "Next: https://github.com/$repo#quick-start"
