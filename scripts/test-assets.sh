#!/usr/bin/env sh
# Build-time artwork download checks with isolated fixtures and no network.
set -eu
ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
command -v sha256sum >/dev/null 2>&1 || { printf 'Required test command not found: sha256sum\n' >&2; exit 1; }
mkdir -p "$ROOT/.pi/tmp"
TEST=$(mktemp -d "$ROOT/.pi/tmp/assets-test.XXXXXX")
trap 'rm -rf -- "$TEST"' EXIT
trap 'exit 1' HUP INT TERM
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
assert_line() { grep -Fx -- "$2" "$1" >/dev/null || fail "Missing line '$2' in $1"; }
expect_failure() { if "$@" > "$TEST/output" 2>&1; then fail "Unexpected success: $*"; fi; }
REPO="$TEST/repo"
mkdir -p "$REPO/scripts" "$REPO/packaging" "$TEST/bin" "$TEST/fixtures"
cp "$ROOT/scripts/fetch-assets.sh" "$REPO/scripts/"
while read -r digest name url; do
	case "$digest" in ''|'#'*) continue ;; esac
	printf 'artwork fixture: %s\n' "$url" > "$TEST/fixtures/$name"
	hash=$(sha256sum < "$TEST/fixtures/$name")
	printf '%s %s %s\n' "${hash%% *}" "$name" "$url"
done < "$ROOT/packaging/assets.txt" > "$REPO/packaging/assets.txt"
export TEST_REPO="$REPO" TEST_FIXTURES="$TEST/fixtures" TEST_CURL_LOG="$TEST/downloads" TEST_CURL_ARGS="$TEST/args"
export TEST_CURL_MODE=success
cat > "$TEST/bin/curl" <<'EOF'
#!/usr/bin/env sh
set -eu
printf 'download\n' >> "$TEST_CURL_LOG"
printf '%s\n' "$@" > "$TEST_CURL_ARGS"
output=''
url=''
while [ "$#" -gt 0 ]; do
	case "$1" in --output) output=$2; shift 2 ;; *) url=$1; shift ;; esac
done
case "$TEST_CURL_MODE" in
	offline) printf 'partial\n' > "$output"; exit 28 ;;
	corrupt) printf 'corrupt\n' > "$output"; exit 0 ;;
esac
name=$(awk -v url="$url" '$3 == url { print $2 }' "$TEST_REPO/packaging/assets.txt")
[ -n "$name" ]
cp "$TEST_FIXTURES/$name" "$output"
EOF
chmod +x "$TEST/bin/curl"
export PATH="$TEST/bin:$PATH"
FETCH="$REPO/scripts/fetch-assets.sh"
DEST="$REPO/packaging/embed"
expect_failure "$FETCH"
"$FETCH" "$DEST"
for arg in --fail --location --proto --proto-redir '=https' --connect-timeout --max-time --max-filesize; do assert_line "$TEST_CURL_ARGS" "$arg"; done
cp "$TEST_CURL_LOG" "$TEST/downloads-before"
TEST_CURL_MODE=offline
"$FETCH" "$DEST"
cmp "$TEST_CURL_LOG" "$TEST/downloads-before" || fail 'Verified cache downloaded again'
printf 'damaged cache\n' > "$DEST/logo_1280x720.png"
TEST_CURL_MODE=corrupt
expect_failure "$FETCH" "$DEST"
assert_line "$DEST/logo_1280x720.png" 'damaged cache'
TEST_CURL_MODE=offline
expect_failure "$FETCH" "$DEST"
assert_line "$DEST/logo_1280x720.png" 'damaged cache'
TEST_CURL_MODE=success
"$FETCH" "$DEST"
cmp "$TEST/fixtures/logo_1280x720.png" "$DEST/logo_1280x720.png"
rm "$DEST/logo_1280x720.png"
ln -s "$TEST/outside" "$DEST/logo_1280x720.png"
expect_failure "$FETCH" "$DEST"
[ ! -e "$TEST/outside" ] || fail 'Downloader followed asset symlink'
[ -z "$(find "$DEST" -name '.asset.*' -print)" ] || fail 'Download scratch files leaked'
printf 'Artwork fetch tests passed.\n'
