# pépé design

pépé is a 10-foot interface: a TV across the room, a Steam Deck in hand, a
remote or a D-pad, rarely a mouse. Every screen should feel like part of one
system: one accent, one focus indicator that moves the same way everywhere,
artwork treated the same way on every page.

The mpv OSD and the web remote take their colours from the same tokens (see
Other surfaces), so a palette change in `theme.go` reaches every screen.

The tokens live in `internal/app/theme.go` and the motion helpers in
`internal/app/motion.go`; this document is the intent behind them. When code
and this document disagree, fix one of them.

## Principles

1. **Focus is always obvious.** From the couch the user must see where focus
   is at a glance and see it move. There is exactly one focused element.
2. **Artwork is the decoration.** The chrome is near-black and quiet so posters
   and fanart carry the colour; the UI adds a single accent.
3. **Readable over anything.** Text never sits directly on uncontrolled
   artwork. It gets a surface, a scrim or the veil, and keeps WCAG AA
   contrast over both white and black artwork.
4. **Low density, large type.** Fewer, bigger things. A screen should be
   understood at 3 m.
5. **Motion explains, never delays.** Motion shows where focus went and what
   changed. Input always applies at once; drawing catches up.

## Colour

Dark, near-black surfaces ("AMOLED black") with one violet accent.

| Token | Role |
| --- | --- |
| `colorBg` | Window background. |
| `colorSurface` | Panels (playing bar). |
| `colorSurface2` | Cards and popups; also the **current** (not focused) item, e.g. the active rail entry or the current season. |
| `colorOutline` | Hairlines and dividers only. |
| `colorTrack` | The unfilled part of a progress bar: 3:1 on `colorSurface`, since it carries the duration. |
| `colorFg` | Primary text. |
| `colorSubtle` | Secondary text: metadata, counts, descriptions. |
| `colorMuted` | Disabled or nonessential hints only; never information the user needs. |
| `colorPrimary` | The accent as text or icon: the header's listing name, radix prefixes, the current item's label or icon, episode codes, "Now playing", "Refreshing…". |
| `colorSelectBg` / `colorSelectFg` | The **focus** fill and the text on it. Nothing else uses this fill. |
| `colorError` | Errors and failure states (and log errors). |
| `colorWarn` | Warnings (log warnings). |
| `colorDetailPanel` | The detail reading surface: the least alpha that keeps detail text at AA over white artwork. |

Rules:

- The accent appears in only two forms: **focus fill** (`colorSelectBg`) and
  **accent text** (`colorPrimary`). Do not add new accent hues or tints.
- The only other hues are signals, never decoration: `colorError` and
  `colorWarn` for status, and the four remote colour-key swatches
  (`colorRemote*`) that match the physical remote's buttons
  in the HDMI-CEC help bar.
- The only translucent white is a count pill on a focused row.
- Focused text is `colorSelectFg` on the focus fill, bold where it is a label.
- Check contrast as it will be composited: Gio blends in linear light, so a
  translucent layer over artwork reads differently than in sRGB tools.
  `theme_test.go` and the detail/backdrop contrast tests guard the tokens.

## Typography

Roboto only, embedded (`text.NoSystemFonts()`), in normal, medium, bold and
italic. Arrows, play and other symbols are Material icons, not glyphs (Roboto
has no fallback).

| Token | Size | Use |
| --- | --- | --- |
| `spTitle` | 46 | Detail page title. |
| `spBrowse` | 32 | Catalog rows, episode title in its info block. |
| `spHero` | 30 | Header wordmark (current listing). |
| `spBody` | 28 | Synopsis, settings rows, multi-line text. |
| `sp` / `spMeta` | 24 | Controls, list rows in popups and detail pages, metadata lines. |
| `spSmall` | 24 | Keycaps and essential hints. |
| `spTiny` | 22 | Count pills, secondary row metadata. Never smaller. |

Long text is clamped to about 55–70 characters per line with 1.35–1.5 line
spacing (`proseWidth`).

Weight shows hierarchy before size does: focused labels and titles are bold,
section and status labels medium, body and metadata regular.

