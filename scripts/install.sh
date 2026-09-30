#!/bin/sh
# Install the Omniplex server from a GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/asiraky/omniplex/main/scripts/install.sh | sh
#
# Environment:
#   OMNIPLEX_VERSION        version to install, e.g. 0.2.0 (default: the latest release)
#   OMNIPLEX_INSTALL_DIR    where the binary goes (default: ~/.local/bin)
#   OMNIPLEX_DOWNLOAD_BASE  URL of a directory holding the release assets. Overrides
#                           OMNIPLEX_VERSION; for mirrors and for testing this script.
#
# This is the terminal path. People who do not live in a terminal should use
# the desktop app: https://github.com/asiraky/omniplex/releases/latest
#
# Everything runs inside main(), so a download cut off halfway through
# executes nothing.

set -eu

REPO=asiraky/omniplex

say() { printf '%s\n' "$*"; }
err() { printf 'omniplex install: %s\n' "$*" >&2; }
die() {
	err "$*"
	exit 1
}

# fetch URL DEST
fetch() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --retry 3 -o "$2" "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$2" "$1"
	else
		die "need curl or wget"
	fi
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d ' ' -f 1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d ' ' -f 1
	else
		die "need sha256sum or shasum to verify the download"
	fi
}

detect_os() {
	case $(uname -s) in
	Darwin) echo darwin ;;
	Linux) echo linux ;;
	MINGW* | MSYS* | CYGWIN* | Windows_NT)
		err "Windows is not supported by this script."
		err "Install the desktop app instead: https://github.com/$REPO/releases/latest"
		exit 1
		;;
	*) die "unsupported operating system: $(uname -s)" ;;
	esac
}

detect_arch() {
	arch=$(uname -m)
	# Rosetta reports x86_64 for a shell running translated on Apple Silicon.
	# The native build is the right one there.
	if [ "$1" = darwin ] && [ "$arch" = x86_64 ] &&
		[ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = 1 ]; then
		arch=arm64
	fi
	case $arch in
	x86_64 | amd64) echo amd64 ;;
	arm64 | aarch64) echo arm64 ;;
	*) die "unsupported architecture: $arch" ;;
	esac
}

main() {
	os=$(detect_os)
	arch=$(detect_arch "$os")
	install_dir=${OMNIPLEX_INSTALL_DIR:-$HOME/.local/bin}

	if [ -n "${OMNIPLEX_DOWNLOAD_BASE:-}" ]; then
		base=${OMNIPLEX_DOWNLOAD_BASE%/}
	elif [ -n "${OMNIPLEX_VERSION:-}" ]; then
		base="https://github.com/$REPO/releases/download/v${OMNIPLEX_VERSION#v}"
	else
		# GitHub redirects this to the latest release's assets, which saves an
		# API call and its rate limit.
		base="https://github.com/$REPO/releases/latest/download"
	fi

	tmp=$(mktemp -d 2>/dev/null || mktemp -d -t omniplex)
	trap 'rm -rf "$tmp"' EXIT
	trap 'exit 1' INT TERM

	# SHA256SUMS names every server tarball, version included, so it tells us
	# both what to download and what it must hash to.
	fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" ||
		die "could not download $base/SHA256SUMS"
	line=$(grep -E "[ *]omniplex-[^ ]+-$os-$arch\.tar\.gz\$" "$tmp/SHA256SUMS" | head -n 1 || true)
	[ -n "$line" ] || die "the release has no build for $os/$arch"
	want=${line%% *}
	file=${line##* }
	file=${file#\*}
	version=${file#omniplex-}
	version=${version%-"$os"-"$arch".tar.gz}

	say "Downloading Omniplex $version for $os/$arch"
	fetch "$base/$file" "$tmp/$file" || die "could not download $base/$file"

	got=$(sha256 "$tmp/$file")
	[ "$got" = "$want" ] || die "checksum mismatch for $file (expected $want, got $got)"

	tar -xzf "$tmp/$file" -C "$tmp"
	bin="$tmp/${file%.tar.gz}/omniplex"
	[ -f "$bin" ] || die "$file does not contain omniplex"

	mkdir -p "$install_dir"
	# Copy beside the destination and rename over it, so a running copy is
	# replaced atomically rather than overwritten in place.
	cp "$bin" "$install_dir/.omniplex.new"
	chmod 0755 "$install_dir/.omniplex.new"
	mv -f "$install_dir/.omniplex.new" "$install_dir/omniplex"

	# `omni` is the short name every build installs. Leave anything else
	# already called omni alone.
	if [ ! -e "$install_dir/omni" ] || [ -L "$install_dir/omni" ]; then
		ln -sf omniplex "$install_dir/omni"
	fi

	say "Installed $install_dir/omniplex"
	say ""
	case ":$PATH:" in
	*":$install_dir:"*) ;;
	*)
		say "$install_dir is not on your PATH. Add it to your shell profile:"
		say ""
		say "  export PATH=\"$install_dir:\$PATH\""
		say ""
		;;
	esac
	say "Start it with:"
	say ""
	say "  omniplex"
	say ""
	say "It prints the URLs it is listening on and a pairing code for other devices."
	say "Omniplex drives your own Claude Code and Codex installs; install and log in"
	say "to at least one of them."
}

main "$@"
