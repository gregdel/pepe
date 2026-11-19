package app

import (
	"fmt"
	"image"
	"strings"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// ── Preview panel (right side of the browse screen) ───────────────────────────

func (a *App) layoutPreview(gtx C) D {
	if a.browse.nav.isAtRoot() {
		return a.previewRoot(gtx)
	}

	child := a.browse.nav.getSelectedChild()
	if child == nil {
		return a.previewMessage(gtx, "No item selected")
	}

	if child.IsTerminal && child.SubtreeCount == 1 {
		media := a.mediaForNode(child)
		if media == nil {
			return a.previewMessage(gtx, "Media details not found")
		}
		return a.previewTerminal(gtx, media)
	}

	return a.previewGroup(gtx, child)
}

func (a *App) previewMessage(gtx C, msg string) D {
	return layout.UniformInset(dp32).Layout(gtx, func(gtx C) D {
		return a.label(gtx, sp, msg, colorFg, 0)
	})
}

// ── Root: section hero ────────────────────────────────────────────────────────

func (a *App) previewRoot(gtx C) D {
	name, count := "Movies", a.browse.library.movieCount
	if a.browse.nav.cursor == 1 {
		name, count = "Shows", a.browse.library.showCount
	}

	return layout.UniformInset(dp32).Layout(gtx, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return a.label(gtx, spTitle, name, colorFg, font.Bold)
			}),
			layout.Rigid(layout.Spacer{Height: dp8}.Layout),
			layout.Rigid(func(gtx C) D {
				return a.label(gtx, spBody, fmt.Sprintf("%d items in the library", count), colorSubtle, 0)
			}),
		)
	})
}

// ── Prefix group ──────────────────────────────────────────────────────────────

func (a *App) previewGroup(gtx C, node *RadixNode) D {
	currentPath := a.browse.nav.getCurrentPath()
	group := node.Label
	if currentPath != "" {
		group = currentPath + node.Label
	}

	return layout.UniformInset(dp24).Layout(gtx, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx C) D {
						if currentPath != "" && strings.HasPrefix(group, currentPath) {
							return a.twoTone(gtx, spTitle, currentPath, group[len(currentPath):], colorFg, colorPrimary)
						}
						return a.label(gtx, spTitle, group, colorFg, font.Bold)
					}),
					layout.Rigid(func(gtx C) D {
						return a.countPill(gtx, node.SubtreeCount, false)
					}),
				)
			}),
			layout.Rigid(layout.Spacer{Height: dp16}.Layout),
			// Sample of the group's content
			layout.Flexed(1, func(gtx C) D {
				maxShow := 14
				var lines []labelLine
				for i, child := range node.Children {
					if i >= maxShow {
						lines = append(lines, labelLine{
							text:  fmt.Sprintf("… and %d more", len(node.Children)-maxShow),
							color: colorMuted, size: spBrowse,
						})
						break
					}
					var label string
					if child.IsTerminal && child.FullString != "" {
						label = child.FullString
					} else {
						label = group + child.Label + "  ›"
					}
					lines = append(lines, labelLine{text: label, color: colorFg, size: spBrowse})
				}
				return a.renderLines(gtx, &a.rightList, lines)
			}),
		)
	})
}

// ── Terminal: movie/show teaser ───────────────────────────────────────────────

func (a *App) previewTerminal(gtx C, media *MediaItem) D {
	if media.Type == mediaKindShow {
		return a.previewShow(gtx, media)
	}
	return a.previewMovie(gtx, media)
}

// previewShell draws fanart + scrim behind bottom-left anchored content. The
// content is measured first so the scrim protects exactly the text block.
func (a *App) previewShell(gtx C, fanart layout.Widget, content layout.Widget) D {
	size := gtx.Constraints.Max
	inset := gtx.Dp(dp32)
	cgtx := gtx
	cgtx.Constraints = layout.Constraints{Max: image.Pt(max(size.X-2*inset, 0), max(size.Y-2*inset, 0))}
	macro := op.Record(gtx.Ops)
	dims := content(cgtx)
	call := macro.Stop()
	top := size.Y - inset - dims.Size.Y

	fgtx := gtx
	fgtx.Constraints = layout.Exact(size)
	fanart(fgtx)
	scrim(gtx, top)
	defer op.Offset(image.Pt(inset, top)).Push(gtx.Ops).Pop()
	call.Add(gtx.Ops)
	return D{Size: size}
}

func (a *App) previewMovie(gtx C, media *MediaItem) D {
	movie, ok := a.client.getMovie(media.ID)
	if !ok {
		return a.previewMessage(gtx, "Movie details not available")
	}
	movieID := movie.ImdbID
	id := media.ID

	return a.previewShell(gtx, func(gtx C) D {
		return a.movieFanart(gtx, movieID)
	}, func(gtx C) D {
		meta := buildMetaLine(movie.Year, movie.Runtime, formatQualityBadge(movie.Quality), float64(movie.Rating), movie.Genres)
		return a.previewMediaContent(gtx, media, meta, func(gtx C) D {
			return a.movieThumb(gtx, id, unit.Dp(140), radiusPoster)
		})
	})
}

func (a *App) previewShow(gtx C, media *MediaItem) D {
	show, ok := a.client.getShow(media.ID)
	if !ok {
		return a.previewMessage(gtx, "Show details not available")
	}
	showID := show.ImdbID

	return a.previewShell(gtx, func(gtx C) D {
		return a.showFanart(gtx, showID)
	}, func(gtx C) D {
		return a.previewMediaContent(gtx, media, buildShowSummary(show), func(gtx C) D {
			return a.showPoster(gtx, showID, unit.Dp(140), radiusPoster)
		})
	})
}

// previewMediaContent lays out the shared poster, title, and metadata.
func (a *App) previewMediaContent(gtx C, media *MediaItem, meta string, poster layout.Widget) D {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.End}.Layout(gtx,
		layout.Rigid(poster),
		layout.Rigid(layout.Spacer{Width: dp24}.Layout),
		layout.Flexed(1, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					return a.body(gtx, spTitle, media.Title, colorFg, 2)
				}),
				layout.Rigid(layout.Spacer{Height: dp8}.Layout),
				layout.Rigid(func(gtx C) D {
					if meta == "" {
						return D{}
					}
					return a.body(gtx, spMeta, meta, colorFg, 2)
				}),
			)
		}),
	)
}
