#!/usr/bin/env sh
set -eu

ROOT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
ICON_URL='https://git.quimbo.fr/attachments/ab9e56b5-bf62-4723-8f30-1438ab62a282'
ICON_SHA256='0a7ccf087cf627109d7756378ec05f9a86ffc1854a81ecb6855c4ba1723c374e'

usage() {
	printf 'Usage: %s [--uninstall]\nRegister the existing checkout binary in your desktop menu; no sudo or binary copying.\n' "$0"
}

case "${1:-}" in
	'') [ "$#" -eq 0 ] || { usage >&2; exit 2; }; UNINSTALL=false ;;
	--uninstall) [ "$#" -eq 1 ] || { usage >&2; exit 2; }; UNINSTALL=true ;;
	-h|--help) usage; exit 0 ;;
	*) usage >&2; exit 2 ;;
esac

warn() {
	printf '%s\n' "$*" >&2
}

fail() {
	warn "$*"
	exit 1
}

case "${XDG_DATA_HOME:-}" in
	/*) DATA_DIR=$XDG_DATA_HOME ;;
	*) DATA_DIR="${HOME:?HOME must be set}/.local/share" ;;
esac
case "$DATA_DIR" in
	/*) ;;
	*) fail 'The desktop data directory must be an absolute path.' ;;
esac
# Control characters cannot safely be represented in these desktop paths.
case "$ROOT_DIR$DATA_DIR" in
	*'
'*) fail 'Checkout and desktop data paths must not contain control characters.' ;;
esac
if printf '%s\n' "$ROOT_DIR" "$DATA_DIR" | LC_ALL=C grep -q '[[:cntrl:]]'; then
	fail 'Checkout and desktop data paths must not contain control characters.'
fi

APPLICATIONS_DIR="$DATA_DIR/applications"
DESKTOP_FILE="$APPLICATIONS_DIR/pepe.desktop"
ICON_DIR="$DATA_DIR/pepe"
ICON_FILE="$ICON_DIR/icon.png"
BINARY="$ROOT_DIR/pepe"

# Do not replace or remove a launcher created outside this script.
if [ -e "$DESKTOP_FILE" ] || [ -L "$DESKTOP_FILE" ]; then
	if [ -L "$DESKTOP_FILE" ] || [ ! -f "$DESKTOP_FILE" ] ||
		! grep -qx 'X-Pepe-Managed=true' "$DESKTOP_FILE" ||
		grep -qx 'X-Pepe-Bundle=true' "$DESKTOP_FILE"; then
		fail "Refusing to overwrite or remove an unrelated launcher: $DESKTOP_FILE"
	fi
fi

verified_icon() {
	command -v sha256sum >/dev/null 2>&1 || return 1
	[ -f "$1" ] && [ ! -L "$1" ] || return 1
	hash=$(sha256sum < "$1") || return 1
	[ "${hash%% *}" = "$ICON_SHA256" ]
}

refresh_desktop() {
	if [ -d "$APPLICATIONS_DIR" ] && command -v update-desktop-database >/dev/null 2>&1; then
		update-desktop-database "$APPLICATIONS_DIR" || warn 'Could not refresh the desktop database; registration files are already updated.'
	fi
}

if "$UNINSTALL"; then
	rm -f -- "$DESKTOP_FILE"
	if verified_icon "$ICON_FILE"; then
		rm -- "$ICON_FILE"
	elif [ -e "$ICON_FILE" ] || [ -L "$ICON_FILE" ]; then
		warn "Leaving unverified icon untouched: $ICON_FILE"
	fi
	refresh_desktop
	printf 'Removed desktop registration; binary and configuration are untouched.\n'
	exit 0
fi

[ -f "$BINARY" ] && [ -x "$BINARY" ] || fail "Build the checkout binary first: cd \"$ROOT_DIR\" && ./build.sh"
case "$BINARY" in
	*=*|*%*) fail "Desktop launchers do not support checkout paths containing '=' or '%'; move the checkout and register again." ;;
esac
[ -f "$ROOT_DIR/packaging/linux/pepe.desktop.in" ] || fail 'Missing desktop entry template.'

DESKTOP_TMP=''
ICON_TMP=''
cleanup() {
	[ -z "$DESKTOP_TMP" ] || rm -f -- "$DESKTOP_TMP"
	[ -z "$ICON_TMP" ] || rm -f -- "$ICON_TMP"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

ICON_VALUE=video-x-generic
fetch_icon() {
	if verified_icon "$ICON_FILE"; then
		ICON_VALUE=$ICON_FILE
		return
	fi
	if [ -e "$ICON_FILE" ] || [ -L "$ICON_FILE" ]; then
		warn "Leaving unverified icon untouched; remove it and re-register to download a fresh copy: $ICON_FILE"
		return
	fi
	if ! command -v curl >/dev/null 2>&1 || ! command -v sha256sum >/dev/null 2>&1; then
		warn 'Icon download needs curl and sha256sum; using a generic desktop icon.'
		return
	fi
	mkdir -p "$ICON_DIR"
	ICON_TMP=$(mktemp "$ICON_DIR/.icon.XXXXXX")
	printf 'Downloading Pépé icon...\n'
	if ! curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
		--connect-timeout 5 --max-time 20 --max-filesize 2097152 \
		--output "$ICON_TMP" "$ICON_URL"; then
		warn 'Icon download failed; using a generic desktop icon. Re-register to retry.'
		return
	fi
	if ! verified_icon "$ICON_TMP"; then
		warn 'Icon checksum mismatch; using a generic desktop icon. The downloaded file was not installed.'
		return
	fi
	chmod 644 "$ICON_TMP"
	mv -- "$ICON_TMP" "$ICON_FILE"
	ICON_TMP=''
	ICON_VALUE=$ICON_FILE
}

# Desktop strings and Exec arguments have separate escaping layers. Exec is
# not a shell command: quote the entire path, then apply desktop-string escapes.
escape_string() {
	printf '%s' "$1" | sed 's/\\/\\\\/g'
}
escape_exec() {
	# shellcheck disable=SC2016 # Escape literal dollars; this is not shell expansion.
	printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g; s/`/\\`/g; s/\$/\\$/g' |
		sed 's/\\/\\\\/g'
}

mkdir -p "$APPLICATIONS_DIR"
DESKTOP_TMP=$(mktemp "$APPLICATIONS_DIR/.pepe.XXXXXX")
cp "$ROOT_DIR/packaging/linux/pepe.desktop.in" "$DESKTOP_TMP"
fetch_icon
{
	printf 'Exec="%s"\n' "$(escape_exec "$BINARY")"
	printf 'TryExec=%s\n' "$(escape_string "$BINARY")"
	printf 'Icon=%s\n' "$(escape_string "$ICON_VALUE")"
} >> "$DESKTOP_TMP"
chmod 644 "$DESKTOP_TMP"
mv -- "$DESKTOP_TMP" "$DESKTOP_FILE"
DESKTOP_TMP=''
refresh_desktop
printf 'Registered %s\nLauncher: %s\n' "$BINARY" "$DESKTOP_FILE"
