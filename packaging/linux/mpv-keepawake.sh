#!/bin/sh
# Opt-in Gaming Mode workaround. Change this path for another mpv launcher.
mpv_path="$HOME/.local/share/flatpak/exports/bin/io.mpv.Mpv"

case ":${XDG_CURRENT_DESKTOP:-}:" in
	*:gamescope:*) ;;
	*) exec "$mpv_path" "$@" ;;
esac

launcher_pid=$$
(
	# Failures stop only this worker, never playback.
	set -e
	key_pending=false
	trap '
		if [ "$?" -ne 0 ]; then
			printf "mpv-keepawake: keepalive stopped; check tools, DISPLAY and keycode 97\n" >&2
		fi
		if "$key_pending"; then
			timeout -k 1 2 xdotool keyup 97 >/dev/null 2>&1 || :
		fi
	' 0
	trap 'exit 1' HUP INT TERM

	launcher_alive() {
		# Reparenting detects launcher exit without PID-reuse bookkeeping.
		while read -r field value rest; do
			case "$field" in PPid:) [ "$value" = "$launcher_pid" ]; return ;; esac
		done < /proc/self/status
		return 1
	}

	[ -n "${DISPLAY:-}" ]
	while launcher_alive; do
		mapping=$(timeout -k 1 2 xmodmap -pke -pm)
		printf '%s\n' "$mapping" | awk '
			$1 == "keycode" && $2 == 97 && $3 == "=" {
				found = 1
				for (i = 4; i <= NF; i++)
					if ($i != "NoSymbol") mapped = 1
			}
			/\(0x0*61\)/ { mapped = 1 }
			END { exit !(found && !mapped) }
		'
		launcher_alive || break
		key_pending=true
		timeout -k 1 2 xdotool key 97
		key_pending=false
		sleep 20
	done
) </dev/null &

exec "$mpv_path" "$@"
