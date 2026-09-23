# pépé

**Pépé** (pronounced like the French *Polochon Player* initials “PP”) is a Go/Gio player interface for browsing and playing movies and shows from a Polochon server. It is designed for keyboard, remote, and controller navigation, including TVs and Steam Deck.

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

`POLOCHON_URL` and `POLOCHON_TOKEN` override values in the file. `POLOCHON_MPV` overrides `mpv_path`. Keep the token-bearing config readable only by your user (for example, `chmod 600 ~/.config/pepe/config.yaml`); playback passes the token to mpv through a private temporary config file, not its process arguments. For example:

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

## Pi UI review

The project-local `.pi/extensions/ui-review.ts` delegates headless Gio UI checks to a separate, read-only Pi agent. After `/reload` in an existing Pi session, use `/ui-review-model` to select an authenticated image-capable model (or `/ui-review-model provider/model-id`); the choice persists in the current session and does **not** change the main agent's model. Then ask the main agent to run `ui_review` after its changes, specifying screens, interactions, and expected behavior. For playback screens, the reviewer can use `gio_start` with `mockMpv: true`: this uses an offline two-movie fixture regardless of server configuration, and Play starts a fake process that stays running until replaced. This tests UI and switch lifecycle, not video decoding, authentication, or mpv IPC. The reviewer has only Gio UI tools and reports screenshots under `.pi/tmp/screenshots/ui-review-*/`; aborting the tool stops the reviewer. Configure `GIO_CONTROL_CONFIG` or `POLOCHON_URL`/`POLOCHON_TOKEN` for catalog-dependent checks. Without them, normal mode shows an empty catalog; mock mode uses its offline fixture.
