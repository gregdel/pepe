package app

import (
	"fmt"
	"image"
	"image/color"
	"strings"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"github.com/dustin/go-humanize"
	"github.com/odwrtw/polochon/lib/papi"
)

// ── Show detail page ──────────────────────────────────────────────────────────
//
// Fanart header with poster + plot, season tabs below, then the episode list
// on the left and the focused episode's details on the right (Netflix-style).

func (a *App) layoutShowDetail(gtx C, s *showPage) D {
	show, ok := a.client.getShow(s.showID)
	if !ok {
		return a.detailMessage(gtx, "Show details not available")
	}
	showID := show.ImdbID

	return a.detailShell(gtx, "fanart:"+showID, func() (image.Image, error) {
		return a.client.fetchShowImage(showID, func(s *papi.Show) *papi.File { return s.Fanart })
	}, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return a.showHeader(gtx, s, show)
			}),
			layout.Rigid(layout.Spacer{Height: dp24}.Layout),
			layout.Rigid(func(gtx C) D {
				return a.showTabs(gtx, s)
			}),
			layout.Rigid(layout.Spacer{Height: dp16}.Layout),
			layout.Flexed(1, func(gtx C) D {
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
					layout.Flexed(0.45, func(gtx C) D {
						return a.showEpisodeList(gtx, s)
					}),
					layout.Rigid(layout.Spacer{Width: dp24}.Layout),
					layout.Flexed(0.55, func(gtx C) D {
						return a.showEpisodeInfo(gtx, s)
					}),
				)
			}),
		)
	})
}

// showHeader renders poster + title + meta + plot + counts.
func (a *App) showHeader(gtx C, s *showPage, show *papi.Show) D {
	showID := show.ImdbID

	seasonCount := len(show.Seasons)
	episodeCount := 0
	for _, sn := range show.Seasons {
		episodeCount += len(sn.Episodes)
	}

	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Start}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			return a.posterImage(gtx, "poster:"+showID, func() (image.Image, error) {
				return a.client.fetchShowImage(showID, func(s *papi.Show) *papi.File { return s.Poster })
			}, unit.Dp(200), radiusPoster)
		}),
		layout.Rigid(layout.Spacer{Width: dp32}.Layout),
		layout.Flexed(1, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					return a.body(gtx, spTitle, s.title, colorFg, 2)
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
					if show.Plot == "" {
						return D{}
					}
					return a.body(gtx, spBody, show.Plot, colorFg, 3)
				}),
			)
		}),
	)
}

// showTabs renders the horizontal season selector.
func (a *App) showTabs(gtx C, s *showPage) D {
	// Keep the selected tab visible.
	if pos := s.tabsList.Position; pos.Count > 0 {
		if s.seasonCursor < pos.First {
			s.tabsList.ScrollTo(s.seasonCursor)
		} else if s.seasonCursor > pos.First+pos.Count-1 {
			s.tabsList.ScrollTo(s.seasonCursor - pos.Count + 1)
		}
	}

	return s.tabsList.Layout(gtx, len(s.seasons), func(gtx C, i int) D {
		season := s.seasons[i]
		current := i == s.seasonCursor
		focused := current && s.focus == focusTabs

		return layout.Inset{Right: dp8}.Layout(gtx, func(gtx C) D {
			inset := layout.Inset{Left: dp16, Right: dp16, Top: dp8, Bottom: dp8}
			macro := op.Record(gtx.Ops)
			dims := inset.Layout(gtx, func(gtx C) D {
				col := colorMuted
				weight := font.Medium
				switch {
				case focused:
					col = colorSelectFg
					weight = font.Bold
				case current:
					col = colorPrimary
				}
				return a.label(gtx, sp, season.Label, col, weight)
			})
			call := macro.Stop()

			switch {
			case focused:
				fillRounded(gtx, dims.Size, radiusRow, colorSelectBg)
			case current:
				fillRounded(gtx, dims.Size, radiusRow, colorSurface2)
				strokeRounded(gtx, dims.Size, radiusRow, colorPrimary)
			}
			call.Add(gtx.Ops)
			return dims
		})
	})
}

// showEpisodeList renders the episodes of the selected season.
func (a *App) showEpisodeList(gtx C, s *showPage) D {
	season := s.currentSeason()
	if season == nil || len(season.Children) == 0 {
		return a.label(gtx, sp, "No episodes", colorMuted, 0)
	}

	// Keep the focused episode visible.
	if pos := s.epList.Position; pos.Count > 0 {
		if s.epCursor < pos.First {
			s.epList.ScrollTo(s.epCursor)
		} else if s.epCursor > pos.First+pos.Count-1 {
			s.epList.ScrollTo(s.epCursor - pos.Count + 1)
		}
	}

	episodes := season.Children
	return s.epList.Layout(gtx, len(episodes), func(gtx C, i int) D {
		ep := episodes[i]
		selected := i == s.epCursor && s.focus == focusEpisodes
		isCursor := i == s.epCursor

		return a.navRowShell(gtx, selected, 0, func(gtx C) D {
			titleCol := colorFg
			subCol := colorMuted
			if selected {
				titleCol = colorSelectFg
				subCol = colorSelectFg
			} else if isCursor {
				titleCol = colorPrimary
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx C) D {
					weight := font.Normal
					if selected {
						weight = font.Bold
					}
					return a.label(gtx, sp, ep.Label, titleCol, weight)
				}),
				layout.Rigid(func(gtx C) D {
					if ep.Episode == nil || ep.Episode.Runtime <= 0 {
						return D{}
					}
					return a.label(gtx, spTiny, fmt.Sprintf("%d min", ep.Episode.Runtime), subCol, 0)
				}),
			)
		})
	})
}

