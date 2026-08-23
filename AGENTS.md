# Repository Guide

## Purpose and Shape

- This is a single root Go `main` package: a Gio interface for browsing and playing Polochon videos.
- The radix tree is core product behavior: it enables fast title search with directional keys and minimal interaction on TVs, remotes, and Steam Deck controls.
- `main.go` owns startup, flags, config loading, and the Gio window. `core.go` owns the Polochon API client, library snapshot, and YAML config. `app.go` is the composition root for the event loop, top-level state, input dispatch, and root layout.
- Keep focused behavior with its owner: `player.go` owns mpv command construction, IPC, and resume tracking; `refresh.go` owns asynchronous library-refresh results; `subtitles.go` owns subtitle popup state and async completion; `format.go` owns display-only formatting helpers.
- Preserve arrow-key/controller navigation and fullscreen playback when changing UI behavior; mouse/desktop use is secondary.
- `actionForKey` is the single mapping for arrow, vim-style, confirm, dismiss, refresh, subtitles, and quit input. Preserve its aliases and modal priority: subtitles, then detail, then browse.
- `listing.go` - Keep listing builders package-private; build movie and show sections separately, merge movie mappings before show mappings, and never mutate shared listing items.
- `MediaItem.Type` uses `MediaKind` constants (`mediaKindMovie` and `mediaKindShow`); do not reintroduce raw type strings in production logic. Use `movieMediaKey` and `episodeMediaKey` for playback/resume identities.
- `APIClient.snapshotFor` - Shallow-copy cached catalog slices under `RLock`, then invoke builders after unlocking; custom snapshots must not replace default client state.
- `APIClient.snapshotFor` cannot fail and returns a `LibrarySnapshot` directly; do not add dead error paths around listing-mode changes.
- `App.browse.library` owns the active `LibrarySnapshot` and listing mode together. Replace its snapshot only through `applyLibrarySnapshot`, and use `sectionRoot` rather than selecting radix roots at call sites.
- Input is normalized to actions in `input.go`. Keep directional browse/detail navigation intact: Right enters a terminal detail view, Left exits it, and Enter does not open browse terminals. Mode-rail Up/Down applies the active listing immediately while retaining the browse location.
- `refreshResult` - A refresh snapshot is valid only when `err` is nil. On failure, retain the displayed catalog, navigation, detail screen, and caches; clear the in-progress flag, log the error, and retain the compact header failure state until retry or success.
- `playerCommand` - Movie and episode launch paths must share `buildPlayerCommand`; unavailable external subtitle URLs remain non-fatal, while a video URL failure prevents launch.
- `asyncCache` - Fetch errors are represented by a resolved zero value, so they are not retried. `drain` is side-effect-only; `onDone` is the UI invalidation signal for both success and failure.
- `RadixNode.Media` - Listing-built terminal nodes carry app-owned identity; string-only `NewRadixTree` callers intentionally retain nil media.
- `MovieCollection.List` / `ShowCollection.List` - Inputs are IMDb-ID sorted; preserve this order because duplicate title-map winners depend on it.
- `DateAdded` fixtures - Set dates through `papi.Movie.Movie.BaseVideo.VideoMetadata` and `papi.Episode.ShowEpisode.BaseVideo.VideoMetadata`; show dates use the newest non-zero nested episode value.
- `NewRadixTree` - A single-item tree can emit the existing root-level incomplete-compression warning; verify behavior with tests rather than treating that log alone as a regression.

## TV Detail UI Readability

