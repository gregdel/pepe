package main

import (
	"fmt"
	"image"
	"strings"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/unit"
	"github.com/odwrtw/polochon/lib/papi"
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
		return a.label(gtx, sp, msg, colorMuted, 0)
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
				return a.label(gtx, spMeta, fmt.Sprintf("%d items in the library", count), colorSubtle, 0)
			}),
			layout.Rigid(layout.Spacer{Height: dp24}.Layout),
			layout.Rigid(func(gtx C) D {
				return a.hintItem(gtx, hint{key: "enter", label: "to browse"})
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

	return layout.UniformInset(dp32).Layout(gtx, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			// Group title with highlighted prefix
			layout.Rigid(func(gtx C) D {
				if currentPath != "" && strings.HasPrefix(group, currentPath) {
					return a.twoTone(gtx, spTitle, currentPath, group[len(currentPath):], colorFg, colorPrimary)
				}
				return a.label(gtx, spTitle, group, colorFg, font.Bold)
			}),
			layout.Rigid(layout.Spacer{Height: dp8}.Layout),
			layout.Rigid(func(gtx C) D {
				return a.label(gtx, spMeta, fmt.Sprintf("%d items in this group", node.SubtreeCount), colorSubtle, 0)
			}),
			layout.Rigid(layout.Spacer{Height: dp20}.Layout),
			layout.Rigid(hairline),
			layout.Rigid(layout.Spacer{Height: dp20}.Layout),
			// Sample of the group's content
			layout.Flexed(1, func(gtx C) D {
				maxShow := 14
				var lines []labelLine
				for i, child := range node.Children {
					if i >= maxShow {
						lines = append(lines, labelLine{
							text:  fmt.Sprintf("… and %d more", len(node.Children)-maxShow),
							color: colorMuted, size: sp,
						})
						break
					}
					var label string
					if child.IsTerminal && child.FullString != "" {
						label = child.FullString
					} else {
						label = group + child.Label
					}
					lines = append(lines, labelLine{text: label, color: colorFg, size: sp})
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

// previewShell draws fanart + scrim behind bottom-left anchored content.
func (a *App) previewShell(gtx C, fanartKey string, fanartFn func() (image.Image, error), content layout.Widget) D {
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx C) D {
			return a.fanart(gtx, fanartKey, fanartFn)
		}),
		layout.Expanded(func(gtx C) D {
			scrim(gtx)
			return D{Size: gtx.Constraints.Max}
		}),
		layout.Stacked(func(gtx C) D {
			return layout.UniformInset(dp32).Layout(gtx, func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Flexed(1, func(gtx C) D { return D{Size: image.Pt(0, gtx.Constraints.Max.Y)} }),
					layout.Rigid(content),
				)
			})
		}),
	)
}

func (a *App) previewMovie(gtx C, media *MediaItem) D {
	movie, ok := a.client.getMovie(media.ID)
	if !ok {
		return a.previewMessage(gtx, "Movie details not available")
	}
	movieID := movie.ImdbID
	id := media.ID

	return a.previewShell(gtx, "fanart:"+movieID, func() (image.Image, error) {
		return a.client.fetchMovieImage(movieID, func(m *papi.Movie) *papi.File { return m.Fanart })
	}, func(gtx C) D {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.End}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return a.posterImage(gtx, "thumb:"+id, func() (image.Image, error) {
					return a.client.fetchMovieImage(id, func(m *papi.Movie) *papi.File { return m.Thumb })
				}, unit.Dp(140), radiusPoster)
			}),
			layout.Rigid(layout.Spacer{Width: dp24}.Layout),
			layout.Flexed(1, func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						return a.body(gtx, spTitle, media.Title, colorFg, 2)
					}),
					layout.Rigid(layout.Spacer{Height: dp8}.Layout),
					layout.Rigid(func(gtx C) D {
						meta := buildMetaLine(movie.Year, movie.Runtime, movie.Genres)
						if meta == "" {
							return D{}
						}
						return a.label(gtx, spMeta, meta, colorSubtle, 0)
					}),
					layout.Rigid(layout.Spacer{Height: dp16}.Layout),
					layout.Rigid(func(gtx C) D {
						return a.hintItem(gtx, hint{key: "→", label: "for details"})
					}),
				)
			}),
		)
	})
}

func (a *App) previewShow(gtx C, media *MediaItem) D {
	show, ok := a.client.getShow(media.ID)
	if !ok {
		return a.previewMessage(gtx, "Show details not available")
	}
	showID := show.ImdbID

	seasonCount := len(show.Seasons)
	episodeCount := 0
	for _, s := range show.Seasons {
		episodeCount += len(s.Episodes)
	}

	return a.previewShell(gtx, "fanart:"+showID, func() (image.Image, error) {
		return a.client.fetchShowImage(showID, func(s *papi.Show) *papi.File { return s.Fanart })
	}, func(gtx C) D {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.End}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return a.posterImage(gtx, "poster:"+showID, func() (image.Image, error) {
					return a.client.fetchShowImage(showID, func(s *papi.Show) *papi.File { return s.Poster })
				}, unit.Dp(140), radiusPoster)
			}),
			layout.Rigid(layout.Spacer{Width: dp24}.Layout),
			layout.Flexed(1, func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						return a.body(gtx, spTitle, media.Title, colorFg, 2)
					}),
					layout.Rigid(layout.Spacer{Height: dp8}.Layout),
					layout.Rigid(func(gtx C) D {
						meta := buildShowMetaLine(show.Year, float64(show.Rating))
						if meta != "" {
							meta += "  ·  "
						}
						meta += fmt.Sprintf("%d seasons · %d episodes", seasonCount, episodeCount)
						return a.label(gtx, spMeta, meta, colorSubtle, 0)
					}),
					layout.Rigid(layout.Spacer{Height: dp16}.Layout),
					layout.Rigid(func(gtx C) D {
						return a.hintItem(gtx, hint{key: "→", label: "for details"})
					}),
				)
			}),
		)
	})
}
