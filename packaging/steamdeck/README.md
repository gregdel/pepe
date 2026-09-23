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
   ```

   Edit `polochon_url` and `polochon_token`.

4. Ensure `mpv` is available on the Deck, or set `mpv_path` in the config to a bundled/wrapper executable.

5. In Steam Desktop Mode, add `run-pepe.sh` or `pepe.desktop` as a Non-Steam Game, then launch from Gaming Mode.

Recommended Steam Input mapping:

- D-pad or left stick: arrow keys
- A: Enter
- B: Escape
- X: Tab
- Y: S
- Menu: R
- View: Q
