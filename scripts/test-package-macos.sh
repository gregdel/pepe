#!/usr/bin/env sh
# Smoke-test a DMG from package-macos.sh without opening a window.
set -eu
if [ "$#" -ne 1 ]; then
	printf 'Usage: %s DMG\n' "$0" >&2
	exit 2
fi
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
MOUNT=$(mktemp -d "${TMPDIR:-/tmp}/pepe-dmg.XXXXXX")
trap 'hdiutil detach -quiet "$MOUNT" 2>/dev/null || true; rmdir "$MOUNT" 2>/dev/null || true' EXIT
trap 'exit 1' HUP INT TERM
hdiutil attach -quiet -nobrowse -readonly -mountpoint "$MOUNT" "$1"

APP="$MOUNT/pepe.app"
BIN="$APP/Contents/MacOS/pepe"
[ -L "$MOUNT/Applications" ] || fail 'Missing Applications shortcut'
for file in Info.plist Resources/pepe.icns Resources/LICENSE Resources/THIRD_PARTY_NOTICES; do
	[ -s "$APP/Contents/$file" ] || fail "Missing $file"
done
plutil -lint "$APP/Contents/Info.plist"
[ "$(plutil -extract CFBundleExecutable raw "$APP/Contents/Info.plist")" = pepe ] || fail 'Wrong CFBundleExecutable'
[ "$(plutil -extract LSMinimumSystemVersion raw "$APP/Contents/Info.plist")" = 13.0 ] || fail 'Wrong LSMinimumSystemVersion'
codesign --verify --strict --verbose=2 "$APP"

want_arch=$(go env GOARCH)
[ "$want_arch" != amd64 ] || want_arch=x86_64
[ "$(lipo -archs "$BIN")" = "$want_arch" ] || fail "Binary architecture is $(lipo -archs "$BIN"), want $want_arch"
# Go and the cgo objects must both target macOS 13 (LC_BUILD_VERSION minos).
minos=$(otool -l "$BIN" | awk '/LC_BUILD_VERSION/ { found = 1 } found && $1 == "minos" { print $2; exit }')
[ "$minos" = 13.0 ] || fail "Binary minimum macOS is $minos, want 13.0"
"$BIN" -h > /dev/null 2>&1 || fail 'pepe -h failed'
printf 'macOS package tests passed: %s\n' "$1"
