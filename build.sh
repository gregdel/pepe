#!/usr/bin/env sh
set -eu

ROOT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
FONT_DIR="$ROOT_DIR/internal/app/embed"
FONT_PATH="$FONT_DIR/font.ttf"
FONT_MEMBER="FiraCodeNerdFont-Regular.ttf"
FONT_URL="https://github.com/ryanoasis/nerd-fonts/releases/download/v3.5.0/FiraCode.tar.xz"
ARCHIVE_SHA256="32226dc81bb30ea421cdc49ba2134b93c2b43096992c829504a3004d2537420d"
FONT_SHA256="06b7d71853384f7fd15fbc8ca13c8a5c6af713954e946e27e3ee560a7b5f0337"

require_command() {
	if ! command -v "$1" >/dev/null 2>&1; then
		printf 'Required command not found: %s\n' "$1" >&2
		exit 1
	fi
}

sha256() {
	hash=$(sha256sum "$1") || return
	printf '%s\n' "${hash%% *}"
}

require_command sha256sum

mkdir -p "$FONT_DIR"

font_ready=false
if [ -f "$FONT_PATH" ] && [ "$(sha256 "$FONT_PATH")" = "$FONT_SHA256" ]; then
	font_ready=true
fi

archive_tmp="${TMPDIR:-/tmp}/polochon-gio-firacode.$$.tar.xz"
font_tmp="${TMPDIR:-/tmp}/polochon-gio-font.$$.ttf"
trap 'rm -f "$archive_tmp" "$font_tmp"' EXIT HUP INT TERM

if [ "$font_ready" = false ]; then
	require_command curl
	require_command tar

	printf 'Downloading FiraCode Nerd Font v3.5.0...\n' >&2
	curl -fL --retry 3 --silent --show-error "$FONT_URL" -o "$archive_tmp"
	if [ "$(sha256 "$archive_tmp")" != "$ARCHIVE_SHA256" ]; then
		printf 'Font archive checksum mismatch\n' >&2
		exit 1
	fi
	if ! tar -xJOf "$archive_tmp" "$FONT_MEMBER" > "$font_tmp"; then
		printf 'Font member not found in archive: %s\n' "$FONT_MEMBER" >&2
		exit 1
	fi
	if [ "$(sha256 "$font_tmp")" != "$FONT_SHA256" ]; then
		printf 'Extracted font checksum mismatch\n' >&2
		exit 1
	fi
	mv "$font_tmp" "$FONT_PATH"
fi

cd "$ROOT_DIR"
go build "$@"
