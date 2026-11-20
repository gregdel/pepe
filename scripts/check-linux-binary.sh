#!/usr/bin/env sh
# Fail if a release binary links a library outside the expected Gio/glibc set or
# needs a newer glibc than the oldest supported baseline (Ubuntu 22.04).
set -eu
if [ "$#" -ne 1 ]; then
	printf 'Usage: %s BINARY\n' "$0" >&2
	exit 2
fi
MAX_GLIBC=2.35
ALLOWED='libc.so.6 libm.so.6 libresolv.so.2 libdl.so.2 libpthread.so.0
libEGL.so.1 libwayland-client.so.0 libwayland-cursor.so.0 libwayland-egl.so.1
libxkbcommon.so.0 libxkbcommon-x11.so.0 libX11.so.6 libX11-xcb.so.1 libxcb.so.1
libXcursor.so.1 libXfixes.so.3'

status=0
for lib in $(readelf -d "$1" | sed -n 's/.*(NEEDED).*\[\(.*\)\]/\1/p'); do
	case " $(printf '%s' "$ALLOWED" | tr '\n' ' ') " in
		*" $lib "*) printf 'needed: %s\n' "$lib" ;;
		*) printf 'Unexpected linked library: %s\n' "$lib" >&2; status=1 ;;
	esac
done
glibc=$(objdump -T "$1" | grep -o 'GLIBC_[0-9.]*' | sed 's/GLIBC_//' | sort -uV | tail -n 1)
printf 'newest glibc symbol: %s (max %s)\n' "$glibc" "$MAX_GLIBC"
if [ "$(printf '%s\n%s\n' "$glibc" "$MAX_GLIBC" | sort -V | tail -n 1)" != "$MAX_GLIBC" ]; then
	printf 'Binary needs glibc %s, newer than %s\n' "$glibc" "$MAX_GLIBC" >&2
	status=1
fi
exit "$status"
