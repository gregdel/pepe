# Linux setup

## Desktop registration from source

From the checkout, build the application:

```sh
./build.sh
```

Configure Polochon before launching from the desktop menu. For a new configuration,
copy the example without overwriting an existing file:

```sh
config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/pepe"
mkdir -p "$config_dir"
if [ ! -e "$config_dir/config.yaml" ]; then
    (umask 077; cp packaging/linux/config.example.yaml "$config_dir/config.yaml")
fi
chmod 600 "$config_dir/config.yaml"
```

Edit `polochon_url` and `polochon_token` in that file. Keep the token private. Pépé
passes it to mpv through a private temporary configuration, not process arguments.
Ensure `mpv` is available in the desktop session's PATH, or set `mpv_path` to an
absolute executable/wrapper path. Managed mode stays off for ordinary mpv; see
[managed launchers](../../README.md#managed-launchers) for worker/recovery wrappers.
The registration script does not install dependencies or write configuration.

Register the built binary:

```sh
scripts/install-desktop.sh
```

The script can be called from any working directory. It points directly to the
`pepe` executable beside `build.sh`; it neither builds nor copies the binary.
The menu entry respects your existing configuration and does not force fullscreen,
change PATH, enable logging, or put credentials in the desktop file. Set
`fullscreen: true` and `player_fullscreen: true` in YAML for a TV/fullscreen setup.
Environment overrides still work, but variables exported only in a terminal may
not be available to the desktop session.

Registration installs per-user files under `$XDG_DATA_HOME`, defaulting to
`$HOME/.local/share`. Relative XDG data paths are ignored:

- `applications/pepe.desktop`: menu entry, with an absolute executable path.
- `pepe/icon.png`: local copy of the
  [hosted Pépé icon](https://git.quimbo.fr/attachments/ab9e56b5-bf62-4723-8f30-1438ab62a282).

The PNG is downloaded with HTTPS-only redirects and a 20-second timeout, then
checked against a pinned SHA-256 before installation. No image is committed or
fetched during application launch. Subsequent registrations reuse a verified
local copy. If `curl`/`sha256sum` are unavailable or downloading/checking fails,
registration still works with the desktop theme's generic video icon. Run the
script again to retry. An unrecognized existing icon is left untouched; remove it
manually and re-register if you want a fresh download. Changes to the hosted image
require updating the checksum in the script deliberately.

Rebuilding `pepe` updates what the menu launches immediately. Moving or deleting
the checkout breaks its shortcut; after moving, re-register from the new location.
Checkout paths containing `=` or `%`, and checkout/data paths containing control
characters, are rejected because desktop launchers cannot safely handle them.
Only one checkout can own the per-user Pépé shortcut at a time. Registration refuses
to replace an unrelated existing `pepe.desktop` or a symlink at that path.

Unregister with the same XDG data directory used for installation:

```sh
scripts/install-desktop.sh --uninstall
```

This leaves the checkout binary, configuration, and unrelated files intact. It
removes only the managed desktop entry and an icon matching the pinned hash;
modified/unverified icons are preserved with a warning. No network or built binary
is needed to uninstall. Desktop database refresh is optional when its tool exists;
icon-theme cache updates are unnecessary because the entry uses a local icon path.

If the menu launch appears to do nothing, run `./pepe` from a terminal to see startup
errors. Missing configuration or an unreachable server currently prevents the
window from opening. For more logging, use `./pepe -logFile ./pepe.log`.
There is no graphical setup screen. Existing copied Steam Deck bundles and Steam
shortcuts are not migrated or removed automatically. The old wrapper's
`PEPE_CONFIG` variable is not used; the application supports `-config PATH` for
manual launches instead.

Offline packaging checks (requires ShellCheck, `desktop-file-validate`, and GLib's
`gio` command):

```sh
shellcheck scripts/install-desktop.sh scripts/test-desktop.sh
scripts/test-desktop.sh
```

The tests use temporary HOME/XDG directories, mocked icon downloads, and a fake
application; they do not require a Polochon server or change your desktop menu.
Real Wayland/X11 menu appearance and window grouping still need desktop testing.

## Steam Deck (optional)

Use the same source build, configuration, and registration in Desktop Mode. Add
Pépé from the application menu, its installed `pepe.desktop`, or the checkout's
`pepe` binary as a Non-Steam Game. Enable fullscreen in YAML before launching from
Gaming Mode. Ensure the configured mpv executable is also available there.

Recommended Steam Input mapping:

- D-pad or left stick: arrow keys
- A: Enter
- B: Escape
- X: Tab
- Y: S
- Menu: R
- View: Q

The optional [web remote](../../README.md#web-remote-and-url-playback) works on
trusted networks; it is disabled by default and has no authentication.

## HDMI-CEC remote

Use the kernel CEC/RC input path, not a libCEC daemon or synthetic global
keyboard injection. The focused Wayland/X11 window receives the remote:
Pépé while browsing, mpv during playback. This requires a CEC-capable HDMI
adapter/driver, kernel `CONFIG_MEDIA_CEC_RC`, v4l-utils (`cec-ctl` and
`ir-keytable`), and a compositor that accepts the RC evdev device as a
keyboard. Enable CEC on the TV and select this HDMI input. Some TVs do not
forward every button.

### Controls

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

### Manual setup

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

### Diagnostics

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