Copy is sentence case. It uses a real ellipsis ("Refreshing…") and joins
metadata with " · ". Status lines are short and say what is happening
("Server unreachable · retrying"), not why.

## Space and shape

- **Spacing:** the scale is `dp2`…`dp48`. Screens keep a 24 dp outer inset
  (`viewportInsets`), scaled with the UI, with no extra overscan border.
- **Scaling:** the whole UI scales from a 1920 dp reference width
  (`scaleContext`), so layouts are designed once at 1080p.
- **Radii:**
  - `radiusRow` (10) for every focusable row, pill and button;
  - `radiusThumb` (6) for row thumbnails;
  - `radiusPoster` (10) for detail posters;
  - `radiusCard` (16) for popups and cards.
  - Keycaps (6) have their own radius.

## Screens

The layouts, as a map for changing them.

- **Catalog** (home). The left column holds three things:
  - The header: the listing name (`spHero`, `colorPrimary`), a hairline, the
    section and typed prefix, and the refresh status and library counts on
    the right (`spTiny`).
  - The mode rail: 80 dp of icons, which expands to 240 dp with labels only
    while it has focus.
  - The radix list.

  The preview sits right of the column, with its text at the bottom over the
  scrim.
- **Movie titles:** a movie is shown under its original title. When its
  localized title differs, the preview and detail page add it as one `spMeta`
  line under the title (`colorFg` over the preview scrim, `colorSubtle` on the
  detail panel); catalog rows show only the original title, and the Smart list
  also lists the localized title as an alias. The TV shows only what Roboto
  can draw: an original title in another script (CJK, Arabic, Thai…) gives way
  to the localized title alone. The web search, drawn with the phone's fonts,
  always shows the original title.
- **Rail pages** (Settings, Logs, Web remote) replace the list and preview.
  They sit on `colorBg`, with no backdrop.
- **Detail pages:**
  - Movie: poster (280 dp) and a reading column of `proseWidth`. The title,
    then metadata, watched mark, Play and synopsis.
  - Show: poster (240 dp) and a wider reading column (1300 dp), with season
    tabs above the episode list and the focused episode's info block.
- **Playing bar:** while mpv runs, a `colorSurface` band across the top with
  a hairline under it. The status is in `colorPrimary` bold and the title in
  `colorFg`: "Now playing" and the playing title, "Starting…" and the next
  title while a switch stops the current one, or "Stopping playback". It is
  the only global player status; detail pages show only how playback
  relates to their own title, never player internals.
- **Help bar:** always at the bottom, on `colorBg`. It stays readable above a
  popup's dimmer.

## Artwork and surfaces

- **Catalog:** the selected title's fanart is drawn full-bleed behind the
  catalog.
  - The left column (header, rail, list) sits on an opaque veil made from a
    blurred, darkened copy of the same artwork. Sharp fanart shows only while
    a title is selected, behind a scrim under the preview text.
  - Text stays left of the veil edge (`catalogListEdge`).
  - The veil is not flat black: it is a dim, coloured glow of the artwork
    (capped at `ambientMaxLuminance`). It gets darker away from the sharp
    image, so the chrome takes on each title's colour.
  - The preview scrim is an even band under the text, eased in over 200 dp
    above it. The fanart above the band is left untouched.
- **Detail pages:** detail pages use `colorDetailPanel` behind the text,
  feathered into the fanart. Darken only where text sits, and only as much
  as AA needs.
- **Gradients** (scrims, feathers, the catalog focus fade) must not show an
  edge at either end. They ease in and out instead of using a linear ramp.
  They are drawn as segments whose alphas are solved for linear-light
  blending, which also avoids banding on TVs.
- **Posters and fanart:** posters always have rounded corners. A missing
  poster shows a placeholder surface, not a broken box.

## Focus

One focus model everywhere: catalog, rail, season tabs, episodes, popups,
settings, buttons.

