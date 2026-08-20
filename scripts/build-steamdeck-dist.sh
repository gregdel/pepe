#!/usr/bin/env sh
set -eu

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
DIST_DIR="$ROOT_DIR/dist/polochon-gio-steamdeck"
INSTALL_DIR="${INSTALL_DIR:-/home/deck/Applications/polochon-gio}"

mkdir -p "$DIST_DIR"

cd "$ROOT_DIR"
"$ROOT_DIR/build.sh" -o "$DIST_DIR/polochon-gio" .

cp "$ROOT_DIR/packaging/steamdeck/run-polochon-gio.sh" "$DIST_DIR/run-polochon-gio.sh"
cp "$ROOT_DIR/packaging/steamdeck/config.example.json" "$DIST_DIR/config.example.json"
cp "$ROOT_DIR/packaging/steamdeck/README.md" "$DIST_DIR/README.md"
sed "s|@INSTALL_DIR@|$INSTALL_DIR|g" \
  "$ROOT_DIR/packaging/steamdeck/polochon-gio.desktop.in" \
  > "$DIST_DIR/polochon-gio.desktop"

chmod +x "$DIST_DIR/polochon-gio" "$DIST_DIR/run-polochon-gio.sh"

printf 'Built %s\n' "$DIST_DIR"
