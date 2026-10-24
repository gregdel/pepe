#!/usr/bin/env sh
# Offline packaging tests. All user paths and command mocks live in one fixture.
set -eu

ROOT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
for command in desktop-file-validate gio; do
	command -v "$command" >/dev/null 2>&1 || {
		printf 'Required test command not found: %s\n' "$command" >&2
		exit 1
	}
done
mkdir -p "$ROOT_DIR/.pi/tmp"
TEST_DIR=$(mktemp -d "$ROOT_DIR/.pi/tmp/desktop-test.XXXXXX")
trap 'rm -rf -- "$TEST_DIR"' EXIT
trap 'exit 1' HUP INT TERM

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

assert_line() {
	grep -Fx -- "$2" "$1" >/dev/null || fail "Missing line '$2' in $1"
}

# Exercise both desktop-string and Exec escaping; data paths also contain '%'.
CHECKOUT="$TEST_DIR/checkout spaces ' \" \` \$ \\ é"
mkdir -p "$CHECKOUT/scripts" "$CHECKOUT/packaging/linux" "$TEST_DIR/bin" "$TEST_DIR/home"
cp "$ROOT_DIR/scripts/install-desktop.sh" "$CHECKOUT/scripts/"
cp "$ROOT_DIR/packaging/linux/pepe.desktop.in" "$CHECKOUT/packaging/linux/"
INSTALL="$CHECKOUT/scripts/install-desktop.sh"
export HOME="$TEST_DIR/home"
export XDG_DATA_HOME="$TEST_DIR/data spaces \$ % \\ é"
export TEST_CURL_LOG="$TEST_DIR/curl.log"
export TEST_CURL_ARGS="$TEST_DIR/curl.args"
export TEST_LAUNCH_OUT="$TEST_DIR/launched"
export TEST_DOWNLOAD_MODE=success
export PATH="$TEST_DIR/bin:$PATH"

# The hasher mock recognizes only the downloader's fixture. The production
# script's pinned-hash comparison is exercised without storing the real image.
cat > "$TEST_DIR/bin/sha256sum" <<'EOF'
#!/usr/bin/env sh
IFS= read -r content || :
if [ "$content" = pepe-test-icon ]; then
	printf '0a7ccf087cf627109d7756378ec05f9a86ffc1854a81ecb6855c4ba1723c374e  -\n'
else
	printf '0000000000000000000000000000000000000000000000000000000000000000  -\n'
fi
EOF
cat > "$TEST_DIR/bin/curl" <<'EOF'
#!/usr/bin/env sh
set -eu
printf 'download\n' >> "$TEST_CURL_LOG"
printf '%s\n' "$@" > "$TEST_CURL_ARGS"
output=''
while [ "$#" -gt 0 ]; do
	case "$1" in
		--output) output=$2; shift 2 ;;
		*) shift ;;
	esac
done
[ -n "$output" ] || exit 2
case "$TEST_DOWNLOAD_MODE" in
	success) printf 'pepe-test-icon\n' > "$output" ;;
	mismatch) printf 'unexpected-image\n' > "$output" ;;
	offline) printf 'partial-image\n' > "$output"; exit 28 ;;
	*) exit 2 ;;
esac
EOF
cat > "$TEST_DIR/bin/update-desktop-database" <<'EOF'
#!/usr/bin/env sh
exit 0
EOF
chmod +x "$TEST_DIR/bin/sha256sum" "$TEST_DIR/bin/curl" "$TEST_DIR/bin/update-desktop-database"

expect_failure() {
	if "$INSTALL" "$@" > "$TEST_DIR/output" 2>&1; then
		fail 'Registration unexpectedly succeeded'
	fi
}

register() {
	(cd "$TEST_DIR" && "$INSTALL") > "$TEST_DIR/output" 2>&1 || fail 'Registration failed'
	DESKTOP="$XDG_DATA_HOME/applications/pepe.desktop"
	desktop-file-validate "$DESKTOP" || fail 'Invalid generated desktop entry'
}

