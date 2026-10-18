#!/bin/sh
# Set pepe's mpv_path (or -mpv) to this wrapper's absolute path.
# Hold a systemd idle inhibitor until mpv exits, including while paused.
# This does not change power settings or block manual suspend.
# Idle/suspend timers must honor logind idle inhibitors for this to take effect.
# Requires systemd-inhibit, a working logind service, and mpv in PATH;
# inhibition failure prevents playback rather than falling back silently.
exec systemd-inhibit \
	--what=idle --mode=block --no-ask-password \
	--who=pepe --why="Video playback" \
	mpv "$@"
