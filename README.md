![pépé](https://git.quimbo.fr/attachments/d60c99c5-e49b-4931-bc29-4dac2df14488)

# pépé

**pépé** (*Polochon Player* -> “PP” -> pépé, I know that's a stretch) is a Go/Gio player interface for browsing and playing movies and shows from a Polochon server. It is designed for keyboard, remote, and controller navigation, including TVs and Steam Deck.

Features include title-based directional search, recently added listings, movie and show details, subtitle selection, playback through `mpv`, and an opt-in web remote with URL playback. Linux HDMI-CEC remotes are supported through kernel RC passthrough; see [Linux CEC setup](packaging/linux/README.md#hdmi-cec-remote).

## Requirements

- Linux with compatible native Gio graphics shared libraries, **even for installation**; macOS packaging is separate
- A reachable Polochon server and its URL/token to run the app, not to install it
- External `mpv` **0.41 or newer with Lua support** for the shared playback interface (or `-mpv-osd=native -controls=keyboard` to use your existing player without it)

## Linux installation

Obtain the native Linux binary for your architecture as `pepe`, not a `.tar.gz`.
Optionally verify it with the supplied `pepe.sha256` in the same directory:

```sh
sha256sum -c pepe.sha256
```

If this checkout owns your desktop registration, first run
`scripts/install-desktop.sh --uninstall`; source registration stays separate.
Install for your user, with no sudo, compiler or network access required:

```sh
chmod +x ./pepe
./pepe install
```

Installation runs before configuration/server checks and UI startup. Edit
`polochon_url` and `polochon_token` in
`${XDG_CONFIG_HOME:-$HOME/.config}/pepe/config.yaml`, then launch:

```sh
"$HOME/.local/bin/pepe"
```

The binary, embedded artwork and `mpv-auto.sh`, `mpv-inhibit.sh`, `mpv-keepawake.sh`
are copied into one fixed directory, `$XDG_DATA_HOME/pepe/`
(default data home: `$HOME/.local/share`); `$HOME/.local/bin/pepe` stays stable.
Reinstalling replaces the managed files without retaining old releases. Individual
files are replaced atomically, not the whole installation. Artwork is copied, not
symlinked to the download; you can delete the download or checkout after installation.
Existing config, including `mpv_path`, is preserved; only a fresh config selects
the installed `mpv-auto.sh`. mpv stays external, and inhibit/keepawake wrappers stay
opt-in. No packages, power settings or Steam databases are changed automatically.

Upgrade by running `./pepe install` from the next downloaded binary. Uninstall
using the installed binary, with the same HOME/XDG paths:

```sh
"$HOME/.local/bin/pepe" uninstall
```

Configuration is preserved. Older `pepe/app/...` installations must be uninstalled
with their original installer first; update any old wrapper paths in config manually.
See
[Linux setup](packaging/linux/README.md#native-binary-installation) for installed
wrapper/artwork paths, compatibility, removal details and Steam Deck limitations.

## Build from source

Developers need Go **1.27.1** and native Gio development libraries:

```sh
./build.sh
./pepe
```

To keep the output separate:

```sh
mkdir -p dist
./build.sh -o dist/pepe .
(cd dist && sha256sum pepe > pepe.sha256)
```

`build.sh` fetches and SHA-256-verifies pinned Roboto/Noto Sans Symbols fonts and
artwork from `packaging/assets.txt`, using `scripts/fetch-assets.sh packaging/embed`
for the latter. Embedded inputs are gitignored; verified cached files work offline.
These downloads happen at build time, never during binary installation or launch.
You can use `go build .` once both embedded fonts and artwork are present.

## Configuration

The default config path is `$XDG_CONFIG_HOME/pepe/config.yaml`, or the platform config directory's `pepe/config.yaml`. If no platform config directory is available, the fallback is `./pepe.yaml`. For source runs, create a YAML file like this; keep the installed `mpv_path` when editing an installer-created config:

```yaml
polochon_url: https://polochon.example
polochon_token: your-token
mpv_path: mpv
fullscreen: false
player_fullscreen: true
mpv_managed_launcher: false
mpv_startup_timeout: 3s
mpv_stop_timeout: 2s
web_ui_addr: ""
controls: keyboard
help_bar: true
mpv_osd: pepe
```

`POLOCHON_URL` and `POLOCHON_TOKEN` override values in the file. `POLOCHON_MPV` overrides `mpv_path`. Keep the config readable only by yourself (for example, `chmod 600 ~/.config/pepe/config.yaml`); playback passes the token to mpv through a private temporary config file, not its process arguments. For example:

```sh
POLOCHON_URL=https://polochon.example \
POLOCHON_TOKEN=your-token \
./pepe
```

Artwork redirects must stay on the original origin (scheme, hostname, and port). Redirects to another origin, including HTTP-to-HTTPS upgrades and HTTPS-to-HTTP downgrades, are rejected to protect the authentication token. Use the server's canonical HTTPS URL directly if available. When logging is enabled, an effective `polochon_url` using HTTP (including a `POLOCHON_URL` override) produces a startup warning; intentional HTTP-only setups remain allowed.

Logging is disabled by default. For troubleshooting, launch with
`./pepe -logFile ./pepe.log`, then reproduce the issue and check the log for
warnings and errors. Startup failures are still reported on stderr.

Explicit playback flags override YAML settings, including `-mpv-managed-launcher=false`.

Useful flags:

- `-config PATH` — config file
- `-logFile PATH` — enable logging to this file (disabled by default)
- `-fullscreen` — start the app and player fullscreen
- `-windowed` — start the app windowed
- `-controls keyboard|hdmi-cec` — keyboard hints/native mpv keys (default), or remote hints/CEC playback bindings
- `-help-bar=false` — hide instructional bars without changing controls
- `-mpv-osd pepe|native` — shared playback panel (default), or native/custom mpv OSD
- `-mpv PATH` — mpv executable or wrapper
- `-mpv-startup-timeout DURATION` — IPC connection timeout (default `3s`)
- `-mpv-stop-timeout DURATION` — cancellation grace period before killing the launcher (default `2s`)
- `-mpv-managed-launcher` — require external launcher teardown confirmation (default off)
- `-web-ui-addr HOST:PORT` — enable the unauthenticated web remote; empty disables it (default)

## Controls and playback help

```sh
./pepe                                      # Keyboard hints and native mpv keys
./pepe -controls=hdmi-cec                    # Remote hints and CEC playback keys
./pepe -controls=hdmi-cec -help-bar=false    # Same controls without automatic hints
```

The matching YAML settings are `controls`, `help_bar`, and `mpv_osd`. Only
explicit flags override YAML, including `-help-bar=true` and `-controls=keyboard`.
In browse, **Right** opens details; OK/Enter does not open a terminal title.
Show details start on season tabs: Down/OK enters episodes, then OK plays.

The shared mpv panel shows elapsed/remaining/total time, progress, and estimated
finish time when paused. It uses the local timezone and cannot estimate unknown
or live durations. Mouse movement keeps the paused panel visible, with room
below for mpv's standard clickable controls. No global mpv configuration is
installed or modified. Pépé extracts its embedded
script/fonts into the private playback runtime and removes them with that session.
Wrappers must forward the original `--script`, `--script-opts-append`, and font
arguments, as well as authentication and IPC arguments.

Keyboard playback retains mpv's native keys: Space pauses, `j` cycles subtitles,
`g` then `s` opens its subtitle selector, and `q` returns. The shared Pépé interface
also offers **Tab** for subtitle selection (user `input.conf` bindings take
priority). The CEC profile instead binds Blue/`s` to subtitle cycling,
Green/`r` to help, Red/Home to return, and
Yellow/Tab to subtitle selection. In that list, arrows navigate, OK selects, and
Back closes without quitting. Selection includes **Off**, embedded tracks, and
loaded external tracks; it does not search/download new subtitles.

`-help-bar=false` hides the footer and automatic playback guidance, not pause
metadata, controls, or explicitly requested Green-button help. Use
`-mpv-osd=native` to disable the automatic Pépé playback panel for custom setups;
the CEC bindings still apply if that control profile is selected. CEC overrides
are session-local and intentionally take priority over those keys in input.conf.
Arbitrary third-party mpv controllers/layouts may require the native option.
See [Linux CEC setup](packaging/linux/README.md#hdmi-cec-remote) for keymap setup,
keyboard-layout limitations, and the complete remote mapping.

Offline playback UI checks (use the Lua runtime matching your mpv):

```sh
luajit tests/mpv-osd.lua internal/app/mpv/pepe.lua
PEPE_TEST_MPV=1 go test ./internal/app -run TestMPVInterfaceIntegration -count=1
```

The second check uses synthetic video, null video/audio outputs, and local
subtitle fixtures; it does not access Polochon or play actual media.

## Desktop registration from source

This development-only registration is separate from `pepe install`; unregister it
before switching to a copied installation. After building and configuring this
checkout, register its binary in your application menu:

```sh
scripts/install-desktop.sh
```

No sudo, binary copying, or graphical installer is involved. The script downloads
and SHA-256-verifies the hosted icon into your XDG data directory; no icon image
is stored in the repository. A verified cached icon works offline; failed downloads
use a generic icon without preventing registration. `curl` and `sha256sum` are
needed for the download.

Rebuilds take effect immediately. If you move the checkout, run the script again.
Remove only the desktop registration and its verified icon with:

```sh
scripts/install-desktop.sh --uninstall
```

See [Linux setup](packaging/linux/README.md) for configuration, paths,
troubleshooting, and optional Steam Deck instructions.

## Web remote and URL playback

Set a listen address in YAML or explicitly on the command line:

```yaml
web_ui_addr: ":8081"
```

```sh
./pepe -web-ui-addr=:8081
```

Open `http://<pepe-host-address>:8081/` from your phone or browser. `:8081` listens
on all interfaces; use `127.0.0.1:8081` for local-only access. An omitted or empty
address disables the server. Choose **Web remote** in the mode menu (Left from
Home, then Up) to display a large QR code linking directly to the HTTP LAN address
and actual listening port. Scan it with your phone on the same network. If several
network addresses are available, press Right then Up/Down to choose another.
Localhost-only listeners show setup guidance instead of an unusable QR code.
The page keeps your current catalog listing; Escape returns to it.

Explicit CLI values override YAML, including
`-web-ui-addr=` to disable it. An invalid or unavailable address fails startup.

**There is no authentication or TLS. Use only on a trusted network; never expose
this port publicly.** Anyone who can reach it can navigate, play, stop, or quit
Pépé and ask mpv to fetch HTTP/HTTPS resources accessible from the host. Browser
cross-origin checks are not authentication or a network security boundary.

The remote mirrors Pépé's navigation: Right opens details, and OK plays from
details (OK does not open a terminal in browse). Separate playback buttons
pause/resume, seek ±10 seconds, and stop mpv. Stop cancels a pending replacement.

The URL input accepts absolute HTTP/HTTPS URLs, replacing current playback only
after its player has stopped. The latest pending selection wins. Pépé displays
“Now playing from Web UI” without showing the submitted URL. URL playback does
not add the Polochon token, catalog subtitles, or catalog resume positions.
Signed URLs can still be visible in local process arguments; existing user mpv
configuration and wrappers remain under your control.

Direct media and manifest URLs work when supported by your mpv build. Website
URLs depend on your existing mpv/resolver setup; Pépé installs or bundles no
resolver. Normal Polochon configuration and initial library fetch remain required.

HTTP command responses report queuing, not successful playback. Commands run on
the application event loop, including wake-ups while the window is minimized or
covered by fullscreen mpv. Playback controls can briefly be unavailable while mpv
starts. Check the browser status for failures and retry if necessary. The server
stops when Pépé closes; existing app-close playback behavior is unchanged.

## Playback titles

Catalog playback uses `Movie name (2024)` or
`Show name S02E05 - Episode name`, omitting unavailable years or show names.
Pépé passes the title both as mpv's window title and as `force-media-title`.
Scripts, on-screen controls and JSON IPC clients can read the standard
`media-title` property without any Pépé-specific integration. The same
catalog title is used by Pépé's now-playing indicator and web status.

For web URL playback, Pépé leaves mpv's title unset and observes
`force-media-title`, which mpv's yt-dlp hook fills with the resolved video title.
The title updates in both the display and web remote, including during fullscreen
playback. Until a title is available, Pépé shows “Web UI video”; it does not use
mpv's URL/filename fallback. User mpv configuration can still override the title.

## Managed launchers

Ordinary mpv and foreground wrappers need no managed mode. A **managed launcher**
controls a separate playback worker or performs recovery afterward—for example,
restoring the display session after direct-display playback. It must explicitly
confirm that playback has stopped and recovery is complete.

```yaml
mpv_path: /absolute/path/to/launcher
mpv_managed_launcher: true
mpv_startup_timeout: 45s
mpv_stop_timeout: 30s
```

Durations must be positive. The startup timeout only bounds IPC connection attempts;
the stop timeout is the grace period after TERM before killing the launcher.

Missing or malformed confirmation retains the private runtime and blocks new
playback. Retry after the launcher writes a valid confirmation to clear the block.

### Launcher protocol

- Receive `--managed-runtime=DIR` before the original mpv arguments. Forward those
  arguments unchanged, preserving authentication includes, subtitles, resume and IPC.
- Stay in the foreground until the worker stops and recovery finishes, including
  after TERM.
- Then atomically rename a private temporary file to `DIR/launcher-complete`,
  containing exactly `stopped-and-restored\n` (a final newline), and exit with
  the playback result.

Pépé trusts this confirmation. The block is in-memory; the launcher must handle
parent disappearance and recovery across app restarts. App-close behavior is unchanged.