| State | Look |
| --- | --- |
| **Focused** | The focus fill (`colorSelectBg`, `radiusRow`) behind the item, `colorSelectFg` bold text, and the item lifted from its centre together with its fill (`focusLift`). No outline ring. |
| **Current, not focused** | `colorSurface2` fill; label or icon in `colorPrimary`. E.g. the active listing on the rail while the catalog has focus, the current season while episodes have focus. |
| **Editing** | A text field being edited: `colorSurface2` with a 2 dp white ring, so the caret and selection stay visible. This is the only place the ring is used. |
| **Rest** | No fill. |

- **The lift** grows an item by 3%, but never more than the fill's 8 dp
  inset on each side (`focusLiftReach`). A wide row lifts less, stays inside
  its list, and its content never leaves the fill (rows pad content by more
  than the lift).
- **In the catalog**, the focus fill fades out to the right into the backdrop
  instead of ending in a hard edge, and it does not scale with the lift:
  scaling its gradient segments shows seams. Everywhere else the fill is a
  solid rounded shape.
- **Lists draw the fill.** In every list (`focusList`; the rail through
  `focusListRow`), the list paints the focus fill, not the row, so the fill
  can glide. Rows use `focusRowShell`; a new focusable list uses the same
  pair rather than its own fill.
- The focused node is marked semantically (`markSelected`). Headless tests
  and the driver find focus that way, so every focusable item must do it.

## Motion

Motion follows Material 3 for curves and durations and Android TV/tvOS for
focus. It is always on, with no setting.

**Focus motion (springs).**
- Focus moves on critically damped springs (`spring`, about 300 ms response).
  A new press while moving keeps the velocity, so a held D-pad gives one
  continuous glide.
- Every focus list uses `listMotion`, so all lists move the same way:
  - **Scrolling lists keep focus fixed.** When the list scrolls, it glides
    under a focus fill that stays put. The fill itself glides between items
    only where the list cannot scroll, or the item is already visible.
  - **The focused item lifts** (see Focus) and the previous one settles
    back.
  - **Focus entering a list** (rail ↔ catalog, tabs ↔ episodes) appears in
    place.
  - **The wheel** can still scroll a list. The list then rests where the
    wheel left it, and the next focus move glides from there.
  - **The lag is capped.** The drawing never trails the cursor by more than
    1.5 items.

**Transitions in place (curves).** Image cross-fades and other in-place
changes use Material's standard easing (`easeStandard`) at medium durations:

| Transition | Duration |
| --- | --- |
| Catalog backdrop cross-fade | 300 ms |
| Detail artwork fade | 250 ms |

Emphasized-decelerate is for things that *enter* the screen. On a fade it
pops, because Gio blends in linear light.

**Rules.**
- Input is never blocked, queued or slowed.
- Snap instead of animating when the content changes rather than the focus:
  a new node, listing, season, refresh or resize.
- Animate only offset, opacity and scale. Never animate text size, wrapping or
  layout width frame by frame.
- Text keeps its contrast through the whole motion: its surface or scrim is at
  full strength from the first frame.
- Idle costs nothing: no frame is requested once motion settles.

## Components

- **Rows** (`focusRowShell`): at least 64 dp tall (`browseRowHeight`).
  Content is inset 12 dp horizontally and the focus fill is inset 8 dp from
  the list edges.
- **Pills and buttons:** 16 × 8 dp padding, `radiusRow`. At rest a button
  has no fill, like any row. The primary action
  (Play) is always the focus fill. On a movie detail page it is the only
  focus target.
- **Catalog rows:**
  - Group rows: the label, a count pill and a `›`.
  - Title rows: a small poster thumbnail and the title.
  - In both, the letters the user has already typed (the radix path) are in
    `colorFg` and the rest of the label in `colorPrimary`, so the next
    choice stands out. A focused row is all `colorSelectFg`.
  - The preview of a group lists its children; past 14 it ends with
    "… and N more" in `colorMuted`.
- **Count pills:** `spTiny` `colorMuted` text on `colorSurface2`; on a
  focused row, white text on translucent white.
- **Keycaps:** `spSmall` `colorFg` text on `colorSurface2` with a
  `colorOutline` stroke. In HDMI-CEC mode, the hints read OK/Back and the
  colour actions show as swatches, in the remote's red, green, yellow, blue
  order.
