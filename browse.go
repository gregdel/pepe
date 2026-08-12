package main

import (
	"fmt"
	"image"
	"strings"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"github.com/odwrtw/polochon/lib/papi"
)

// ── Browse screen ─────────────────────────────────────────────────────────────
//
// Header (wordmark + radix query + counts) on top, nav list on the left,
// contextual preview on the right. This is the home of the radix tree search.

func (a *App) layoutBrowse(gtx C) D {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(a.layoutHeader),
		layout.Rigid(hairline),
		layout.Flexed(1, func(gtx C) D {
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Flexed(navFlex, a.layoutNavPanel),
				layout.Rigid(func(gtx C) D {
					size := image.Point{X: gtx.Dp(1), Y: gtx.Constraints.Max.Y}
					defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
					paint.Fill(gtx.Ops, colorOutline)
					return D{Size: size}
				}),
				layout.Flexed(previewFlex, a.layoutPreview),
			)
		}),
	)
}

// ── Header ────────────────────────────────────────────────────────────────────

func (a *App) layoutHeader(gtx C) D {
	return layout.Inset{Left: dp24, Right: dp24, Top: dp12, Bottom: dp12}.Layout(gtx, func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			// Wordmark
			layout.Rigid(func(gtx C) D {
				return a.label(gtx, spHero, "Polochon", colorPrimary, font.Bold)
			}),
			layout.Rigid(layout.Spacer{Width: dp24}.Layout),
			// Breadcrumb / radix query
			layout.Rigid(func(gtx C) D {
				return a.layoutBreadcrumb(gtx)
			}),
			layout.Flexed(1, func(gtx C) D { return D{Size: image.Pt(gtx.Constraints.Max.X, 0)} }),
			// Status + counts
			layout.Rigid(func(gtx C) D {
				return a.layoutHeaderStatus(gtx)
			}),
		)
	})
}

// layoutBreadcrumb renders "Movies · int|" — the current radix prefix shown as
// a search query, making the tree drill-down feel like typing.
func (a *App) layoutBreadcrumb(gtx C) D {
	if a.nav.isAtRoot() {
		return a.label(gtx, spMeta, "Home", colorSubtle, 0)
	}

	section := "Movies"
	if a.nav.section == SectionShows {
		section = "Shows"
	}
	prefix := a.nav.getCurrentPath()

	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			return a.label(gtx, spMeta, section, colorFg, font.Medium)
		}),
		layout.Rigid(func(gtx C) D {
			if prefix == "" {
				return D{}
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					return a.label(gtx, spMeta, "  ·  ", colorMuted, 0)
				}),
				layout.Rigid(func(gtx C) D {
					return a.label(gtx, spMeta, prefix, colorPrimary, font.Bold)
				}),
				layout.Rigid(func(gtx C) D {
					// Caret block after the query.
					size := image.Pt(gtx.Dp(dp2), gtx.Dp(dp24))
					return layout.Inset{Left: dp4}.Layout(gtx, func(gtx C) D {
						defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
						paint.Fill(gtx.Ops, colorPrimary)
						return D{Size: size}
					})
				}),
			)
		}),
	)
}

func (a *App) layoutHeaderStatus(gtx C) D {
	var parts []layout.FlexChild

	if a.refreshing {
		parts = append(parts, layout.Rigid(func(gtx C) D {
			return a.label(gtx, spTiny, "Refreshing…", colorPrimary, font.Medium)
		}))
		parts = append(parts, layout.Rigid(layout.Spacer{Width: dp16}.Layout))
	}
	if a.playerStatus == PlayerStatusRunning {
		parts = append(parts, layout.Rigid(func(gtx C) D {
			return a.label(gtx, spTiny, "Now playing", colorTerminal, font.Medium)
		}))
		parts = append(parts, layout.Rigid(layout.Spacer{Width: dp16}.Layout))
	}
	parts = append(parts, layout.Rigid(func(gtx C) D {
		return a.label(gtx, spTiny,
			fmt.Sprintf("%d movies · %d shows", a.movieCount, a.showCount),
			colorMuted, 0)
	}))

	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, parts...)
}

// ── Nav panel ─────────────────────────────────────────────────────────────────

func (a *App) layoutNavPanel(gtx C) D {
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx C) D {
			defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
			paint.Fill(gtx.Ops, colorSurface)
			return D{Size: gtx.Constraints.Max}
		}),
		layout.Stacked(a.layoutNavList),
	)
}

