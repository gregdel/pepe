#!/usr/bin/env sh
set -eu

ROOT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)

rm -f "$ROOT_DIR/internal/app/embed/font.ttf" # Remove the legacy generated Fira Code asset.
"$ROOT_DIR/scripts/fetch-assets.sh" "$ROOT_DIR/internal/app/embed" "$ROOT_DIR/packaging/fonts.txt"
"$ROOT_DIR/scripts/fetch-assets.sh" "$ROOT_DIR/packaging/embed"

cd "$ROOT_DIR"
go build "$@"
