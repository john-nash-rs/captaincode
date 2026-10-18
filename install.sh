#!/bin/sh
# CaptainCode installer.
#
# Fetches the latest release for this platform, verifies its SHA-256 against
# the published checksums file, and installs the binary into a user-writable
# directory. No privileges are required for a default install.
#
#   curl -fsSL https://raw.githubusercontent.com/john-nash-rs/captaincode/main/install.sh | sh
#
# This script is served from the repository. If a custom domain is configured
# later, it and this header are updated together.
#
#   curl -fsSL https://raw.githubusercontent.com/john-nash-rs/captaincode/main/install.sh | sh
#
# RISK OF PIPING A REMOTE SCRIPT INTO A SHELL
# ------------------------------------------------
# This runs whatever the remote host serves, with your privileges, before you
# read any of it. That is inherent to the pattern, not a flaw in this script.
# Mitigations, in decreasing order of preference:
#
#   1. Download, read, then run:
#          curl -fsSLO https://raw.githubusercontent.com/john-nash-rs/captaincode/main/install.sh
#          less install.sh
#          sh install.sh
#   2. Pin the version being installed:  sh install.sh --version v0.1.0
#   3. Verify out of band: the binary is checked against the release's
#      checksums.txt before it is installed, so a corrupted download fails
#      closed rather than executing.
#
# TLS verification is never disabled. The checksum file is fetched over the
# same verified channel as the archive.

set -eu

REPO="john-nash-rs/captaincode"
API="https://api.github.com"
INSTALL_DIR="${CAPTAINCODE_INSTALL_DIR:-${HOME}/.local/bin}"
VERSION=""
FORCE=0
ASSUME_YES=0

# ---------------------------------------------------------------------------
# Output helpers. Everything a user reads goes to stdout; diagnostics go to
# stderr so the script stays usable in pipelines.
# ---------------------------------------------------------------------------
info() { printf '%s\n' "$*"; }
warn() { printf '%s\n' "$*" >&2; }
die()  { printf 'error: %s\n' "$*" >&2; exit 1; }

usage() {
	cat <<'USAGE'
captaincode installer

Usage:
  install.sh [options]

Options:
  -d, --dir DIR      Install into DIR (default: ~/.local/bin)
  -v, --version VER  Install a specific version tag, e.g. v0.1.0
  -f, --force        Overwrite an existing captaincode binary
  -y, --yes          Do not prompt for confirmation
  -h, --help         Show this help

Environment:
  CAPTAINCODE_INSTALL_DIR   Same as --dir

Examples:
  curl -fsSL https://raw.githubusercontent.com/john-nash-rs/captaincode/main/install.sh | sh
  curl -fsSL https://raw.githubusercontent.com/john-nash-rs/captaincode/main/install.sh | sh -s -- --dir /usr/local/bin
  curl -fsSL https://raw.githubusercontent.com/john-nash-rs/captaincode/main/install.sh | sh -s -- --version v0.1.0
USAGE
}

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------
while [ $# -gt 0 ]; do
	case "$1" in
		-d|--dir)      [ $# -ge 2 ] || die "--dir requires a value"; INSTALL_DIR="$2"; shift 2 ;;
		-v|--version)  [ $# -ge 2 ] || die "--version requires a value"; VERSION="$2"; shift 2 ;;
		-f|--force)    FORCE=1; shift ;;
		-y|--yes)      ASSUME_YES=1; shift ;;
		-h|--help)     usage; exit 0 ;;
		*)             die "unknown option: $1 (try --help)" ;;
	esac
done

# ---------------------------------------------------------------------------
# Platform detection
# ---------------------------------------------------------------------------
os=$(uname -s 2>/dev/null || echo unknown)
arch=$(uname -m 2>/dev/null || echo unknown)

case "$os" in
	Darwin) os="darwin" ;;
	Linux)  os="linux" ;;
	*) die "unsupported operating system: $os (CaptainCode supports macOS and Linux)" ;;
esac

case "$arch" in
	x86_64|amd64)  arch="amd64" ;;
	arm64|aarch64) arch="arm64" ;;
	*) die "unsupported architecture: $arch (supported: amd64, arm64)" ;;
esac

# The binary is linked without cgo, so it needs no libc compatibility shim on
# Linux. Record the minimum kernel requirement rather than silently producing a
# binary that fails to start on an older host.
if [ "$os" = "linux" ]; then
	case "$(uname -r 2>/dev/null || echo 0)" in
		2.*|3.*|4.*|5.*|6.*) die "Linux kernel 3.10 or newer is required" ;;
	esac
