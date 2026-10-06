![pépé](https://git.quimbo.fr/attachments/d60c99c5-e49b-4931-bc29-4dac2df14488)

# pépé

**pépé** (*Polochon Player* -> “PP” -> pépé, I know that's a stretch) is a Go/Gio player interface for browsing and playing movies and shows from a Polochon server. It is designed for keyboard, remote, and controller navigation, including TVs and Steam Deck.

Features include title-based directional search, recently added listings, movie and show details, subtitle selection, playback through `mpv`, and an opt-in web remote with URL playback. Linux HDMI-CEC remotes are supported through kernel RC passthrough; see [Linux CEC setup](packaging/linux/README.md).

## Requirements

- Go **1.27.1**
- A reachable Polochon server and its URL/token
- `mpv` for playback

## Build and run

```sh
./build.sh
./pepe
```

On a fresh checkout, `build.sh` downloads the pinned Roboto and Noto Sans Symbols font files and verifies their SHA-256 hashes before building. You can also use `go build .` once those embedded font files are present.

The default config path is `$XDG_CONFIG_HOME/pepe/config.yaml`, or the platform config directory's `pepe/config.yaml`. If no platform config directory is available, the fallback is `./pepe.yaml`. Create a YAML file like this:

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
```

`POLOCHON_URL` and `POLOCHON_TOKEN` override values in the file. `POLOCHON_MPV` overrides `mpv_path`. Keep the config readable only by yourself (for example, `chmod 600 ~/.config/pepe/config.yaml`); playback passes the token to mpv through a private temporary config file, not its process arguments. For example:

```sh
POLOCHON_URL=https://polochon.example \
POLOCHON_TOKEN=your-token \
./pepe
```

Artwork redirects must stay on the original origin (scheme, hostname, and port). Redirects to another origin, including HTTPS-to-HTTP downgrades, are rejected to protect the authentication token.

Explicit playback flags override YAML settings, including `-mpv-managed-launcher=false`.

Useful flags:

- `-config PATH` — config file
- `-logFile PATH` — log file (default under `$XDG_STATE_HOME/pepe/`, or `~/.local/state/pepe/`)
- `-fullscreen` — start the app and player fullscreen
- `-windowed` — start the app windowed
- `-mpv PATH` — mpv executable or wrapper
- `-mpv-startup-timeout DURATION` — IPC connection timeout (default `3s`)
- `-mpv-stop-timeout DURATION` — cancellation grace period before killing the launcher (default `2s`)
- `-mpv-managed-launcher` — require external launcher teardown confirmation (default off)
- `-web-ui-addr HOST:PORT` — enable the unauthenticated web remote; empty disables it (default)

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
