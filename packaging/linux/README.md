# Linux setup

## Native binary installation

Download `pepe-…-linux-amd64.tar.gz` (most PCs and the Steam Deck) or
`pepe-…-linux-arm64.tar.gz`, and optionally `SHA256SUMS`, from the
[latest release](https://github.com/gregdel/pepe/releases/latest), then:

```sh
sha256sum -c --ignore-missing SHA256SUMS
tar -xzf pepe-*-linux-*.tar.gz
cd pepe-*-linux-*/
./pepe install
```

To upgrade, do the same with a newer release.

Checkout desktop registration is no longer supported. If an older checkout owns
the desktop entry, unregister it **before updating the checkout** using its old
script, with the same HOME/XDG data directory used to register it:

```sh
scripts/install-desktop.sh --uninstall
```

If you have already updated, use the script from the older checkout or revision.
The copied installer refuses to overwrite the old entry; it does not migrate or
remove it automatically. The old script preserves unverified icons.

No sudo, compiler or network is needed. Installation runs before configuration
loading, Polochon server access or UI startup. mpv is external and is not installed.
The executable and its embedded artwork/wrappers are copied, not symlinked to the
download:

- `$XDG_DATA_HOME/pepe/`: one real directory containing the binary,
  artwork in `assets/`, and `mpv-auto.sh`, `mpv-inhibit.sh`, `mpv-keepawake.sh`.
- `$HOME/.local/bin/pepe`: stable launcher for the menu, terminal and Steam.
- `$XDG_DATA_HOME/applications/pepe.desktop`: menu entry using absolute paths.
- `$XDG_CONFIG_HOME/pepe/config.yaml`: mode-600 template **only if absent**.

XDG defaults are `$HOME/.local/share` and `$HOME/.config`; relative XDG data paths
are ignored, while relative XDG config paths are rejected to match application
configuration lookup. Symlink installation parents and unrelated existing files
are refused. The download and source checkout can be deleted after installation.

Edit `polochon_url` and `polochon_token` in
`${XDG_CONFIG_HOME:-$HOME/.config}/pepe/config.yaml` before launching. Keep the
token private; pépé passes it to mpv through a private temporary configuration,
not process arguments:

```sh
"$HOME/.local/bin/pepe"
```

Existing config, `mpv_path` and permissions are preserved. Only a fresh config
selects `${XDG_DATA_HOME:-$HOME/.local/share}/pepe/mpv-auto.sh`, which
tries native mpv in PATH, then user/default-system Flatpak `io.mpv.Mpv`.
Polochon environment overrides and command-line precedence are unchanged. mpv
0.41+ with Lua is required; discovery does not guarantee player capabilities.
The mpv Lua UI/fonts are already embedded and extracted temporarily per playback
session, not installed globally. No packages, global mpv config, power settings
or Steam shortcut/artwork/controller databases are changed automatically.
Missing/invalid config or an unavailable server still prevents normal UI startup;
installation needs neither. There is no setup UI.

Upgrade or reinstall with `./pepe install`. The payload is staged and verified,
then managed files are replaced directly in the `pepe/` directory, including locally
modified artwork/wrappers. Each file is replaced by rename, so reinstalling from the
running installed executable also works. There is no release history or whole-update
atomic switch: an interrupted upgrade can leave mixed versions; rerun `install` to
finish replacing the files. Uninstall with the installed binary and the same
HOME/XDG paths:

```sh
"$HOME/.local/bin/pepe" uninstall
```

Removal preserves configuration, modified/unknown installed files, the source icon,
mpv and Steam shortcuts. `.pepe-managed` (`pepe-user-install-v1`) and `SHA256SUMS`
record ownership of the installed files. Unrelated files already in the data directory
are left alone; conflicting managed paths are refused.

Older `pepe/app/...` installations are not migrated or removed by this installer.
Uninstall them with their original installer first, then update any old wrapper paths
in configuration manually. Existing configuration is never rewritten.

Installed opt-in wrapper paths:

- `${XDG_DATA_HOME:-$HOME/.local/share}/pepe/mpv-inhibit.sh`
- `${XDG_DATA_HOME:-$HOME/.local/share}/pepe/mpv-keepawake.sh`

Neither is enabled automatically. Use the expanded absolute path as `mpv_path`;
YAML does not expand shell variables. `mpv-inhibit.sh` needs systemd-inhibit/logind
and native mpv; inhibition failure prevents playback. See
[sleep limitations and keepawake requirements](#automatic-sleep-during-playback).
For customization, copy a wrapper outside the managed installation and configure
that copy; reinstalling overwrites managed wrappers.

**Compatibility:** the executable dynamically links Gio's Linux graphics/system
libraries, so compatible shared libraries are required **even for install/uninstall**.
Build on a defined SteamOS-compatible baseline and verify on the actual Deck;
success on a newer host does not prove compatibility with older SteamOS libraries.
No runtime library bundling or universal Linux compatibility is claimed. Linux is
supported; macOS app/DMG packaging and graphical onboarding remain separate work.

## Building and offline checks

With Go **1.27.1** and the appropriate native Gio development libraries/toolchain
for Linux amd64 or arm64, build with `./build.sh`, or keep the output separate:

```sh
mkdir -p dist
./build.sh -o dist/pepe .
(cd dist && sha256sum pepe > pepe.sha256)
```

`build.sh` fetches the Roboto fonts pinned in `packaging/fonts.txt` and the artwork
pinned in the platform-independent `packaging/assets.txt`, hosted in the
[assets-v1 release](https://github.com/gregdel/pepe/releases/tag/assets-v1). Downloads use HTTPS and
SHA-256 verification; failures abort the build. `packaging/embed` and embedded
fonts are gitignored, and verified cached files work offline. Original artwork
bytes/resolutions are retained; no images are committed. These downloads happen
only at build time, never during binary installation or launch. The shared
manifest remains usable by future macOS packaging.

Offline validation (with embedded inputs prepared by `./build.sh`; requires
ShellCheck, `desktop-file-validate` and GLib's `gio` command):

```sh
shellcheck build.sh scripts/fetch-assets.sh scripts/test-assets.sh scripts/test-install.sh \
  packaging/linux/mpv-auto.sh
scripts/test-install.sh
scripts/test-assets.sh
go test ./internal/install ./packaging
```

Installation checks exercise the real binary with isolated HOME/XDG directories,
no actual mpv or Polochon server; asset checks use mocked downloads.

If the menu launch appears to do nothing, run `"$HOME/.local/bin/pepe"` from a
terminal to see startup errors. Missing configuration or an unreachable server
prevents the window from opening. Use `-logFile PATH` for more logging; there is
no graphical setup screen. The old wrapper's `PEPE_CONFIG` variable is not used;
use `-config PATH` for manual launches instead. Real Wayland/X11 menu appearance
and window grouping still need desktop testing.

## Steam Deck (optional)

Run `./pepe install` in Desktop Mode. Add pépé from the application menu or its
installed `pepe.desktop` as a Non-Steam Game. Use the stable
`$HOME/.local/bin/pepe` launcher rather than a release-specific executable.
Set `fullscreen: true` and `player_fullscreen: true` in YAML before launching
from Gaming Mode. Ensure the configured mpv executable is also available there.

Installed artwork is in
`${XDG_DATA_HOME:-$HOME/.local/share}/pepe/assets`; apply it manually
in Steam:

| File | Role | Actual original size |
| --- | --- | --- |
| `application_icon_1024.png` | Desktop/application icon | 1024×1024 |
| `logo_1280x720.png` | Transparent pictorial logo | 1672×941 |
| `background_1920x620.jpg` | Library hero | 1760×576 |
| `wide_cover_920x430.jpg` | Wide cover | 1408×704 |
| `cover_600x900.jpg` | Portrait cover | 832×1248 |

Recommended Steam Input mapping:

- D-pad or left stick: arrow keys
- A: Enter
- B: Escape
- X: Tab
- Y: S
- Menu: R
- View: Q

The optional web remote is disabled by default. Read its
[security caveats](#web-remote-caveats) before enabling it.

### Automatic sleep during playback

Gaming Mode can suspend the Deck during mpv playback despite an active systemd
idle inhibitor. Tests on **SteamOS 3.8.28, stable channel** produced these results,
with both locks confirmed in `systemd-inhibit --list`:

| Inhibitor (`--mode=block`) | Observed result |
| --- | --- |
| `--what=idle` | The Deck still suspended and playback stopped. |
| `--what=sleep` | SSH remained usable, but the display went black; pointer activity did not restore the picture. |

A D-Bus trace captured an explicit `org.freedesktop.login1.Manager.Suspend()`
request from `dbus-send` inside `steam-launcher.service`. A logind idle inhibitor
inhibits logind's idle handling, not explicit suspend requests from another
process. A sleep inhibitor can block those requests, including manual suspend,
but did not keep Gaming Mode's display usable in this test.

The generic [`mpv-inhibit.sh`](mpv-inhibit.sh) wrapper is useful where the Linux
idle policy honors logind idle inhibitors; it is **not a Gaming Mode fix**.
No proper inhibition solution preserving both automatic playback inhibition and
manual suspend was verified on this setup. Do not assume a stronger lock solves
the problem.
See upstream [application inhibition support](https://github.com/ValveSoftware/SteamOS/issues/1881)
and [black display when suspend is inhibited](https://github.com/ValveSoftware/SteamOS/issues/2619).
Retest on the target SteamOS and Steam client versions before relying on a fix.

#### Opt-in input workaround

[`mpv-keepawake.sh`](mpv-keepawake.sh) is a separate, experimental workaround:
it sends an unused raw X keycode (97) every 20 seconds for the entire mpv
launcher lifetime, **including while paused, buffering, or playing in the
background**. A manual pulse test kept playback and the display normal past
the usual sleep timeout on the Deck above. The complete wrapper still needs
on-device validation, including automatic sleep after exit and manual
suspend/resume.

To try it, set `mpv_path` to the installed wrapper's absolute path (adjust for
your HOME/XDG data directory), or to a separately customized copy:

```yaml
mpv_path: /home/you/.local/share/pepe/mpv-keepawake.sh
```

It launches `$HOME/.local/share/flatpak/exports/bin/io.mpv.Mpv`, matching a
per-user Flatpak installation; edit its `mpv_path` assignment for another
launcher. All original playback arguments are forwarded unchanged. The input
worker runs on the host, outside Flatpak, without extra sandbox permissions.

The worker only activates when `XDG_CURRENT_DESKTOP` includes `gamescope`.
It requires Linux `/proc`, host `xdotool`, `xmodmap`, `awk`, and GNU `timeout`,
and a usable inherited `DISPLAY`/`XAUTHORITY`. It checks that keycode 97 remains
unmapped and is not a modifier before each pulse; it never rewrites the keymap,
clears modifiers, or moves the mouse. If validation or input fails, the worker
stops with a warning on stderr while the player continues normally. pépé currently
discards player stderr, so diagnose warnings by running the wrapper directly
from the Gaming Mode environment. The worker checks its parent before each
pulse and exits at the next 20-second check after the launcher ends, including
after forced termination.

This simulates activity, not inhibition. It takes no systemd lock and changes
no power settings. Do not chain it through the failed sleep-inhibitor approach.
To undo it, restore your previous `mpv_path`. Static check:

```sh
shellcheck packaging/linux/mpv-keepawake.sh
```

Validate playback, pause/background behavior, automatic sleep after exit, and
manual suspend/resume on the Deck.

## HDMI-CEC remote

Use the kernel CEC/RC input path, not a libCEC daemon or synthetic global
keyboard injection. The focused Wayland/X11 window receives the remote:
pépé while browsing, mpv during playback. This requires a CEC-capable HDMI
adapter/driver, kernel `CONFIG_MEDIA_CEC_RC`, v4l-utils (`cec-ctl` and
`ir-keytable`), and a compositor that accepts the RC evdev device as a
keyboard. Enable CEC on the TV and select this HDMI input. Some TVs do not
forward every button.

### Controls

Select `controls: hdmi-cec` in YAML or launch with `-controls=hdmi-cec`.
`help_bar: false` / `-help-bar=false` hides hints without disabling controls.
The playback overrides below are only enabled by this control profile.

| Remote | pépé | mpv with HDMI-CEC controls |
| --- | --- | --- |
| Arrows | Navigate; **Right** opens a title's detail | Default mpv seek controls |
| OK | Activate (does not open browse terminals) | Toggle pause |
| Back/Exit | Close subtitles, leave details, or go back | Quit to pépé |
| Root/contents menu | Home (close subtitles first) | Quit to pépé |
| Red | Mark the movie or episode watched/unwatched | Ignored |
| Blue | Subtitle search | Cycle subtitles |
| Green | Refresh library in browse | Toggle help |
| Yellow | Movies/shows in browse | Select subtitle track |
| Play, pause, play/pause | Ignored | Resume, pause, toggle |
| Stop | Ignored | Quit to pépé |
| Rewind / fast forward | Ignored | Seek -10 / +10 seconds |
| Previous / next | Ignored | Previous / next chapter |
| Programme/channel + / − | Ignored | Volume +5 / −5 points, only when opted in below |

`cec.toml` maps CEC Select/Exit to Enter/Escape; the kernel defaults
KEY_OK/KEY_EXIT are not understood by Gio's pinned XKB backend. Colour buttons
share keyboard shortcuts: Blue sends S, Green sends R, Red sends W, and
Yellow sends Tab. Letter keys follow the seat's active keyboard layout; the
layout must translate KEY_S/KEY_R/KEY_W to s/r/w (including lock/modifier state). Unlike
the old F-key mapping, these shortcuts are not layout-independent. Transport
buttons retain dedicated F5–F12 mappings because Play/Pause/Stop have distinct
semantics and no pépé browse shortcuts. Programme +/− send F3/F4, which are
ignored unless playback volume control is explicitly enabled. Existing
keyboard/vim navigation is unchanged. Home returns to browse home without
changing the listing mode.

During playback, Yellow opens mpv's native selection UI with an Off entry and
all loaded subtitle tracks. Up/Down and OK choose a track; Back dismisses the
list without quitting or changing pause state. Blue remains a quick cycle.
Green shows help without pausing. Keyboard-mode mpv retains its own shortcuts.

**Migration:** reload `cec.toml` after upgrading. Red sent Home before it marked
titles watched. Older colour assignments were
Red=refresh, Green=movies/shows, Yellow=Home, and Blue/Red/Green emitted F1–F3.
Do not mix an old installed keymap with the new help/bindings.

Power, volume, mute, and unsupported buttons are deliberately absent.
**Clear the default keymap** when loading this one, so forwarded CEC power
keys cannot reach logind. No automatic TV wake, input switching, standby,
system shutdown, ARC or TV volume control is implemented.

### Optional programme-key volume

For analogue speakers, programme/channel +/− can adjust mpv's software volume
without taking over the TV's normal volume buttons. The TV must forward these
keys to the playback device. After updating both pépé and `cec.toml`, opt in
through the launched player's mpv configuration:

```ini
script-opts-append=pepe-volume_keys=yes
```

With `-controls=hdmi-cec`, this enables repeatable 5-point volume steps with
mpv's native on-screen volume indicator and sets `volume-max=100` to prevent
amplification. F3/F4 are no-ops by default; keyboard mode is unchanged even if
the option is set. Leave it disabled on HDMI-audio devices. This controls the
player, not ALSA mixer gain, TV volume, mute or ARC.

Each new player starts at its configured volume (mpv defaults to 100). Add
`volume=50`, for example, for a lower startup level. Changes are not remembered
between player processes. Keep the analogue mixer/amplifier at a safe level.

### Manual setup

The Raspberry Pi appliance manages device setup separately; its source pin,
launch profile, and keymap must be updated together. Older images also inject
their own pause overlay/input.conf and need migration before using this shared
interface (remove the duplicate overlay and unconditional input overrides).
On other Linux systems, inspect the actual devices first:

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
sudo cec-ctl --device "$device" --playback --cec-version-1.4 --osd-name=pepe --rc-src-dev-root
sudo ir-keytable --sysdev "$rc" --read
```

The driver supplies the physical address from HDMI EDID. Adapter setup
persists after cec-ctl exits and is reclaimed by the kernel on HDMI hotplug;
the keymap must be reloaded when the RC device is recreated or after reboot.
For persistent setup, use your distribution's udev/systemd configuration.
Do not run a competing libCEC/cec-follower process that takes ownership.

No mpv input.conf installation or special wrapper is required. pépé loads its
embedded playback script and installs the remote bindings only with
`-controls=hdmi-cec`. `mpv-cec.conf` documents those bindings; do not load it
unconditionally or merge it into global mpv configuration. See
[wrapper requirements](#playback-wrappers) when using a custom launcher.

If your compositor ignores the device, it needs a CEC-specific udev rule
setting `ID_INPUT_KEYBOARD=1`. Keep input access seat-scoped; do not add pépé
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

## Configuration caveats

- Nonempty `POLOCHON_URL`, `POLOCHON_TOKEN` and `POLOCHON_MPV` environment variables
  override `polochon_url`, `polochon_token` and `mpv_path`. Explicit command-line
  options override the corresponding settings, including explicit `false` values.
  Terminal-only environment variables may not reach menu or Steam launches.
- Use the server's canonical HTTPS URL directly. Artwork redirects must preserve
  scheme, hostname and port to protect the token; even HTTP-to-HTTPS redirects
  are rejected.

## Web remote caveats

**There is no authentication or TLS. Use only on a trusted network; never expose
it publicly.** Anyone who can reach the remote can control or quit pépé and ask mpv
to fetch HTTP/HTTPS resources accessible from the host. Browser cross-origin checks
are not authentication. A listen address such as `:8081` binds all interfaces;
`127.0.0.1:8081` restricts access to the local machine.

- URL playback does not add the Polochon token, catalog subtitles or resume
  positions. Signed URLs can still appear in local process arguments.
- Website URLs depend on your existing mpv/resolver setup; pépé installs no resolver.
- The remote still requires normal Polochon configuration and a successful initial
  library fetch. A successful HTTP command response means queued, not playing;
  check the remote's status for playback failures.

## Playback wrappers

Forward all original mpv arguments unchanged, including authentication includes,
subtitles, resume, IPC, and pépé's script/options/font arguments. pépé extracts its
playback UI into a private per-session runtime; no global mpv config is installed.
Third-party mpv UI scripts may conflict with the shared panel: `mpv_osd: native`
disables pépé's panel without disabling CEC bindings.

### Managed launchers

Enable `mpv_managed_launcher` only for launchers that control a separate worker or
perform recovery after playback. Ordinary mpv and the bundled wrappers do not need it.

A managed launcher must:

1. Accept `--managed-runtime=DIR` before the original mpv arguments and forward
   the original arguments unchanged to the player.
2. Stay in the foreground until playback has stopped and recovery is complete,
   including after TERM.
3. Atomically rename a private temporary file to `DIR/launcher-complete`, containing
   exactly `stopped-and-restored\n` (including the final newline), then exit with
   the playback result.

`mpv_startup_timeout` bounds IPC connection attempts, not recovery.
`mpv_stop_timeout` is the grace period after TERM before killing the launcher.
Missing or malformed completion retains the private runtime and blocks new playback;
retry after the launcher writes a valid confirmation to clear the block. pépé trusts
that confirmation. The block is in-memory: launchers must handle parent disappearance
and recovery across app restarts themselves.