launch() {
	rm -f "$TEST_LAUNCH_OUT"
	gio launch "$DESKTOP" || fail 'Desktop launch failed'
	attempt=0
	while [ ! -f "$TEST_LAUNCH_OUT" ] && [ "$attempt" -lt 100 ]; do
		sleep 0.05
		attempt=$((attempt + 1))
	done
	[ -f "$TEST_LAUNCH_OUT" ] || fail 'Desktop did not launch the fake binary'
	assert_line "$TEST_LAUNCH_OUT" "$CHECKOUT/pepe"
	assert_line "$TEST_LAUNCH_OUT" 'arguments=0'
}

"$INSTALL" --help >/dev/null
expect_failure --unknown
expect_failure --uninstall extra
expect_failure
[ ! -e "$XDG_DATA_HOME" ] || fail 'Missing binary created installation files'
printf 'not executable\n' > "$CHECKOUT/pepe"
expect_failure
cat > "$CHECKOUT/pepe" <<'EOF'
#!/usr/bin/env sh
printf 'original\n%s\narguments=%s\n' "$0" "$#" > "$TEST_LAUNCH_OUT"
EOF
chmod +x "$CHECKOUT/pepe"

mkdir -p "$HOME/.config/pepe" "$XDG_DATA_HOME/applications"
printf 'private config\n' > "$HOME/.config/pepe/config.yaml"
printf 'unrelated\n' > "$XDG_DATA_HOME/applications/other.desktop"
printf '[Desktop Entry]\nName=Unrelated\n' > "$XDG_DATA_HOME/applications/pepe.desktop"
cp "$XDG_DATA_HOME/applications/pepe.desktop" "$TEST_DIR/unrelated.desktop"
expect_failure
expect_failure --uninstall
cmp "$TEST_DIR/unrelated.desktop" "$XDG_DATA_HOME/applications/pepe.desktop" || fail 'Unrelated launcher changed'
rm "$XDG_DATA_HOME/applications/pepe.desktop"
ln -s "$TEST_DIR/missing" "$XDG_DATA_HOME/applications/pepe.desktop"
expect_failure
expect_failure --uninstall
rm "$XDG_DATA_HOME/applications/pepe.desktop"

register
assert_line "$XDG_DATA_HOME/pepe/icon.png" pepe-test-icon
assert_line "$DESKTOP" 'Name=pépé'
assert_line "$DESKTOP" 'X-Pepe-Managed=true'
assert_line "$DESKTOP" 'Terminal=false'
for argument in --fail --location --proto --proto-redir '=https' --connect-timeout --max-time --max-filesize; do
	assert_line "$TEST_CURL_ARGS" "$argument"
done
assert_line "$TEST_CURL_ARGS" 'https://git.quimbo.fr/attachments/ab9e56b5-bf62-4723-8f30-1438ab62a282'
launch
assert_line "$TEST_LAUNCH_OUT" original

cp "$TEST_CURL_LOG" "$TEST_DIR/downloads-before"
cp "$DESKTOP" "$TEST_DIR/desktop-before"
TEST_DOWNLOAD_MODE=offline
register
cmp "$TEST_CURL_LOG" "$TEST_DIR/downloads-before" || fail 'Verified icon downloaded again'
cmp "$DESKTOP" "$TEST_DIR/desktop-before" || fail 'Repeated registration changed launcher'
# A rebuild must take effect without another registration.
sed 's/original/rebuilt/' "$CHECKOUT/pepe" > "$TEST_DIR/rebuilt"
cp "$TEST_DIR/rebuilt" "$CHECKOUT/pepe"
launch
assert_line "$TEST_LAUNCH_OUT" rebuilt

mv "$CHECKOUT" "$TEST_DIR/relocated checkout"
CHECKOUT="$TEST_DIR/relocated checkout"
INSTALL="$CHECKOUT/scripts/install-desktop.sh"
register
launch
rm "$CHECKOUT/pepe"
cp "$TEST_CURL_LOG" "$TEST_DIR/downloads-before"
"$INSTALL" --uninstall >/dev/null
[ ! -e "$DESKTOP" ] && [ ! -e "$XDG_DATA_HOME/pepe/icon.png" ] || fail 'Uninstall left owned assets'
"$INSTALL" --uninstall >/dev/null
cmp "$TEST_CURL_LOG" "$TEST_DIR/downloads-before" || fail 'Uninstall downloaded an icon'
assert_line "$HOME/.config/pepe/config.yaml" 'private config'
assert_line "$XDG_DATA_HOME/applications/other.desktop" unrelated

