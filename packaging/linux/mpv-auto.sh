#!/usr/bin/env sh
# Used only by a new bundle configuration. Explicit user mpv_path values are untouched.
set -eu
if command -v mpv >/dev/null 2>&1; then
	exec mpv "$@"
fi
if command -v flatpak >/dev/null 2>&1; then
	for installation in --user --system; do
		if flatpak info "$installation" io.mpv.Mpv >/dev/null 2>&1; then
			exec flatpak run "$installation" io.mpv.Mpv "$@"
		fi
	done
fi
printf 'Pépé needs mpv 0.41+ with Lua support. Install mpv (or io.mpv.Mpv through Discover), or set mpv_path in your config.\n' >&2
exit 1
