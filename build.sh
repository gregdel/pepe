#!/usr/bin/env sh
set -eu

ROOT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
FONT_DIR="$ROOT_DIR/internal/app/embed"
FONT_REVISION="ec626514f79f831f1ab848a82114a0ce7e2d6372"
FONT_BASE_URL="https://raw.githubusercontent.com/google/fonts/$FONT_REVISION/ofl/roboto"
FONT_REGULAR_PATH="$FONT_DIR/roboto.ttf"
FONT_REGULAR_URL="$FONT_BASE_URL/Roboto%5Bwdth%2Cwght%5D.ttf"
FONT_REGULAR_SHA256="d7598e12c5dbef095ff8272cfc55da0250bd07fbdecbac8a530b9b277872a134"
FONT_ITALIC_PATH="$FONT_DIR/roboto-italic.ttf"
FONT_ITALIC_URL="$FONT_BASE_URL/Roboto-Italic%5Bwdth%2Cwght%5D.ttf"
FONT_ITALIC_SHA256="9725a847af6b460ffca162ae66d20dad48b01876137947180b42d7dcd7887182"

require_command() {
	if ! command -v "$1" >/dev/null 2>&1; then
		printf 'Required command not found: %s\n' "$1" >&2
		exit 1
	fi
}

# sha256sum is not guaranteed on macOS; shasum ships with it.
if command -v sha256sum >/dev/null 2>&1; then
	digest() { sha256sum; }
elif command -v shasum >/dev/null 2>&1; then
	digest() { shasum -a 256; }
else
	printf 'Required command not found: sha256sum or shasum\n' >&2
	exit 1
fi

sha256() {
	hash=$(digest < "$1") || return
	printf '%s\n' "${hash%% *}"
}

mkdir -p "$FONT_DIR"
rm -f "$FONT_DIR/font.ttf" # Remove the legacy generated Fira Code asset.

font_tmp="${TMPDIR:-/tmp}/pepe-font.$$.ttf"
trap 'rm -f "$font_tmp"' EXIT HUP INT TERM

fetch_font() {
	path=$1
	url=$2
	want=$3
	label=$4

	if [ -f "$path" ] && [ "$(sha256 "$path")" = "$want" ]; then
		return
	fi

	require_command curl
	printf 'Downloading %s...\n' "$label" >&2
	curl -fL --retry 3 --silent --show-error "$url" -o "$font_tmp"
	if [ "$(sha256 "$font_tmp")" != "$want" ]; then
		printf '%s checksum mismatch\n' "$label" >&2
		exit 1
	fi
	mv "$font_tmp" "$path"
}

fetch_font "$FONT_REGULAR_PATH" "$FONT_REGULAR_URL" "$FONT_REGULAR_SHA256" "Roboto variable font"
fetch_font "$FONT_ITALIC_PATH" "$FONT_ITALIC_URL" "$FONT_ITALIC_SHA256" "Roboto Italic variable font"

"$ROOT_DIR/scripts/fetch-assets.sh" "$ROOT_DIR/packaging/embed"

cd "$ROOT_DIR"
go build "$@"
