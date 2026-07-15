#!/bin/sh
# inkcap installer. Detects OS/arch, downloads the matching release archive
# from GitHub, verifies its checksum, and installs the binary.
#
#   curl -fsSL https://raw.githubusercontent.com/scttfrdmn/inkcap/main/install.sh | sh
#
# Environment:
#   INKCAP_VERSION   tag to install (default: latest release)
#   INKCAP_BIN_DIR   install directory (default: /usr/local/bin, or ~/.local/bin
#                    if that isn't writable)
set -eu

REPO="scttfrdmn/inkcap"
BINARY="inkcap"

err() { echo "install: $*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

# --- fetch helper (curl or wget) ---------------------------------------------
if have curl; then
	dl() { curl -fsSL "$1"; }
	dlo() { curl -fsSL "$1" -o "$2"; }
elif have wget; then
	dl() { wget -qO- "$1"; }
	dlo() { wget -qO "$2" "$1"; }
else
	err "need curl or wget"
fi

# --- detect platform ---------------------------------------------------------
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
	linux) os=linux ;;
	darwin) os=darwin ;;
	*) err "unsupported OS: $os (use the prebuilt binaries or 'go install')" ;;
esac

arch=$(uname -m)
case "$arch" in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) err "unsupported architecture: $arch" ;;
esac

# --- resolve version ---------------------------------------------------------
version="${INKCAP_VERSION:-}"
if [ -z "$version" ]; then
	version=$(dl "https://api.github.com/repos/$REPO/releases/latest" |
		grep '"tag_name"' | head -1 | cut -d'"' -f4)
	[ -n "$version" ] || err "could not determine the latest version"
fi
# strip a leading v for the archive name; keep it for the download path
ver_no_v=$(echo "$version" | sed 's/^v//')

archive="${BINARY}_${ver_no_v}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$version"

# --- download + verify -------------------------------------------------------
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "install: downloading $archive ($version)" >&2
dlo "$base/$archive" "$tmp/$archive" || err "download failed: $base/$archive"

echo "install: verifying checksum" >&2
if dlo "$base/checksums.txt" "$tmp/checksums.txt" 2>/dev/null; then
	want=$(grep " $archive\$" "$tmp/checksums.txt" | awk '{print $1}')
	if [ -n "$want" ]; then
		if have sha256sum; then
			got=$(sha256sum "$tmp/$archive" | awk '{print $1}')
		elif have shasum; then
			got=$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')
		else
			got=""
			echo "install: no sha256 tool found, skipping verification" >&2
		fi
		[ -z "$got" ] || [ "$got" = "$want" ] || err "checksum mismatch for $archive"
	fi
else
	echo "install: checksums.txt unavailable, skipping verification" >&2
fi

tar -xzf "$tmp/$archive" -C "$tmp"
[ -f "$tmp/$BINARY" ] || err "archive did not contain $BINARY"

# --- install -----------------------------------------------------------------
bindir="${INKCAP_BIN_DIR:-/usr/local/bin}"
if [ ! -d "$bindir" ] || [ ! -w "$bindir" ]; then
	if [ -z "${INKCAP_BIN_DIR:-}" ]; then
		bindir="$HOME/.local/bin"    # fall back to a user-writable dir
	fi
fi
mkdir -p "$bindir"

install -m 0755 "$tmp/$BINARY" "$bindir/$BINARY" 2>/dev/null ||
	{ mv "$tmp/$BINARY" "$bindir/$BINARY" && chmod 0755 "$bindir/$BINARY"; } ||
	err "could not install to $bindir (set INKCAP_BIN_DIR to a writable path)"

echo "install: installed $BINARY $version to $bindir/$BINARY" >&2
case ":$PATH:" in
	*":$bindir:"*) ;;
	*) echo "install: note: $bindir is not on your PATH" >&2 ;;
esac
