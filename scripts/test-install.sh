#!/usr/bin/env sh
# Offline standalone-binary integration; no real desktop, server or player used.
# Usage: test-install.sh [BINARY] (default: build from the checkout).
set -eu
ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
for tool in go desktop-file-validate gio sha256sum; do
	command -v "$tool" >/dev/null 2>&1 || { printf 'Required test command not found: %s\n' "$tool" >&2; exit 1; }
done
mkdir -p "$ROOT/.pi/tmp"
TEST=$(mktemp -d "$ROOT/.pi/tmp/install-test.XXXXXX")
trap 'rm -rf -- "$TEST"' EXIT
trap 'exit 1' HUP INT TERM
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
assert_line() { grep -Fx -- "$2" "$1" >/dev/null || fail "Missing line '$2' in $1"; }
expect_failure() { if "$@" > "$TEST/output" 2>&1; then fail "Unexpected success: $*"; fi; }
mkdir "$TEST/download" "$TEST/bin"
if [ "$#" -gt 0 ]; then
	cp -- "$1" "$TEST/download/pepe"
else
	(cd "$ROOT" && TMPDIR="$TEST" go build -o "$TEST/download/pepe" .)
fi
# Never refresh the real desktop database, open a window or contact Polochon.
cat > "$TEST/bin/update-desktop-database" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod +x "$TEST/bin/update-desktop-database"
export PATH="$TEST/bin:$PATH"
export HOME="$TEST/home spaces ' \" \` \$ \\ é"
export XDG_DATA_HOME="$TEST/data spaces \$ % \\ é"
export XDG_CONFIG_HOME="$TEST/config spaces ' é"
export DISPLAY='' WAYLAND_DISPLAY='' POLOCHON_URL='' POLOCHON_TOKEN='' POLOCHON_MPV=''
export TEST_LAUNCH_OUT="$TEST/launched"
APP="$XDG_DATA_HOME/pepe"
BIN="$HOME/.local/bin/pepe"
DESKTOP="$XDG_DATA_HOME/applications/pepe.desktop"
CONFIG="$XDG_CONFIG_HOME/pepe/config.yaml"
DOWNLOAD="$TEST/download/pepe"
"$DOWNLOAD" install -h > "$TEST/output" 2>&1
"$DOWNLOAD" uninstall -h > "$TEST/output" 2>&1
expect_failure "$DOWNLOAD" install --unknown
expect_failure "$DOWNLOAD" install extra
expect_failure "$DOWNLOAD" unknown-command
[ ! -e "$APP" ] || fail 'Help/invalid arguments created an installation'
# A legacy checkout registration must be explicitly removed before installing.
mkdir -p "$(dirname "$DESKTOP")"
printf '[Desktop Entry]\nName=pépé\nX-Pepe-Managed=true\nExec=/old/checkout/pepe\n' > "$DESKTOP"
cp "$DESKTOP" "$TEST/checkout.desktop"
expect_failure "$DOWNLOAD" install
grep -F 'unregister the checkout first' "$TEST/output" >/dev/null || fail 'Missing checkout unregistration guidance'
cmp "$TEST/checkout.desktop" "$DESKTOP" || fail 'Install changed the checkout entry'
[ ! -e "$APP" ] && [ ! -e "$BIN" ] || fail 'Refused install created payload or launcher'
rm "$DESKTOP" # Simulate manual unregistration using the old checkout script.
"$DOWNLOAD" install > "$TEST/output"
[ -d "$APP" ] && [ ! -L "$APP" ] || fail 'Installation is not a real directory'
for path in app current releases; do [ ! -e "$APP/$path" ] || fail "Installation created $path nesting"; done
[ "$(stat -c %a "$CONFIG")" = 600 ] || fail 'Configuration is not private'
player=$(printf '%s' "$APP/mpv-auto.sh" | sed "s/'/''/g")
assert_line "$CONFIG" "mpv_path: '$player'"
assert_line "$DESKTOP" 'Name=pépé'
assert_line "$DESKTOP" 'X-Pepe-Bundle=true'
desktop-file-validate "$DESKTOP"
# All assets/wrappers must be independent copies, including after download removal.
rm -rf "$TEST/download"
for script in mpv-auto.sh mpv-inhibit.sh mpv-keepawake.sh; do
	[ -x "$APP/$script" ] && [ ! -L "$APP/$script" ] || fail "Missing copied wrapper: $script"
	cmp "$ROOT/packaging/linux/$script" "$APP/$script"
