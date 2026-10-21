#!/bin/sh
#
# agentty installer.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/smallnest/agentty/master/scripts/install.sh | sh
#   curl -fsSL ... | sh -s -- --version v0.1.0
#   curl -fsSL ... | AGENTTY_INSTALL_DIR="$HOME/bin" sh
#
# Environment:
#   AGENTTY_VERSION       release tag to install (default: latest)
#   AGENTTY_INSTALL_DIR   destination directory
#   AGENTTY_BASE_URL      release download base URL (mirrors/air-gapped installs)

set -eu

REPO="smallnest/agentty"
BINARY="agentty"

say() { printf '%s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

usage() {
	cat <<'EOF'
Install agentty from GitHub Releases.

Usage:
  install.sh [--version <tag>] [--dir <path>]

Options:
  -v, --version <tag>   Install a specific release tag (default: latest)
  -d, --dir <path>      Install directory (default: /usr/local/bin, or ~/.local/bin)
  -h, --help            Show this help

Environment:
  AGENTTY_VERSION       Same as --version
  AGENTTY_INSTALL_DIR   Same as --dir
  AGENTTY_BASE_URL      Override the release download base URL
EOF
}

need() {
	command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

download() {
	url=$1
	dest=$2
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$url" -o "$dest"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO "$dest" "$url"
	else
		die "curl or wget is required"
	fi
}

download_stdout() {
	url=$1
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$url"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO- "$url"
	else
		die "curl or wget is required"
	fi
}

latest_tag() {
	# Prefer the redirect from /releases/latest: no API rate limit.
	if command -v curl >/dev/null 2>&1; then
		effective=$(curl -fsSL -o /dev/null -w '%{url_effective}' \
			"https://github.com/${REPO}/releases/latest" 2>/dev/null || true)
		case "$effective" in
			*/tag/*)
				printf '%s\n' "${effective##*/}"
				return 0
				;;
		esac
	fi

	# Fall back to the API, which works with curl or wget.
	json=$(download_stdout "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null || true)
	tag=$(printf '%s' "$json" |
		sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' |
		head -n 1)
	if [ -n "$tag" ]; then
		printf '%s\n' "$tag"
	fi
	return 0
}

VERSION="${AGENTTY_VERSION:-}"
INSTALL_DIR="${AGENTTY_INSTALL_DIR:-}"

while [ $# -gt 0 ]; do
	case "$1" in
		--version|-v)
			[ $# -ge 2 ] || die "missing value for $1"
			VERSION=$2
			shift 2
			;;
		--dir|-d)
			[ $# -ge 2 ] || die "missing value for $1"
			INSTALL_DIR=$2
			shift 2
			;;
		--help|-h)
			usage
			exit 0
			;;
		*)
			die "unknown option: $1"
			;;
	esac
done

need uname
need tar

os=$(uname -s)
case "$os" in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	MINGW*|MSYS*|CYGWIN*) os=windows ;;
	*) die "unsupported operating system: $os" ;;
esac

arch=$(uname -m)
case "$arch" in
	x86_64|amd64) arch=amd64 ;;
	arm64|aarch64) arch=arm64 ;;
	*) die "unsupported architecture: $arch" ;;
esac

if [ "$os" = "windows" ] && [ "$arch" = "arm64" ]; then
	die "windows/arm64 is not published; use amd64 or WSL"
fi

if [ -z "$VERSION" ]; then
	VERSION=$(latest_tag)
fi
[ -n "$VERSION" ] || die "could not determine the latest release; pass --version vX.Y.Z"

tag=$VERSION
case "$tag" in
	v*) ;;
	*) tag="v$tag" ;;
esac
version=${tag#v}

case "$os" in
	windows) ext=zip ;;
	*) ext=tar.gz ;;
esac
archive="${BINARY}_${version}_${os}_${arch}.${ext}"
base="${AGENTTY_BASE_URL:-https://github.com/${REPO}/releases/download}"
url="${base}/${tag}/${archive}"
checksums_url="${base}/${tag}/checksums.txt"

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t agentty)
trap 'rm -rf "$tmp"' 0 1 2 15

say "Downloading ${url}"
download "$url" "$tmp/$archive"
download "$checksums_url" "$tmp/checksums.txt"

expected=$(awk -v file="$archive" '$2 == file { print $1; exit }' "$tmp/checksums.txt")
[ -n "$expected" ] || die "no checksum for ${archive}"
if command -v sha256sum >/dev/null 2>&1; then
	actual=$(sha256sum "$tmp/$archive" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
	actual=$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')
else
	die "sha256sum or shasum is required"
fi
[ "$actual" = "$expected" ] || die "checksum mismatch for ${archive}"

case "$ext" in
	tar.gz)
		tar -xzf "$tmp/$archive" -C "$tmp"
		;;
	zip)
		need unzip
		unzip -q "$tmp/$archive" -d "$tmp"
		;;
esac

bin="$tmp/$BINARY"
if [ ! -f "$bin" ] && [ -f "$bin.exe" ]; then
	bin="$bin.exe"
fi
[ -f "$bin" ] || die "archive did not contain ${BINARY}"

bin_name="$BINARY"
if [ "$os" = "windows" ]; then
	bin_name="${BINARY}.exe"
fi

if [ -z "$INSTALL_DIR" ]; then
	if [ "$(id -u)" -eq 0 ]; then
		INSTALL_DIR=/usr/local/bin
	else
		INSTALL_DIR="${HOME}/.local/bin"
	fi
fi
mkdir -p "$INSTALL_DIR"
[ -w "$INSTALL_DIR" ] || die "${INSTALL_DIR} is not writable; set AGENTTY_INSTALL_DIR to a writable directory"

if command -v install >/dev/null 2>&1; then
	install -m 0755 "$bin" "$INSTALL_DIR/$bin_name"
else
	cp "$bin" "$INSTALL_DIR/$bin_name"
	chmod 0755 "$INSTALL_DIR/$bin_name"
fi

say "Installed ${BINARY} ${tag} to ${INSTALL_DIR}/${bin_name}"
case ":${PATH}:" in
	*":${INSTALL_DIR}:"*)
		"$INSTALL_DIR/$bin_name" version || true
		;;
	*)
		say "Add ${INSTALL_DIR} to PATH to run ${BINARY}."
		;;
esac