func (a *App) layoutNavList(gtx C) D {
	// Keep cursor centered in the visible area (like scrolloff=999).
	if pos := a.leftList.Position; pos.Count > 0 {
		target := max(0, a.nav.cursor-pos.Count/2)
		if pos.First != target {
			a.leftList.ScrollTo(target)
		}
	}

	if a.nav.isAtRoot() {
		items := []struct {
			name  string
			count int
		}{
			{"Movies", a.movieCount},
			{"Shows", a.showCount},
		}
		return a.leftList.Layout(gtx, len(items), func(gtx C, i int) D {
			return a.groupRow(gtx, "", items[i].name, items[i].count, i == a.nav.cursor)
		})
	}

	current := a.nav.getCurrentNode()
	if current == nil {
		return D{}
	}
	currentPath := a.nav.getCurrentPath()
	children := current.Children
	return a.leftList.Layout(gtx, len(children), func(gtx C, i int) D {
		node := children[i]
		selected := i == a.nav.cursor

		if node.IsTerminal && node.SubtreeCount == 1 {
			return a.terminalRow(gtx, node.FullString, currentPath, selected)
		}

		label := node.Label
		if currentPath != "" {
			label = currentPath + node.Label
		}
		return a.groupRow(gtx, currentPath, label, node.SubtreeCount, selected)
	})
}

// navRowShell draws the selection pill behind a row's content.
func (a *App) navRowShell(gtx C, selected bool, minHeight unit.Dp, content layout.Widget) D {
	return layout.Inset{Left: dp8, Right: dp8, Top: dp2, Bottom: dp2}.Layout(gtx, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		if h := gtx.Dp(minHeight); gtx.Constraints.Min.Y < h {
			gtx.Constraints.Min.Y = h
		}
		macro := op.Record(gtx.Ops)
		dims := layout.Inset{Left: dp12, Right: dp12, Top: dp6, Bottom: dp6}.Layout(gtx, content)
		call := macro.Stop()
		if selected {
			fillRounded(gtx, dims.Size, radiusRow, colorSelectBg)
		}
		call.Add(gtx.Ops)
		return dims
	})
}

// groupRow renders a radix prefix group: two-tone label + count pill.
func (a *App) groupRow(gtx C, prefix, label string, count int, selected bool) D {
	return a.navRowShell(gtx, selected, 0, func(gtx C) D {
		fg := colorFg
		prefixCol := colorPrimary
		if selected {
			fg = colorSelectFg
			prefixCol = colorSelectFg
		}
		rest := label
		if prefix != "" && strings.HasPrefix(label, prefix) {
			rest = label[len(prefix):]
		} else {
			prefix = ""
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx C) D {
				return a.twoTone(gtx, sp, prefix, rest, fg, prefixCol)
			}),
			layout.Rigid(func(gtx C) D {
				return a.countPill(gtx, count, selected)
			}),
		)
	})
}

// terminalRow renders a playable movie/show with a poster thumbnail.
func (a *App) terminalRow(gtx C, fullString, currentPath string, selected bool) D {
	const thumbW = unit.Dp(44)

	return a.navRowShell(gtx, selected, thumbW*3/2, func(gtx C) D {
		titleCol := colorTerminal
		subCol := colorMuted
		if selected {
			titleCol = colorSelectFg
			subCol = colorSelectFg
		}

		media := a.titleToMedia[fullString]

		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				if media == nil {
					return D{}
				}
				return a.rowPoster(gtx, media)
			}),
			layout.Rigid(func(gtx C) D {
				if media == nil {
					return D{}
				}
				return layout.Spacer{Width: dp12}.Layout(gtx)
			}),
			layout.Flexed(1, func(gtx C) D {
				if selected {
					return a.label(gtx, sp, fullString, titleCol, font.Bold)
				}
				if currentPath == "" || !strings.HasPrefix(fullString, currentPath) {
					return a.label(gtx, sp, fullString, titleCol, 0)
				}
				return a.twoTone(gtx, sp, currentPath, fullString[len(currentPath):], titleCol, colorPrimary)
			}),
			layout.Rigid(func(gtx C) D {
				q := a.rowQuality(media)
				if q == "" {
					return D{}
				}
				return a.label(gtx, spTiny, q, subCol, font.Medium)
			}),
		)
	})
}

// rowPoster renders the small poster thumbnail for a terminal nav row.
func (a *App) rowPoster(gtx C, media *MediaItem) D {
	const thumbW = unit.Dp(44)
	if media.Type == "show" {
		id := media.ID
		return a.posterImage(gtx, "poster:"+id, func() (image.Image, error) {
			return a.client.fetchShowImage(id, func(s *papi.Show) *papi.File { return s.Poster })
		}, thumbW, radiusThumb)
	}
	id := media.ID
	return a.posterImage(gtx, "thumb:"+id, func() (image.Image, error) {
		return a.client.fetchMovieImage(id, func(m *papi.Movie) *papi.File { return m.Thumb })
	}, thumbW, radiusThumb)
}

// rowQuality returns the right-aligned quality label for a movie row.
func (a *App) rowQuality(media *MediaItem) string {
	if media == nil || media.Type != "movie" {
		return ""
	}
	movie, ok := a.client.getMovie(media.ID)
	if !ok {
		return ""
	}
	return formatQualityBadge(movie.Quality)
}