fi

# ---------------------------------------------------------------------------
# Temporary directory with guaranteed cleanup, including on interruption.
# ---------------------------------------------------------------------------
TMPDIR_CLEAN=""
cleanup() {
	[ -n "$TMPDIR_CLEAN" ] && [ -d "$TMPDIR_CLEAN" ] && rm -rf "$TMPDIR_CLEAN"
}
trap cleanup EXIT HUP INT TERM

TMPDIR_CLEAN=$(mktemp -d 2>/dev/null) || die "could not create a temporary directory"
[ -n "$TMPDIR_CLEAN" ] || die "could not create a temporary directory"

# ---------------------------------------------------------------------------
# Network helpers. curl is preferred; wget is supported because some minimal
# images ship only one of them.
# ---------------------------------------------------------------------------
have() { command -v "$1" >/dev/null 2>&1; }

fetch() {
	# fetch <url> <destination>
	_url="$1"; _out="$2"
	if have curl; then
		# --proto and --tlsv1.2 pin the protocol set so a redirect cannot
		# downgrade to plaintext.
		curl --fail --silent --show-error --location \
			--proto '=https' --tlsv1.2 \
			--retry 3 --retry-delay 2 \
			-o "$_out" "$_url" || return 1
	elif have wget; then
		wget --quiet --https-only --tries=3 -O "$_out" "$_url" || return 1
	else
		die "neither curl nor wget is available; install one and retry"
	fi
}

fetch_text() {
	# Same as fetch but to stdout, for small API responses.
	_url="$1"
	if have curl; then
		curl --fail --silent --show-error --location \
			--proto '=https' --tlsv1.2 --retry 3 --retry-delay 2 "$_url"
	elif have wget; then
		wget --quiet --https-only --tries=3 -O - "$_url"
	else
		die "neither curl nor wget is available; install one and retry"
	fi
}

sha256_of() {
	# Prefer the platform toolchain; fall back to openssl.
	if have shasum; then
		shasum -a 256 "$1" | awk '{print $1}'
	elif have sha256sum; then
		sha256sum "$1" | awk '{print $1}'
	elif have openssl; then
		openssl dgst -sha256 "$1" | awk '{print $NF}'
	else
		die "no SHA-256 tool found (need shasum, sha256sum, or openssl)"
	fi
}

# ---------------------------------------------------------------------------
# Resolve the version to install
# ---------------------------------------------------------------------------
if [ -z "$VERSION" ]; then
	info "Looking up the latest release..."
	resolved=$(fetch_text "$API/repos/$REPO/releases/latest" 2>/dev/null \
		| tr ',' '\n' \
		| grep '"tag_name"' \
		| head -1 \
		| sed 's/.*"tag_name"[^"]*"\([^"]*\)".*/\1/') || resolved=""
	[ -n "$resolved" ] || die "could not determine the latest release from the GitHub API.
       Check your network connection, or install a specific version:
       sh install.sh --version v0.1.0"
	VERSION="$resolved"
fi

# Accept both "0.1.0" and "v0.1.0".
case "$VERSION" in
	v*) ;;
	*) VERSION="v$VERSION" ;;
esac

info "captaincode $VERSION  ($os/$arch)"

# ---------------------------------------------------------------------------
# Download the archive and the checksum file
# ---------------------------------------------------------------------------
archive="captaincode_${VERSION#v}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$VERSION"

info "Downloading $archive..."
fetch "$base/$archive" "$TMPDIR_CLEAN/$archive" \
	|| die "download failed: $base/$archive
       The release may not have an artifact for $os/$arch.
       See https://github.com/$REPO/releases"

info "Downloading checksums.txt..."
fetch "$base/checksums.txt" "$TMPDIR_CLEAN/checksums.txt" \
	|| die "download failed: $base/checksums.txt
       Refusing to install without a checksum to verify against."

# ---------------------------------------------------------------------------
# Verify BEFORE extracting or executing anything. A mismatch is fatal and the
# partially downloaded file is discarded with the rest of the temp directory.
# ---------------------------------------------------------------------------
expected=$(awk -v f="$archive" '$2 == f || $2 == "*"f { print $1 }' "$TMPDIR_CLEAN/checksums.txt" | head -1)
[ -n "$expected" ] || die "no checksum listed for $archive in checksums.txt.
       This archive was not published by a verified release; refusing to install it."

