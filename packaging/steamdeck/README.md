# Steam Deck Setup

This bundle is for a personal Steam Deck install from Desktop Mode.

1. Build the bundle:

   ```sh
   scripts/build-steamdeck-dist.sh
   ```

2. Copy `dist/pepe-steamdeck` to the Deck, for example:

   ```sh
   /home/deck/Applications/pepe
   ```

3. Create the config file:

   ```sh
   mkdir -p "$HOME/.config/pepe"
   cp config.example.yaml "$HOME/.config/pepe/config.yaml"
   chmod 600 "$HOME/.config/pepe/config.yaml"
   ```

   Edit `polochon_url` and `polochon_token`. The config contains the server token, so keep it private; mpv receives it through a private temporary config file rather than its process arguments.

4. Ensure `mpv` is available on the Deck, or set `mpv_path` in the config to a bundled/wrapper executable. Leave managed mode off for ordinary mpv; see [managed launchers](../../README.md#managed-launchers) for worker/recovery wrappers.

5. In Steam Desktop Mode, add `run-pepe.sh` or `pepe.desktop` as a Non-Steam Game, then launch from Gaming Mode.

Optionally set `web_ui_addr: ":8081"` to use the [web remote](../../README.md#web-remote-and-url-playback) from a phone at `http://<deck-address>:8081/`. It has no authentication: use only on trusted networks and never expose the port publicly. Leave the address empty to disable it.

Recommended Steam Input mapping:

- D-pad or left stick: arrow keys
- A: Enter
- B: Escape
- X: Tab
- Y: S
- Menu: R
- View: Q
