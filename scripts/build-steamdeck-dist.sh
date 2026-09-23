#!/usr/bin/env sh
set -eu

ROOT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
DIST_DIR="$ROOT_DIR/dist/pepe-steamdeck"
INSTALL_DIR="${INSTALL_DIR:-/home/deck/Applications/pepe}"

mkdir -p "$DIST_DIR"

cd "$ROOT_DIR"
"$ROOT_DIR/build.sh" -o "$DIST_DIR/pepe" .

cp "$ROOT_DIR/packaging/steamdeck/run-pepe.sh" "$DIST_DIR/run-pepe.sh"
cp "$ROOT_DIR/packaging/steamdeck/config.example.yaml" "$DIST_DIR/config.example.yaml"
cp "$ROOT_DIR/packaging/steamdeck/README.md" "$DIST_DIR/README.md"
sed "s|@INSTALL_DIR@|$INSTALL_DIR|g" \
  "$ROOT_DIR/packaging/steamdeck/pepe.desktop.in" \
  > "$DIST_DIR/pepe.desktop"

chmod +x "$DIST_DIR/pepe" "$DIST_DIR/run-pepe.sh"

printf 'Built %s\n' "$DIST_DIR"
