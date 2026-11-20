#!/usr/bin/env sh
# Build dist/pepe-VERSION-macos-ARCH.dmg, an ad-hoc signed pepe.app for the host
# architecture, and print its path (the only stdout output). Extra arguments
# are passed to go build.
set -eu
ROOT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
if [ "$#" -lt 1 ]; then
	printf 'Usage: %s VERSION [GO BUILD FLAGS...]\n' "$0" >&2
	exit 2
fi
VERSION=$1
shift
case "$VERSION" in ''|*[!a-zA-Z0-9_.-]*) printf 'Invalid version: %s\n' "$VERSION" >&2; exit 1 ;; esac
[ "$(go env GOOS)" = darwin ] || { printf 'macOS packages must be built on macOS\n' >&2; exit 1; }
for tool in codesign hdiutil iconutil plutil sips; do
	command -v "$tool" >/dev/null 2>&1 || { printf 'Required command not found: %s\n' "$tool" >&2; exit 1; }
done
# CFBundleVersion must be dot-separated integers: 20261120-100227 -> 20261120.100227.
case "$VERSION" in
	*[!0-9-]*|-*|*-) BUNDLE_VERSION=0.0.0 ;;
	*) BUNDLE_VERSION=$(printf '%s' "$VERSION" | sed 's/-/./g') ;;
esac
NAME="pepe-$VERSION-macos-$(go env GOARCH)"
DIST="$ROOT_DIR/dist"
mkdir -p -- "$DIST"
WORK=$(mktemp -d "$DIST/.macos.XXXXXX")
trap 'rm -rf -- "$WORK"' EXIT
trap 'exit 1' HUP INT TERM
APP="$WORK/dmg/pepe.app"
mkdir -p -- "$APP/Contents/MacOS" "$APP/Contents/Resources"

# Go 1.27 requires macOS 13; build Gio's Objective-C code for the same minimum.
MACOSX_DEPLOYMENT_TARGET=13.0 "$ROOT_DIR/build.sh" -trimpath -o "$APP/Contents/MacOS/pepe" "$@" .
sed "s/@BUNDLE_VERSION@/$BUNDLE_VERSION/g" "$ROOT_DIR/packaging/macos/Info.plist.in" > "$APP/Contents/Info.plist"
plutil -lint "$APP/Contents/Info.plist" >&2

ICONSET="$WORK/pepe.iconset"
ICON="$ROOT_DIR/packaging/embed/application_icon_1024.png"
mkdir -- "$ICONSET"
for size in 16 32 128 256 512; do
	sips -z "$size" "$size" "$ICON" --out "$ICONSET/icon_${size}x${size}.png" >/dev/null
	sips -z "$((size * 2))" "$((size * 2))" "$ICON" --out "$ICONSET/icon_${size}x${size}@2x.png" >/dev/null
done
iconutil -c icns -o "$APP/Contents/Resources/pepe.icns" "$ICONSET"

cp -- "$ROOT_DIR/LICENSE" "$APP/Contents/Resources/LICENSE"
"$ROOT_DIR/scripts/third-party-notices.sh" > "$APP/Contents/Resources/THIRD_PARTY_NOTICES"

# Ad-hoc signature: required to run on Apple Silicon; not a Developer ID.
codesign --force --sign - --timestamp=none "$APP"
codesign --verify --strict --verbose=2 "$APP"

ln -s /Applications "$WORK/dmg/Applications"
rm -f -- "$DIST/$NAME.dmg"
# hdiutil occasionally fails with "Resource busy" on CI runners; retry.
attempt=1
until hdiutil create -volname 'pépé' -srcfolder "$WORK/dmg" -fs HFS+ -format UDZO "$DIST/$NAME.dmg" >&2; do
	[ "$attempt" -lt 3 ] || exit 1
	attempt=$((attempt + 1))
	rm -f -- "$DIST/$NAME.dmg"
	sleep 5
done
printf '%s\n' "$DIST/$NAME.dmg"
