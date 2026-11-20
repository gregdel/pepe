#!/usr/bin/env sh
# Build the release tarball dist/pepe-VERSION-linux-ARCH.tar.gz for the host
# architecture. Extra arguments are passed to go build.
set -eu
ROOT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
if [ "$#" -lt 1 ]; then
	printf 'Usage: %s VERSION [GO BUILD FLAGS...]\n' "$0" >&2
	exit 2
fi
VERSION=$1
shift
case "$VERSION" in ''|*[!a-zA-Z0-9_.-]*) printf 'Invalid version: %s\n' "$VERSION" >&2; exit 1 ;; esac
[ "$(go env GOOS)" = linux ] || { printf 'Linux packages must be built on Linux\n' >&2; exit 1; }
NAME="pepe-$VERSION-linux-$(go env GOARCH)"
DIST="$ROOT_DIR/dist"
STAGE="$DIST/$NAME"

rm -rf -- "$STAGE" "$STAGE.tar.gz"
mkdir -p -- "$STAGE"
"$ROOT_DIR/build.sh" -trimpath -o "$STAGE/pepe" "$@" .
"$ROOT_DIR/scripts/third-party-notices.sh" > "$STAGE/THIRD_PARTY_NOTICES"
cp -- "$ROOT_DIR/LICENSE" "$STAGE/LICENSE"
cat > "$STAGE/INSTALL" <<END
pépé $VERSION for Linux ($(go env GOARCH))

Requirements: mpv 0.41+ with Lua, and the Wayland/X11 desktop libraries Gio
links against (present on desktop systems and SteamOS).

Install or upgrade (existing configuration is kept):

    ./pepe install

Then set polochon_url and polochon_token in ~/.config/pepe/config.yaml.
Remove with: ~/.local/bin/pepe uninstall

More: https://github.com/gregdel/pepe/blob/master/packaging/linux/README.md
END
tar -czf "$STAGE.tar.gz" -C "$DIST" "$NAME"
rm -rf -- "$STAGE"
printf '%s\n' "$STAGE.tar.gz"
