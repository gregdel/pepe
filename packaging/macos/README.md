# macOS

Requires macOS 13 or later and mpv 0.41+ with Lua (`brew install mpv`).

1. Download from the [latest release](https://github.com/gregdel/pepe/releases/latest):
   `pepe-…-macos-arm64.dmg` (Apple Silicon) or `pepe-…-macos-amd64.dmg` (Intel).
2. Open the `.dmg` and drag **pepe** onto **Applications**.
3. Open pepe. The app is not notarized, so macOS blocks the first launch: go to
   System Settings → Privacy & Security and click **Open Anyway**, or run
   `xattr -dr com.apple.quarantine /Applications/pepe.app`.
4. Set `polochon_url` and `polochon_token` in
   `~/Library/Application Support/pepe/config.yaml` (created on first launch),
   then open pepe again.

To uninstall, move `/Applications/pepe.app` to the Trash.