done
while read -r digest name _url; do
	case "$digest" in ''|'#'*) continue ;; esac
	[ -f "$APP/assets/$name" ] && [ ! -L "$APP/assets/$name" ] || fail "Missing copied artwork: $name"
done < "$ROOT/packaging/assets.txt"
(cd "$APP" && sha256sum --check --strict SHA256SUMS)
[ -z "$(find "$APP/" -type l -print)" ] || fail 'Installed payload contains symlinks'
# Use a temporary fake app to exercise the real desktop Exec parser with quoted
# paths. Restore the exact binary before reinstall/uninstall; never open the UI.
mv "$APP/pepe" "$TEST/original"
cat > "$APP/pepe" <<'EOF'
#!/bin/sh
printf '%s\n' "$0" > "$TEST_LAUNCH_OUT"
EOF
chmod 755 "$APP/pepe"
gio launch "$DESKTOP"
attempt=0
while [ ! -f "$TEST_LAUNCH_OUT" ] && [ "$attempt" -lt 100 ]; do sleep 0.05; attempt=$((attempt + 1)); done
[ -f "$TEST_LAUNCH_OUT" ] || fail 'Desktop entry did not launch'
assert_line "$TEST_LAUNCH_OUT" "$BIN"
mv "$TEST/original" "$APP/pepe"
# Reinstall from the installed executable; preserve existing custom configuration.
printf 'private existing configuration\nmpv_path: /my/custom/player\n' > "$CONFIG"
chmod 640 "$CONFIG"
"$BIN" install > "$TEST/output"
assert_line "$TEST/output" "Configuration already exists; keeping $CONFIG"
for path in app current releases; do [ ! -e "$APP/$path" ] || fail "Reinstall created $path nesting"; done
assert_line "$CONFIG" 'mpv_path: /my/custom/player'
[ "$(stat -c %a "$CONFIG")" = 640 ] || fail 'Existing config permissions changed'
"$BIN" uninstall > "$TEST/output"
[ ! -e "$APP" ] && [ ! -e "$DESKTOP" ] && [ ! -L "$BIN" ] || fail 'Clean uninstall left installed files'
assert_line "$CONFIG" 'mpv_path: /my/custom/player'

# Helper tests use only fake Flatpak/mpv. Never execute the keepawake worker.
HELPER="$ROOT/packaging/linux/mpv-auto.sh"
mkdir "$TEST/player-bin"
ln -s "$(command -v sh)" "$TEST/player-bin/sh"
cat > "$TEST/player-bin/flatpak" <<'EOF'
#!/usr/bin/env sh
if [ "$1" = info ]; then [ "$2" = "$TEST_FLATPAK_INSTALL" ]; exit; fi
printf '%s\n' "$@" > "$TEST_LAUNCH_OUT"
exit 0
EOF
chmod +x "$TEST/player-bin/flatpak"
export TEST_FLATPAK_INSTALL=--user
PATH="$TEST/player-bin" "$HELPER" '--include=/private path/auth.conf' '--script=/private path/pepe.lua'
assert_line "$TEST_LAUNCH_OUT" run
assert_line "$TEST_LAUNCH_OUT" --user
assert_line "$TEST_LAUNCH_OUT" '--include=/private path/auth.conf'
assert_line "$TEST_LAUNCH_OUT" '--script=/private path/pepe.lua'
TEST_FLATPAK_INSTALL=--system
PATH="$TEST/player-bin" "$HELPER" --version
assert_line "$TEST_LAUNCH_OUT" --system
TEST_FLATPAK_INSTALL=missing
expect_failure env PATH="$TEST/player-bin" "$HELPER" --version
cat > "$TEST/player-bin/mpv" <<'EOF'
#!/usr/bin/env sh
printf 'native\n%s\n' "$*" > "$TEST_LAUNCH_OUT"
exit 42
EOF
chmod +x "$TEST/player-bin/mpv"
status=0
PATH="$TEST/player-bin" "$HELPER" --version || status=$?
[ "$status" -eq 42 ] || fail 'Native mpv exit status was not forwarded'
assert_line "$TEST_LAUNCH_OUT" native
printf 'Standalone installation tests passed.\n'