- **Watch state:** a check icon in `colorSubtle`. Detail pages add the label
  "Watched"; episode rows show only the icon. The resume position gets its
  own `colorFg` line in the episode info block, since Enter resumes from it.
- **Now playing:** on the title mpv is playing, Enter does nothing and the
  help bar drops its play hint. The movie's Play button reads "Now playing";
  the episode row shows a play icon in `colorPrimary` instead of the check,
  and the info block's resume line becomes "Now playing" with the icon.
- **Playback failure:** only on the failed title's detail page, right under
  its Play button (movies) or in place of the resume line (episodes): a
  short reason in `colorError` and "Details in Logs" in `colorSubtle`. Raw
  errors can carry private URLs and paths, so they stay in the log.
- **Empty, loading and error states:** a single plain line ("No episodes",
  "Searching…") in `colorFg` or `colorSubtle`; errors in `colorError`. No
  spinners, so idle stays free.
- **Help bar:** keycaps (`spSmall`) with their labels, in a fixed order per
  screen. Keep the hints accurate when keys change.
- **Popups:** a dimmed screen (black, about 70%) with a `radiusCard` card on
  `colorSurface2`. Popups are modal and own all input.

## Other surfaces

Every colour below comes from `theme.go`. Do not write colour values into
these files.

- **mpv OSD** (`internal/app/mpv/pepe.lua`): what the user sees while
  paused. pépé passes `osdColors` as script options, in ASS's BGR hex
  (`assColor`), and the panel is drawn in Roboto from `--osd-fonts-dir`. The
  panel is `colorSurface` with `colorFg` text and `colorSubtle` labels. The
  accent (`colorPrimary`) marks PAUSED, the time left, the finish time and
  the progress fill. The progress track is `colorTrack`. The script's
  defaults only mirror the theme so it works on its own.
- **Web remote** (`internal/app/web/`): phone-first. `themeCSS` serves the
  colour and radius tokens ahead of `remote.css`, which derives pressed,
  hover and glow shades with `color-mix` (falling back to plain tokens where
  it is unsupported) and has no literal colours except shadow black. Primary buttons are the focus fill, accent text and focus
  outlines are `colorPrimary`, and a matched or current chip uses the
  current-item look (`surface-2` with `colorPrimary`).
  The playback progress bar matches the OSD's: a `colorPrimary` fill on an
  `outline-strong` track with a `colorFg` thumb.
  Search is a full-screen view behind the top search bar, on plain `bg`
  without the page glow, which the opaque sticky bar would cut. Its filter chips
  (All, Movies, Shows) use the current-item look for the checked one. A result
  row is a `surface` card: a 2:3 poster on a `surface-2` placeholder showing
  a film or TV glyph in `muted` until the image loads (or for good if it fails),
  the title (two lines at most), the localized title of a movie on one
  `subtle` line when it differs, then a type badge (glyph in `colorPrimary`
  and the word Movie or Show, never colour alone) and the year in `subtle`.
  Movies add a separate play button in `colorPrimary`; the row itself opens
  the title on the TV.
- **QR code** (Web remote page): black on white for scanning, the one
  intentionally bright surface in the UI.

## References

- [Android TV design: typography](https://developer.android.com/design/ui/tv/guides/styles/typography),
  [color on TV](https://developer.android.com/design/ui/tv/guides/foundations/color-on-tv),
  [featured carousel scrims](https://developer.android.com/design/ui/tv/guides/components/featured-carousel)
- [Material 3 motion: easing and duration](https://m3.material.io/styles/motion/easing-and-duration/tokens-specs)
- [Apple HIG: focus and selection (tvOS)](https://developer.apple.com/design/human-interface-guidelines/focus-and-selection)
- [Fire TV design guidelines](https://developer.amazon.com/docs/fire-tv/design-and-user-experience-guidelines.html)
- [WCAG contrast minimum](https://www.w3.org/WAI/WCAG22/Understanding/contrast-minimum),
  [G18: contrast over images](https://www.w3.org/WAI/WCAG22/Techniques/general/G18)
