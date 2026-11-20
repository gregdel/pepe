![pépé](https://git.quimbo.fr/attachments/d60c99c5-e49b-4931-bc29-4dac2df14488)

# pépé

**pépé** (*Polochon Player* → “PP” → pépé, I know that's a stretch) is a player
for movies and shows from a [polochon](https://github.com/odwrtw/polochon) media server.
It runs on the desktop, on a TV (controlled with its own remote over HDMI-CEC, or with a
keyboard or controller), and on the Steam Deck. Playback goes through mpv.

You need a polochon server URL/token and mpv **0.41+ with Lua**.
pépé is **not a static binary**: [Gio's native dependencies](https://gioui.org/doc/install)
are required, even to run `pepe install` or `pepe uninstall`.

## Install

Copy the native `pepe` binary to a temporary location (`~/Downloads`, `/tmp`, etc.).
Run these commands from the directory containing `pepe`:

```sh
chmod +x ./pepe && ./pepe install
```

This installs pépé and its menu entry, preserving existing config.

## Configure

Edit `~/.config/pepe/config.yaml` with your server details:

```yaml
polochon_url: https://polochon.example
polochon_token: your-token
```

For the optional Steam Deck keepawake workaround (`deck` user), also set:

```yaml
mpv_path: "/home/deck/.local/share/pepe/mpv-keepawake.sh"
fullscreen: true
```

This experimental wrapper needs the per-user mpv Flatpak and host input tools;
read its [requirements and limitations](packaging/linux/README.md#opt-in-input-workaround).
It is not enabled automatically. Otherwise, keep the installed `mpv_path`.

## Build from source

```sh
./build.sh
```

## More documentation

See `./pepe -help` and the config comments for available options.
[Linux setup](packaging/linux/README.md) covers upgrades, uninstall, Steam Deck,
HDMI-CEC, and non-obvious integration caveats.