actual=$(sha256_of "$TMPDIR_CLEAN/$archive")
[ -n "$actual" ] || die "could not compute a SHA-256 checksum for the download"

if [ "$expected" != "$actual" ]; then
	warn "CHECKSUM MISMATCH"
	warn "  expected: $expected"
	warn "  actual:   $actual"
	die "refusing to install. The download is corrupt or was tampered with."
fi
info "Checksum verified."

# ---------------------------------------------------------------------------
# Extract
# ---------------------------------------------------------------------------
tar -xzf "$TMPDIR_CLEAN/$archive" -C "$TMPDIR_CLEAN" || die "could not extract $archive"
[ -f "$TMPDIR_CLEAN/captaincode" ] || die "the archive did not contain a captaincode binary"
chmod +x "$TMPDIR_CLEAN/captaincode"

# ---------------------------------------------------------------------------
# Install
# ---------------------------------------------------------------------------
if ! mkdir -p "$INSTALL_DIR" 2>/dev/null; then
	die "cannot create $INSTALL_DIR
       Choose another location:  sh install.sh --dir ~/bin"
fi
[ -w "$INSTALL_DIR" ] || die "$INSTALL_DIR is not writable
       Choose another location:  sh install.sh --dir ~/bin"

target="$INSTALL_DIR/captaincode"
replacing=0

if [ -e "$target" ] && [ "$FORCE" -ne 1 ]; then
	if [ "$ASSUME_YES" -ne 1 ]; then
		if [ -t 0 ]; then
			printf 'Overwrite existing %s? [y/N] ' "$target"
			read -r reply
			case "$reply" in
				y|Y|yes|YES) replacing=1 ;;
				*) info "Cancelled. Nothing was changed."; exit 0 ;;
			esac
		else
			die "$target already exists.
       Re-run with --force to replace it, or --dir to install elsewhere."
		fi
	else
		replacing=1
	fi
fi

[ -e "$target" ] && [ "$replacing" -eq 1 ] && warn "Replacing existing $target"
[ -e "$target" ] && [ "$FORCE" -eq 1 ] && warn "Replacing existing $target (--force)"

# Install to a temporary name and rename, so an existing binary is never left
# half-written if the copy is interrupted.
cp "$TMPDIR_CLEAN/captaincode" "$INSTALL_DIR/.captaincode.tmp.$$" \
	|| die "could not write to $INSTALL_DIR"
chmod 755 "$INSTALL_DIR/.captaincode.tmp.$$" || die "could not set permissions"
mv -f "$INSTALL_DIR/.captaincode.tmp.$$" "$target" \
	|| { rm -f "$INSTALL_DIR/.captaincode.tmp.$$"; die "could not install $target"; }

info ""
info "Installed $target"
"$target" version 2>/dev/null | head -1 || true

# ---------------------------------------------------------------------------
# PATH guidance. Only mention it when the directory is genuinely not on PATH.
# ---------------------------------------------------------------------------
case ":${PATH:-}:" in
	*":$INSTALL_DIR:"*) ;;
	*)
		info ""
		info "$INSTALL_DIR is not on your PATH. Add it with:"
		info ""
		# The variable is single-quoted on purpose. Double quotes would expand
		# $HOME and $PATH now, baking this shell's values into the file, and a
		# nested shell -c with its own quoting cannot be pasted reliably.
		case "${SHELL:-}" in
			*/zsh)
				info "    echo 'export PATH=\"\$HOME/.local/bin:\$PATH\"' >> ~/.zshrc" ;;
			*/bash)
				info "    echo 'export PATH=\"\$HOME/.local/bin:\$PATH\"' >> ~/.bashrc" ;;
			*)
				info "    echo 'export PATH=\"\$HOME/.local/bin:\$PATH\"' >> ~/.profile" ;;
		esac
		info ""
		info "Then open a new shell, or run:"
		info ""
		info "    export PATH=\"\$INSTALL_DIR:\$PATH\""
		;;
esac

info ""
info "Next steps:"
info "  captaincode doctor                          check sandbox prerequisites"
info "  captaincode ./myproject                      open the terminal interface"
info "  captaincode ./myproject --ide=false          one-shot answer"
info ""
info "Note: installing the binary does not install the sandbox dependency."
info "macOS uses the built-in /usr/bin/sandbox-exec. Linux requires bubblewrap:"
info "  sudo apt-get install bubblewrap"
info "Run 'captaincode doctor' to confirm both."
info ""
info "Upgrade: re-run this installer. Uninstall: rm $target"