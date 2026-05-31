#!/usr/bin/env sh
set -eu

APP_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
CONFIG_FILE="${POLOCHON_GIO_CONFIG:-$HOME/.config/polochon-gio/config.json}"

export PATH="$APP_DIR:$PATH"

exec "$APP_DIR/polochon-gio" \
  -config "$CONFIG_FILE" \
  -fullscreen \
  "$@"
