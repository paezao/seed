#!/bin/sh
# Installs the seed CLI from the latest release:
#   curl -fsSL https://raw.githubusercontent.com/paezao/seed/main/install.sh | sh
# SEED_BIN_DIR (default ~/.local/bin) chooses where it goes.
set -eu

repo="paezao/seed"
base="${SEED_RELEASES_URL:-https://github.com/$repo/releases/latest/download}"
bin_dir="${SEED_BIN_DIR:-$HOME/.local/bin}"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in
  linux|darwin) ;;
  *) echo "seed runs on Linux and macOS (on Windows, install it inside WSL2)" >&2; exit 1 ;;
esac
arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "seed isn't built for $arch yet" >&2; exit 1 ;;
esac

file="seed_${os}_${arch}.tar.gz"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
echo "downloading $file…"
curl -fsSL "$base/$file" -o "$tmp/$file"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"
grep " $file\$" "$tmp/checksums.txt" > "$tmp/expected" || { echo "no checksum for $file" >&2; exit 1; }
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$tmp" && sha256sum -c --status expected)
else
  (cd "$tmp" && shasum -a 256 -c -s expected)
fi || { echo "checksum mismatch for $file: not installing" >&2; exit 1; }

tar -xzf "$tmp/$file" -C "$tmp" seed
mkdir -p "$bin_dir"
install -m 0755 "$tmp/seed" "$bin_dir/seed"
echo "installed seed to $bin_dir/seed"
case ":$PATH:" in
  *":$bin_dir:"*) ;;
  *) echo "note: add $bin_dir to your PATH" ;;
esac
echo
echo "Seed needs Docker (or Podman) running and an OpenRouter key. Then:"
echo "  export OPENROUTER_API_KEY=sk-or-…"
echo "  seed new myapp -e OPENROUTER_API_KEY"