cp "$TEST_DIR/rebuilt" "$CHECKOUT/pepe"
chmod +x "$CHECKOUT/pepe"
for mode in offline mismatch; do
	TEST_DOWNLOAD_MODE=$mode
	register
	assert_line "$DESKTOP" 'Icon=video-x-generic'
	[ ! -e "$XDG_DATA_HOME/pepe/icon.png" ] || fail 'Failed download installed an icon'
	[ -z "$(find "$XDG_DATA_HOME" -name '.icon.*' -o -name '.pepe.*')" ] || fail 'Temporary files leaked'
done
TEST_DOWNLOAD_MODE=success
register
assert_line "$XDG_DATA_HOME/pepe/icon.png" pepe-test-icon
printf 'user image\n' > "$XDG_DATA_HOME/pepe/icon.png"
cp "$TEST_CURL_LOG" "$TEST_DIR/downloads-before"
register
assert_line "$DESKTOP" 'Icon=video-x-generic'
cmp "$TEST_CURL_LOG" "$TEST_DIR/downloads-before" || fail 'Unrecognized icon was replaced'
"$INSTALL" --uninstall >/dev/null
assert_line "$XDG_DATA_HOME/pepe/icon.png" 'user image'
[ -x "$CHECKOUT/pepe" ] || fail 'Uninstall removed binary'

# Empty/unset and invalid relative XDG overrides must use the HOME fallback.
TEST_DOWNLOAD_MODE=offline
for data_dir in '' relative; do
	XDG_DATA_HOME=$data_dir
	(cd "$TEST_DIR" && "$INSTALL") > "$TEST_DIR/output" 2>&1
	desktop-file-validate "$HOME/.local/share/applications/pepe.desktop"
	"$INSTALL" --uninstall >/dev/null
	[ ! -e "$TEST_DIR/relative" ] || fail 'Relative XDG directory was used'
done
unset XDG_DATA_HOME
(cd "$TEST_DIR" && "$INSTALL") > "$TEST_DIR/output" 2>&1
[ -f "$HOME/.local/share/applications/pepe.desktop" ] || fail 'Unset XDG override did not use HOME'
"$INSTALL" --uninstall >/dev/null

# Optional download tools may be absent; the remaining registration still works.
mkdir "$TEST_DIR/no-download-tools"
for command in sh dirname grep mkdir mktemp cp sed chmod mv rm; do
	ln -s "$(command -v "$command")" "$TEST_DIR/no-download-tools/$command"
done
XDG_DATA_HOME="$TEST_DIR/no-tools-data" PATH="$TEST_DIR/no-download-tools" "$INSTALL" > "$TEST_DIR/output" 2>&1
assert_line "$TEST_DIR/no-tools-data/applications/pepe.desktop" 'Icon=video-x-generic'
grep -F 'needs curl and sha256sum' "$TEST_DIR/output" >/dev/null || fail 'Missing tools did not produce guidance'
PATH="$TEST_DIR/no-download-tools" XDG_DATA_HOME="$TEST_DIR/no-tools-data" "$INSTALL" --uninstall >/dev/null

export XDG_DATA_HOME="$TEST_DIR/data"
mv "$CHECKOUT" "$TEST_DIR/invalid=checkout"
CHECKOUT="$TEST_DIR/invalid=checkout"
INSTALL="$CHECKOUT/scripts/install-desktop.sh"
expect_failure
mv "$CHECKOUT" "$TEST_DIR/invalid%checkout"
CHECKOUT="$TEST_DIR/invalid%checkout"
INSTALL="$CHECKOUT/scripts/install-desktop.sh"
expect_failure
mv "$CHECKOUT" "$TEST_DIR/invalid
checkout"
CHECKOUT="$TEST_DIR/invalid
checkout"
INSTALL="$CHECKOUT/scripts/install-desktop.sh"
expect_failure
printf 'Desktop registration tests passed.\n'
