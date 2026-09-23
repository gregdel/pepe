#!/usr/bin/env sh
set -eu

APP_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
CONFIG_FILE="${PEPE_CONFIG:-$HOME/.config/pepe/config.yaml}"

export PATH="$APP_DIR:$PATH"

exec "$APP_DIR/pepe" \
  -config "$CONFIG_FILE" \
  -fullscreen \
  "$@"