- Design detail pages as 10-foot interfaces: keep information density low, make the next action obvious, and optimize for quick scanning rather than desktop-style reading.
- Never place important text directly over uncontrolled poster or fanart pixels. Give titles, metadata, descriptions, lists, and controls a predictable dark surface, local gradient, or sufficiently strong cinematic scrim; preserve unobstructed artwork in areas without text.
- Meet at least WCAG AA contrast against the final composited background: 4.5:1 for normal text and 3:1 for large text. Do not use `colorMuted` for actionable controls or information users must read; reserve it for disabled or nonessential hints.
- Use the pinned proportional Roboto face for titles, labels, prose, episode identifiers, keycaps, and technical values. Differentiate technical text through hierarchy and colour rather than reintroducing a global monospace face. Avoid thin, decorative, or tightly spaced body faces.
- Keep descriptions short, use approximately 1.35–1.5 line spacing, constrain prose to roughly 55–70 characters per line, and clamp summaries where necessary rather than spanning most of the screen.
- Order episode information by user value: episode title and Play action; air date and runtime; synopsis; then file size, quality, codecs, container, and release group. Technical metadata must not visually compete with playback or editorial information.
- On detail pages, render technical metadata as one subdued secondary line after the synopsis; do not restore the previous bright file badges.
- Make the primary playback action visually prominent while preserving Enter/controller activation. Keep focused state unmistakable after the viewer looks away and ensure every action remains reachable by directional input.
- Keep text and focused controls inside the inner 90% TV-safe area. Use generous spacing between focusable elements and avoid important information near display edges.
- For show pages, avoid redundant long-form copy: once episodes are being browsed, prioritize the focused episode synopsis and shorten or omit the show-level synopsis.
- Validate detail-page changes with headless screenshots at representative 1080p and smaller viewports. Inspect bright and dark fanart cases; a layout that is readable on one image is not sufficient.
- The application typography is pinned, generated Roboto loaded with `text.NoSystemFonts()`; do not rely on host fonts. Keep normal, medium, bold, and italic rendering available, retain Noto Sans Symbols and Noto Sans Symbols 2 as fallbacks for remote-control glyphs absent from Roboto, and preserve the verified download workflow in `build.sh`.
- Movie and show full-screen details share contrast-safe primitives but browse previews retain their own scrim. Do not weaken detail reading surfaces or change preview treatment incidentally.
- Keep show detail navigation unchanged: the initial focus is season tabs, Down enters episodes, Enter on tabs enters episodes, and Enter on an episode plays it. Play is prominent and pointer-accessible but is not a separate D-pad focus zone.
- Detail technical metadata is a subdued line after the synopsis, not a row of bright badges. Preserve quality, codecs, container, size, and release group when values are available.
- Keep the existing detail help-bar hints and ordering; maintain readable contrast and TV-safe margins rather than removing or duplicating them.
- Authoritative references: [Android TV typography](https://developer.android.com/design/ui/tv/guides/styles/typography), [Android TV featured carousel/scrim guidance](https://developer.android.com/design/ui/tv/guides/components/featured-carousel), [Amazon Fire TV UX guidelines](https://developer.amazon.com/docs/fire-tv/design-and-user-experience-guidelines.html), and [WCAG contrast guidance](https://www.w3.org/WAI/WCAG22/Understanding/contrast-minimum).

## Setup and Checks

- The module requires Go `1.26.1` and uses the upstream `github.com/odwrtw/polochon` module; a sibling checkout is not required for builds and tests.
- Format changed Go files with `gofmt -w <files>`.
- Run the full test suite with `go test ./...`; run one focused test with `go test -run 'TestName' .`.
- Run `go test -race ./...` after changing asynchronous refresh, cache, subtitle, or player behavior.
- Run `golangci-lint run` for cleanup work. The explicit movie `VideoMetadata.DateAdded` fixture selectors are intentional and locally suppress `staticcheck`'s embedded-field simplification suggestion.
- Run `./build.sh` on a fresh clone before building: it fetches the gitignored embedded Roboto and Noto symbol font files (verifying pinned SHA-256 hashes) required by `go:embed`. Then build the application with `go build .`.
- Tests are local and do not require a running Polochon service; config tests use temporary YAML files and environment variables.
- There is no repository CI, code-generation, or pre-commit configuration to run. Focused tests live beside their subsystem; do not restore the removed legacy integration-test catch-all.

## Headless Gio/Pi UI Tooling

- The reusable headless driver lives in `internal/driver`; the shared application UI is in `internal/app`; the JSONL process is `cmd/gio-control`; the project-local Pi extension is `.pi/extensions/gio-control.ts`.
- Headless rendering on Linux may require software EGL in the container. For tests and the driver, use:
  ```sh
  export EGL_PLATFORM=surfaceless
  export LIBGL_ALWAYS_SOFTWARE=1
  ```
- Run the direct driver tests with `go test ./internal/driver`. Run the complete suite with `go test ./...`; use `go test -race ./...` after asynchronous UI changes.
- Build the command driver with `go build ./cmd/gio-control`. A protocol smoke test is:
  ```sh
  go build -o /tmp/gio-control ./cmd/gio-control
  printf '%s\\n' \\
    '{"id":1,"method":"tree","params":{}}' \\
    '{"id":2,"method":"screenshot","params":{}}' \\
    '{"id":3,"method":"shutdown","params":{}}' |
    /tmp/gio-control
  ```
  The first line must be the JSON `ready` event; diagnostics belong on stderr. Screenshots are written below `.pi/tmp/screenshots/`.
- In Pi, `gio_start` builds and starts the driver. Use `gio_tree`, `gio_click`, `gio_type`, `gio_key`, `gio_resize`, and `gio_screenshot` for interaction. Prefer semantic labels or IDs over coordinates. Use `gio_restart` after changing Gio UI code; it rebuilds the driver and starts a fresh application state.
- Without Polochon configuration, the driver starts a non-networked empty catalog for UI development. To exercise real catalog data, provide the normal config or set `GIO_CONTROL_CONFIG`; `POLOCHON_URL` and `POLOCHON_TOKEN` are also honored.

## Runtime Configuration

- The default config is `$XDG_CONFIG_HOME/polochon-gio/config.yaml`, or the platform equivalent from `os.UserConfigDir()`.
- YAML requires `polochon_url` and `polochon_token`; `POLOCHON_URL` and `POLOCHON_TOKEN` override file values.
- `mpv_path`, `fullscreen`, and `player_fullscreen` are supported; `POLOCHON_MPV` and the `-mpv` flag override the mpv executable.
- Useful flags are `-config`, `-logFile`, `-fullscreen`, and `-windowed`. The default log is under `$XDG_STATE_HOME/polochon-gio/` when available.

## Headless Gio/Pi UI Navigation Patterns

When navigating the Gio UI in Pi with `gio_key`, use only arrow keys and Enter (never `gio_click`). The browse state machine drives all navigation:

### Home → Movie Details (date-added sorted)

```
gio_key LeftArrow      # move focus from content → mode rail
gio_key UpArrow        # cycle mode: Smart list → Last added (date added)
gio_key RightArrow     # move focus from mode rail → content
gio_key RightArrow     # enter Movies section (cursor 0 at root)
gio_key RightArrow     # navigate into first movie (terminal node)
gio_key Return         # open movie detail page
gio_screenshot         # capture the detail screen
```

**Key mechanics:**
- Root has two sections: Movies (cursor 0) and Shows (cursor 1). Up/Down at root toggles between them.
- Mode rail: Up/Down cycles `ListingMode` values — `ListingModeTitle` ("Smart list") and `ListingModeDateAdded` ("Last added"). Up from Title wraps to DateAdded; Down from DateAdded wraps to Title.
- RightArrow from mode rail returns focus to content (does not change mode). RightArrow from root enters the selected section.
- RightArrow on a terminal node (leaf) navigates into it; Enter opens the detail page.
- LeftArrow from content returns to mode rail; from a section root returns to Home; from deeper levels goes back.
- Escape (actionBack) from mode rail returns focus to content; from section root returns to Home; otherwise goes back one level.

### Browse Focus States

| Focus | LeftArrow | RightArrow | Up/Down | Enter |
|---|---|---|---|---|
| `browseFocusContent` (root) | → mode rail | enter section | toggle Movies/Shows | enter section |
| `browseFocusModeRail` | stay | → content | cycle listing mode | → content |
| Section content | go back | go forward | scroll list | open terminal/detail |

### Listing Modes

| Constant | Label | Description |
|---|---|---|
| `ListingModeTitle` | Smart list | Alphabetical by title |
| `ListingModeDateAdded` | Last added | Sorted by date added |

## Steam Deck Packaging

- `scripts/build-steamdeck-dist.sh` builds the binary into ignored `dist/` and assembles the launcher bundle; mpv must exist on the target or be configured with `mpv_path`.
- The packaging script currently refers to missing `packaging/steamdeck/config.example.json`; the actual example and documented format are `config.example.yaml`. Verify or fix this mismatch before relying on the bundle.
