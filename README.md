![pépé](https://git.quimbo.fr/attachments/d60c99c5-e49b-4931-bc29-4dac2df14488)

# pépé

**pépé** (*Polochon Player* -> “PP” -> pépé, I know that's a stretch) is a Go/Gio player interface for browsing and playing movies and shows from a Polochon server. It is designed for keyboard, remote, and controller navigation, including TVs and Steam Deck.

Features include title-based directional search, recently added listings, movie and show details, subtitle selection, and playback through `mpv`.

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
```

`POLOCHON_URL` and `POLOCHON_TOKEN` override values in the file. `POLOCHON_MPV` overrides `mpv_path`. Keep the config readable only by yourself (for example, `chmod 600 ~/.config/pepe/config.yaml`); playback passes the token to mpv through a private temporary config file, not its process arguments. For example:

```sh
POLOCHON_URL=https://polochon.example \
POLOCHON_TOKEN=your-token \
./pepe
```

Useful flags:

- `-config PATH` — config file
- `-logFile PATH` — log file (default under `$XDG_STATE_HOME/pepe/`, or `~/.local/state/pepe/`)
- `-fullscreen` — start the app and player fullscreen
- `-windowed` — start the app windowed
- `-mpv PATH` — mpv executable
