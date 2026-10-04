![pépé](https://git.quimbo.fr/attachments/d60c99c5-e49b-4931-bc29-4dac2df14488)

# pépé

**pépé** (*Polochon Player* -> “PP” -> pépé, I know that's a stretch) is a Go/Gio player interface for browsing and playing movies and shows from a Polochon server. It is designed for keyboard, remote, and controller navigation, including TVs and Steam Deck.

Features include title-based directional search, recently added listings, movie and show details, subtitle selection, and playback through `mpv`. Linux HDMI-CEC remotes are supported through kernel RC passthrough; see [Linux CEC setup](packaging/linux/README.md).

## Requirements

- Go **1.26.1**
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
```

`POLOCHON_URL` and `POLOCHON_TOKEN` override values in the file. `POLOCHON_MPV` overrides `mpv_path`. Keep the config readable only by yourself (for example, `chmod 600 ~/.config/pepe/config.yaml`); playback passes the token to mpv through a private temporary config file, not its process arguments. For example:

```sh
POLOCHON_URL=https://polochon.example \
POLOCHON_TOKEN=your-token \
./pepe
```

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
