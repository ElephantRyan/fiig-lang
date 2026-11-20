#!/bin/sh
# Installs the fiig toolchain from a GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/ElephantRyan/fiig-lang/main/install.sh | sh
#
# FIIG_VERSION  a release tag such as v0.1.0-alpha.2 (default: the newest
#               release, pre-releases included: every alpha is one)
# FIIG_INSTALL  the directory to put `fiig` in (default: ~/.local/bin)
set -eu

repo="ElephantRyan/fiig-lang"
dest="${FIIG_INSTALL:-$HOME/.local/bin}"

fail() { echo "install: $*" >&2; exit 1; }

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) fail "unsupported OS $(uname -s); on Windows use install.ps1" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) fail "unsupported CPU $(uname -m)" ;;
esac

if command -v curl >/dev/null 2>&1; then
  get() { curl -fsSL "$1"; }
elif command -v wget >/dev/null 2>&1; then
  get() { wget -qO- "$1"; }
else
  fail "needs curl or wget"
fi

version="${FIIG_VERSION:-}"
if [ -z "$version" ]; then
  # /releases/latest skips pre-releases, so ask for the newest release.
  version=$(get "https://api.github.com/repos/$repo/releases?per_page=1" |
    sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)
  [ -n "$version" ] || fail "could not find a release of $repo"
fi

name="fiig-$version-$os-$arch"
base="https://github.com/$repo/releases/download/$version"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading fiig $version ($os/$arch)"
get "$base/$name.tar.gz" >"$tmp/$name.tar.gz" || fail "no $name.tar.gz in release $version"
get "$base/SHA256SUMS" >"$tmp/SHA256SUMS" || fail "no SHA256SUMS in release $version"

want=$(grep " $name.tar.gz\$" "$tmp/SHA256SUMS" | awk '{print $1}')
[ -n "$want" ] || fail "SHA256SUMS has no entry for $name.tar.gz"
if command -v sha256sum >/dev/null 2>&1; then
  have=$(sha256sum "$tmp/$name.tar.gz" | awk '{print $1}')
else
  have=$(shasum -a 256 "$tmp/$name.tar.gz" | awk '{print $1}')
fi
[ "$have" = "$want" ] || fail "checksum mismatch for $name.tar.gz"

tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
mkdir -p "$dest"
cp "$tmp/$name/fiig" "$dest/fiig"
chmod +x "$dest/fiig"

installed=$("$dest/fiig" version) || fail "the downloaded fiig does not run on this machine"
echo "Installed $(echo "$installed" | head -n 1) to $dest/fiig"
case ":$PATH:" in
  *":$dest:"*) ;;
  *) echo "Add it to your PATH:  export PATH=\"$dest:\$PATH\"" ;;
esac
echo "Next: docs/getting-started.md in the repository, or  fiig help"
