#!/bin/sh
# Installs the seed CLI from the latest release:
#   curl -fsSL https://raw.githubusercontent.com/paezao/seed/main/install.sh | sh
# SEED_BIN_DIR (default ~/.local/bin) chooses where it goes; SEED_VERSION
# (e.g. 2026.10.20) installs that release instead of the latest.
set -eu

repo="paezao/seed"
bin_dir="${SEED_BIN_DIR:-$HOME/.local/bin}"

for tool in curl tar; do
  command -v "$tool" >/dev/null 2>&1 || { echo "seed's installer needs $tool" >&2; exit 1; }
done

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
# An Intel shell on Apple Silicon (Rosetta) still gets the native build.
if [ "$os" = darwin ] && [ "$arch" = amd64 ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = 1 ]; then
  arch=arm64
fi

# Settle on one release first, so the archive and its checksum come from the
# same one even if a new release is published meanwhile.
if [ -n "${SEED_RELEASES_URL:-}" ]; then
  base="$SEED_RELEASES_URL"
  version="(from $base)"
else
  if [ -n "${SEED_VERSION:-}" ]; then
    tag="v${SEED_VERSION#v}"
  else
    latest="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest")"
    tag="${latest##*/}"
    case "$tag" in v*) ;; *) echo "couldn't find the latest release of $repo" >&2; exit 1 ;; esac
  fi
  base="https://github.com/$repo/releases/download/$tag"
  version="$tag"
fi

file="seed_${os}_${arch}.tar.gz"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
echo "downloading seed $version for $os/$arch…"
curl -fsSL "$base/$file" -o "$tmp/$file"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"
grep " $file\$" "$tmp/checksums.txt" > "$tmp/expected" || { echo "no checksum for $file" >&2; exit 1; }
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$tmp" && sha256sum -c expected >/dev/null 2>&1)
else
  (cd "$tmp" && shasum -a 256 -c expected >/dev/null 2>&1)
fi || { echo "checksum mismatch for $file: not installing" >&2; exit 1; }

tar -xzf "$tmp/$file" -C "$tmp" seed
mkdir -p "$bin_dir"
install -m 0755 "$tmp/seed" "$bin_dir/seed"
echo "installed seed $version to $bin_dir/seed"

case ":$PATH:" in
  *":$bin_dir:"*)
    found="$(command -v seed 2>/dev/null || true)"
    if [ -n "$found" ] && [ "$found" != "$bin_dir/seed" ]; then
      echo "note: $found comes first in your PATH; remove it, or put $bin_dir before it"
    fi
    ;;
  *)
    echo "note: $bin_dir isn't in your PATH; add it, e.g.:"
    echo "  echo 'export PATH=\"$bin_dir:\$PATH\"' >> ~/.profile"
    ;;
esac
if ! command -v docker >/dev/null 2>&1 && ! command -v podman >/dev/null 2>&1; then
  echo "note: seed runs each Seed in a container: install Docker (or Podman) first"
fi
echo
echo "Seed needs Docker running (or Podman, with SEED_CONTAINER_ENGINE=podman) and an OpenRouter key. Then:"
echo "  export OPENROUTER_API_KEY=sk-or-…"
echo "  seed new myapp -e OPENROUTER_API_KEY"
