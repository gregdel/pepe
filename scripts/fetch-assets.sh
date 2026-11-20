#!/usr/bin/env sh
# Fetch SHA-pinned build-time inputs (artwork by default, or another manifest such as
# packaging/fonts.txt); never used during installation or launch.
set -eu
ROOT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
	printf 'Usage: %s DESTINATION [MANIFEST]\n' "$0" >&2
	exit 2
fi
# sha256sum is not guaranteed on macOS; shasum ships with it.
if command -v sha256sum >/dev/null 2>&1; then
	digest() { sha256sum; }
elif command -v shasum >/dev/null 2>&1; then
	digest() { shasum -a 256; }
else
	printf 'Required command not found: sha256sum or shasum\n' >&2
	exit 1
fi
MANIFEST=${2:-$ROOT_DIR/packaging/assets.txt}
[ -f "$MANIFEST" ] || { printf 'Manifest not found: %s\n' "$MANIFEST" >&2; exit 1; }
mkdir -p -- "$1"
DEST=$(CDPATH='' cd -- "$1" && pwd)
TMP=''
trap '[ -z "$TMP" ] || rm -f -- "$TMP"' EXIT
trap 'exit 1' HUP INT TERM

while read -r want name url; do
	case "$want" in ''|'#'*) continue ;; esac
	case "$name" in ''|.*|*[!a-zA-Z0-9_.-]*) printf 'Invalid asset filename: %s\n' "$name" >&2; exit 1 ;; esac
	case "$url" in https://*) ;; *) printf 'Asset URL must use HTTPS\n' >&2; exit 1 ;; esac
	file="$DEST/$name"
	[ ! -L "$file" ] || { printf 'Refusing asset symlink: %s\n' "$file" >&2; exit 1; }
	if [ -f "$file" ]; then
		hash=$(digest < "$file")
		[ "${hash%% *}" != "$want" ] || continue
	fi
	command -v curl >/dev/null 2>&1 || { printf 'Required command not found: curl\n' >&2; exit 1; }
	TMP=$(mktemp "$DEST/.asset.XXXXXX")
	printf 'Downloading %s...\n' "$name" >&2
	curl --fail --retry 3 --silent --show-error --location --proto '=https' --proto-redir '=https' \
		--connect-timeout 5 --max-time 30 --max-filesize 8388608 --output "$TMP" "$url"
	hash=$(digest < "$TMP")
	[ "${hash%% *}" = "$want" ] || { printf 'Asset checksum mismatch: %s\n' "$name" >&2; exit 1; }
	chmod 644 "$TMP"
	mv -- "$TMP" "$file"
	TMP=''
done < "$MANIFEST"
