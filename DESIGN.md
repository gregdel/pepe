# pépé design

pépé is a 10-foot interface: a TV across the room, a Steam Deck in hand, a
remote or a D-pad, rarely a mouse. Every screen should feel like part of one
system: one accent, one focus indicator that moves the same way everywhere,
artwork treated the same way on every page.

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
| `colorFg` | Primary text. |
| `colorSubtle` | Secondary text: metadata, counts, descriptions. |
| `colorMuted` | Disabled or nonessential hints only; never information the user needs. |
| `colorPrimary` | The accent as text or icon: wordmark, radix prefixes, the current item's label or icon, episode codes. |
| `colorSelectBg` / `colorSelectFg` | The **focus** fill and the text on it. Nothing else uses this fill. |
| `colorTerminal` | Errors and failure states (and log errors). |
| `colorWarn` | Warnings (log warnings). |
| `colorDetailPanel` | The detail reading surface: the least alpha that keeps detail text at AA over white artwork. |

Rules:

- The accent appears in only two forms: **focus fill** (`colorSelectBg`) and
  **accent text** (`colorPrimary`). Do not add new accent hues or tints.
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

## Artwork and surfaces

- **Catalog:** the selected title's fanart is drawn full-bleed behind the
  catalog.
  - The left column (header, rail, list) sits on an opaque veil made from a
    blurred, darkened copy of the same artwork. Sharp fanart shows only while
    a title is selected, behind a scrim under the preview text.
  - Text stays left of the veil edge (`catalogListEdge`).
- **Detail pages:** detail pages use `colorDetailPanel` behind the text,
  feathered into the fanart. Darken only where text sits, and only as much
  as AA needs.
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
- **Pills and buttons:** 16 × 8 dp padding, `radiusRow`. The primary action
  (Play) is always the focus fill. On a movie detail page it is the only
  focus target.
- **Count pills:** `spTiny` on `colorSurface2`; on a focused row, translucent
  white.
- **Help bar:** keycaps (`spSmall`) with their labels, in a fixed order per
  screen. Keep the hints accurate when keys change.
- **Popups:** a dimmed screen (black, about 70%) with a `radiusCard` card on
  `colorSurface2`. Popups are modal and own all input.

## References

- [Android TV design: typography](https://developer.android.com/design/ui/tv/guides/styles/typography),
  [color on TV](https://developer.android.com/design/ui/tv/guides/foundations/color-on-tv),
  [featured carousel scrims](https://developer.android.com/design/ui/tv/guides/components/featured-carousel)
- [Material 3 motion: easing and duration](https://m3.material.io/styles/motion/easing-and-duration/tokens-specs)
- [Apple HIG: focus and selection (tvOS)](https://developer.apple.com/design/human-interface-guidelines/focus-and-selection)
- [Fire TV design guidelines](https://developer.amazon.com/docs/fire-tv/design-and-user-experience-guidelines.html)
- [WCAG contrast minimum](https://www.w3.org/WAI/WCAG22/Understanding/contrast-minimum),
  [G18: contrast over images](https://www.w3.org/WAI/WCAG22/Techniques/general/G18)