// showEpisodeInfo renders the focused episode's details card.
func (a *App) showEpisodeInfo(gtx C, s *showPage) D {
	node := s.currentEpisode()
	if node == nil || node.Episode == nil {
		return D{}
	}
	ep := node.Episode

	// Build the info sections.
	var sections []func(C) D

	sections = append(sections, func(gtx C) D {
		return a.body(gtx, sp, node.Label, colorFg, 2)
	})
	sections = append(sections, spacerFn(dp8))

	if meta := buildEpisodeMetaLine(ep); meta != "" {
		sections = append(sections, func(gtx C) D {
			return a.label(gtx, spTiny, meta, colorSubtle, 0)
		})
		sections = append(sections, spacerFn(dp12))
	}

	if badges := a.buildEpisodeBadges(ep); len(badges) > 0 {
		sections = append(sections, func(gtx C) D {
			return a.renderBadgeRow(gtx, badges)
		})
		sections = append(sections, spacerFn(dp12))
	}

	if ep.Plot != "" {
		p := ep.Plot
		sections = append(sections, func(gtx C) D {
			return a.body(gtx, sp, p, colorFg, 5)
		})
		sections = append(sections, spacerFn(dp12))
	}

	epMediaKey := episodeMediaKey(node.ShowID, ep.Season, ep.Episode)
	if pos, ok := a.resumePos[epMediaKey]; ok && pos > minimumResumePosition {
		p := pos
		sections = append(sections, func(gtx C) D {
			return a.renderKV(gtx, "Resume", formatResumePos(p))
		})
		sections = append(sections, spacerFn(dp8))
	}

	sections = append(sections, func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return a.hintItem(gtx, hint{key: "enter", label: "play"})
			}),
			layout.Rigid(layout.Spacer{Width: dp16}.Layout),
			layout.Rigid(func(gtx C) D {
				return a.hintItem(gtx, hint{key: "s", label: "subtitles"})
			}),
		)
	})

	// Rounded info card.
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx C) D {
			fillRounded(gtx, gtx.Constraints.Max, radiusCard, color.NRGBA{R: 0x10, G: 0x10, B: 0x14, A: 0xd9})
			return D{Size: gtx.Constraints.Max}
		}),
		layout.Stacked(func(gtx C) D {
			return layout.UniformInset(dp24).Layout(gtx, func(gtx C) D {
				return s.infoList.Layout(gtx, len(sections), func(gtx C, i int) D {
					return sections[i](gtx)
				})
			})
		}),
	)
}

// buildEpisodeMetaLine returns "Aired 2008-03-02  ·  45 min".
func buildEpisodeMetaLine(ep *papi.Episode) string {
	var parts []string
	if ep.Aired != "" {
		parts = append(parts, "Aired "+ep.Aired)
	}
	if ep.Runtime > 0 {
		parts = append(parts, fmt.Sprintf("%d min", ep.Runtime))
	}
	if ep.ReleaseGroup != "" {
		parts = append(parts, ep.ReleaseGroup)
	}
	return strings.Join(parts, "  ·  ")
}

func buildShowMetaLine(year int, rating float64) string {
	var parts []string
	if year > 0 {
		parts = append(parts, fmt.Sprintf("%d", year))
	}
	if rating > 0 {
		parts = append(parts, fmt.Sprintf("%.1f", rating))
	}
	return strings.Join(parts, "  ·  ")
}

func (a *App) buildEpisodeBadges(ep *papi.Episode) []badge {
	var badges []badge
	if ep.Size > 0 {
		badges = append(badges, badge{text: humanize.Bytes(uint64(ep.Size)), bg: colorSizeBadgeBg})
	}
	if q := formatQualityBadge(ep.Quality); q != "" {
		badges = append(badges, badge{text: q, bg: colorQualBadgeBg})
	}
	if c := formatCodecs(ep.VideoCodec, ep.AudioCodec); c != "" {
		badges = append(badges, badge{text: c, bg: colorCodecBadgeBg})
	}
	if ct := formatContainer(ep.Container); ct != "" {
		badges = append(badges, badge{text: ct, bg: colorBadgeBg})
	}
	return badges
}
