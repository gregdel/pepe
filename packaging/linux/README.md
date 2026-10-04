# Linux HDMI-CEC remote

Use the kernel CEC/RC input path, not a libCEC daemon or synthetic global
keyboard injection. The focused Wayland/X11 window receives the remote:
Pépé while browsing, mpv during playback. This requires a CEC-capable HDMI
adapter/driver, kernel `CONFIG_MEDIA_CEC_RC`, v4l-utils (`cec-ctl` and
`ir-keytable`), and a compositor that accepts the RC evdev device as a
keyboard. Enable CEC on the TV and select this HDMI input. Some TVs do not
forward every button.

## Controls

| Remote | Pépé | mpv with mpv-cec.conf |
| --- | --- | --- |
| Arrows | Navigate; **Right** opens a title's detail | Default mpv seek controls |
| OK | Activate (does not open browse terminals) | Toggle pause |
| Back/Exit | Close subtitles, leave details, or go back | Quit to Pépé |
| Root/contents menu, yellow | Home (close subtitles first) | Quit to Pépé |
| Blue | Subtitle search | Cycle subtitles |
| Red | Refresh library | Ignored |
| Green | Movies/shows in browse | Ignored |
| Play, pause, play/pause | Ignored | Resume, pause, toggle |
| Stop | Ignored | Quit to Pépé |
| Rewind / fast forward | Ignored | Seek -10 / +10 seconds |
| Previous / next | Ignored | Previous / next chapter |

`cec.toml` maps CEC Select/Exit to Enter/Escape; the kernel defaults
KEY_OK/KEY_EXIT are not understood by Gio's pinned XKB backend. Colour and
transport buttons use layout-independent F keys. Existing keyboard/vim
navigation is unchanged; Escape now also leaves movie/show details, and
Home returns to browse home without changing the listing mode.

Power, volume, mute, and unsupported buttons are deliberately absent.
**Clear the default keymap** when loading this one, so forwarded CEC power
keys cannot reach logind. No automatic TV wake, input switching, standby,
system shutdown, ARC or volume control is implemented.

## Manual setup

The Raspberry Pi appliance configures this automatically. On other Linux
systems, inspect the actual devices first:

```sh
cec-ctl --list-devices
ir-keytable
```

Select the CEC adapter for the connected HDMI port and its corresponding
CEC RC device. Replace these placeholders; do not assume rc0/cec0:

```sh
device=/dev/cecX
rc=rcX
sudo ir-keytable --sysdev "$rc" --clear --write "$PWD/packaging/linux/cec.toml"
sudo cec-ctl --device "$device" --playback --cec-version-1.4 --osd-name=Pepe --rc-src-dev-root
sudo ir-keytable --sysdev "$rc" --read
```

The driver supplies the physical address from HDMI EDID. Adapter setup
persists after cec-ctl exits and is reclaimed by the kernel on HDMI hotplug;
the keymap must be reloaded when the RC device is recreated or after reboot.
For persistent setup, use your distribution's udev/systemd configuration.
Do not run a competing libCEC/cec-follower process that takes ownership.

For mpv, merge `mpv-cec.conf` into your existing input.conf, or select an
mpv wrapper through Pépé's `mpv_path` / `-mpv` option:

```sh
#!/bin/sh
exec mpv --input-conf=/absolute/path/to/packaging/linux/mpv-cec.conf "$@"
```

If your compositor ignores the device, it needs a CEC-specific udev rule
setting `ID_INPUT_KEYBOARD=1`. Keep input access seat-scoped; do not add Pépé
to the unrestricted `input` group or run it as root.

## Diagnostics

`sudo cec-ctl --device "$device"` should show RC passthrough, a valid physical
address and an allocated playback logical address. Check received messages
with `sudo cec-ctl --device "$device" --monitor`, and decoded keys with
`sudo ir-keytable --sysdev "$rc" --test`. Stop diagnostic monitors afterwards.
An invalid physical address usually means the TV is off, the cable/port is
disconnected, or no valid EDID is available. If CEC messages arrive but no
keys do, check RC passthrough, the loaded map and compositor device access.

Tests cover navigation/shortcut dispatch, not HDMI electrical signalling,
TV interoperability, hotplug or actual Cage focus handoff. Those require
a real adapter and TV.
