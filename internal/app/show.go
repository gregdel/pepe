package app

import (
	"fmt"
	"strings"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"github.com/odwrtw/polochon/lib/papi"
)

// ── Show detail page ──────────────────────────────────────────────────────────
//
// Protected show summary and season tabs above a stable episode list and the
// focused episode's details. Focus and playback remain owned by showPage.

func (a *App) layoutShowDetail(gtx C, s *showPage) D {
	show, ok := a.client.getShow(s.showID)
	if !ok {
		return a.detailMessage(gtx, "Show details not available")
	}
	showID := show.ImdbID

	return a.detailShell(gtx, func(gtx C) D {
		return a.showFanart(gtx, showID)
	}, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return a.showHeader(gtx, s, show)
			}),
			layout.Rigid(layout.Spacer{Height: dp20}.Layout),
			layout.Rigid(func(gtx C) D {
				return a.showTabs(gtx, s)
			}),
			layout.Rigid(layout.Spacer{Height: dp12}.Layout),
			layout.Flexed(1, func(gtx C) D {
				return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
					layout.Flexed(0.42, func(gtx C) D {
						return a.showEpisodeList(gtx, s)
					}),
					layout.Rigid(layout.Spacer{Width: dp24}.Layout),
					layout.Flexed(0.58, func(gtx C) D {
						return a.showEpisodeInfo(gtx, s)
					}),
				)
			}),
		)
	})
}

// showHeader renders a compact, protected poster + show summary. The width
// leaves an artwork-only region on wide screens and keeps prose lines short.
func (a *App) showHeader(gtx C, s *showPage, show *papi.Show) D {
	showID := show.ImdbID
	headerWidth := min(gtx.Dp(unit.Dp(1300)), gtx.Constraints.Max.X)
	gtx.Constraints.Min.X = headerWidth
	gtx.Constraints.Max.X = headerWidth

	return detailPanel(gtx, layout.UniformInset(dp24), func(gtx C) D {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Start}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return a.showPoster(gtx, showID, unit.Dp(180), radiusPoster)
			}),
			layout.Rigid(layout.Spacer{Width: dp32}.Layout),
			layout.Flexed(1, func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						return a.body(gtx, spTitle, s.title, colorFg, 2)
					}),
					layout.Rigid(layout.Spacer{Height: dp8}.Layout),
					layout.Rigid(func(gtx C) D {
						return a.body(gtx, spMeta, buildShowSummary(show), colorSubtle, 2)
					}),
					layout.Rigid(func(gtx C) D {
						if show.Plot == "" {
							return D{}
						}
						return layout.Inset{Top: dp16}.Layout(gtx, func(gtx C) D {
							return a.body(gtx, spBody, show.Plot, colorFg, 2)
						})
					}),
				)
			}),
		)
	})
}

func showCounts(show *papi.Show) (seasons, episodes int) {
	seasons = len(show.Seasons)
	for _, season := range show.Seasons {
		if season != nil {
			episodes += len(season.Episodes)
		}
	}
	return seasons, episodes
}

// showTabs renders the horizontal season selector.
func (a *App) showTabs(gtx C, s *showPage) D {
	if pos := s.tabsList.Position; pos.Count > 0 {
		if s.seasonCursor < pos.First {
			s.tabsList.ScrollTo(s.seasonCursor)
		} else if s.seasonCursor > pos.First+pos.Count-1 {
			s.tabsList.ScrollTo(s.seasonCursor - pos.Count + 1)
		}
	}

	return detailPanel(gtx, layout.Inset{Left: dp8, Right: dp8, Top: dp4, Bottom: dp4}, func(gtx C) D {
		return s.tabsList.Layout(gtx, len(s.seasons), func(gtx C, i int) D {
			season := s.seasons[i]
			current := i == s.seasonCursor
			focused := current && s.focus == focusTabs

			return layout.Inset{Right: dp8}.Layout(gtx, func(gtx C) D {
				inset := layout.Inset{Left: dp16, Right: dp16, Top: dp8, Bottom: dp8}
				macro := op.Record(gtx.Ops)
				dims := inset.Layout(gtx, func(gtx C) D {
					col := colorSubtle
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
	})
}

// showEpisodeList renders episodes on a stable surface for arbitrary fanart.
func (a *App) showEpisodeList(gtx C, s *showPage) D {
	return detailPanel(gtx, layout.Inset{Top: dp8, Bottom: dp8}, func(gtx C) D {
		season := s.currentSeason()
		if season == nil || len(season.Children) == 0 {
			return layout.UniformInset(dp16).Layout(gtx, func(gtx C) D {
				return a.label(gtx, sp, "No episodes", colorMuted, 0)
			})
		}

		if pos := s.epList.Position; pos.Count > 0 {
			if s.epCursor < pos.First {
				s.epList.ScrollTo(s.epCursor)
			} else if s.epCursor > pos.First+pos.Count-1 {
				s.epList.ScrollTo(s.epCursor - pos.Count + 1)
			}
		}

		episodes := season.Children
		return s.epList.Layout(gtx, len(episodes), func(gtx C, i int) D {
			node := episodes[i]
			selected := i == s.epCursor && s.focus == focusEpisodes
			isCursor := i == s.epCursor

			return a.rowShell(gtx, selected, false, 0, func(gtx C) D {
				titleCol := colorFg
				subCol := colorSubtle
				if selected {
					titleCol = colorSelectFg
					subCol = colorSelectFg
				} else if isCursor {
					titleCol = colorPrimary
				}
				code, title := episodeDisplayParts(node)
				weight := font.Normal
				if selected {
					weight = font.Bold
				}

				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						return layout.Inset{Right: dp16}.Layout(gtx, func(gtx C) D {
							return a.label(gtx, sp, code, titleCol, font.Medium)
						})
					}),
					layout.Flexed(1, func(gtx C) D {
						return a.label(gtx, sp, title, titleCol, weight)
					}),
					layout.Rigid(func(gtx C) D {
						if node.Episode == nil || node.Episode.Runtime <= 0 {
							return D{}
						}
						return a.label(gtx, spTiny, fmt.Sprintf("%d min", node.Episode.Runtime), subCol, 0)
					}),
				)
			})
		})
	})
}

// showEpisodeInfo renders the focused episode's editorial information first and
// demotes file details to one secondary line after the synopsis.
func (a *App) showEpisodeInfo(gtx C, s *showPage) D {
	node := s.currentEpisode()
	if node == nil || node.Episode == nil {
		return D{}
	}
	ep := node.Episode
	code, title := episodeDisplayParts(node)

	var sections []func(C) D
	sections = append(sections, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return a.label(gtx, spTiny, code, colorPrimary, font.Bold)
			}),
			layout.Rigid(layout.Spacer{Height: dp4}.Layout),
			layout.Rigid(func(gtx C) D {
				return a.body(gtx, spBody, title, colorFg, 2)
			}),
		)
	})

	sections = append(sections, spacerFn(dp12))
	sections = append(sections, func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return a.detailActionButton(gtx, "detail:show:play", "Play episode", "▶ Play", true, func() {
					a.launchEpisode(node)
				})
			}),
			layout.Rigid(layout.Spacer{Width: dp12}.Layout),
			layout.Rigid(func(gtx C) D {
				return a.detailActionButton(gtx, "detail:show:subtitles", "Episode subtitles", "Subtitles", false, a.openSubtitlePopup)
			}),
		)
	})

	if meta := buildEpisodeMetaLine(ep); meta != "" {
		sections = append(sections, spacerFn(dp12))
		sections = append(sections, func(gtx C) D {
			return a.body(gtx, spMeta, meta, colorSubtle, 2)
		})
	}

	if ep.Plot != "" {
		plot := ep.Plot
		sections = append(sections, spacerFn(dp16))
		sections = append(sections, func(gtx C) D {
			return a.body(gtx, spBody, plot, colorFg, 0)
		})
	}

	if technical := formatTechnicalMetadata(ep.Size, ep.Quality, ep.VideoCodec, ep.AudioCodec, ep.Container, ep.ReleaseGroup); technical != "" {
		sections = append(sections, spacerFn(dp16))
		sections = append(sections, func(gtx C) D {
			return a.body(gtx, spMeta, technical, colorSubtle, 2)
		})
	}

	epMediaKey := episodeMediaKey(node.ShowID, ep.Season, ep.Episode)
	if pos, ok := a.resumePos[epMediaKey]; ok && pos > minimumResumePosition {
		position := pos
		sections = append(sections, spacerFn(dp12))
		sections = append(sections, func(gtx C) D {
			return a.renderKV(gtx, "Resume", formatResumePos(position))
		})
	}

	return detailPanel(gtx, layout.UniformInset(dp24), func(gtx C) D {
		return s.infoList.Layout(gtx, len(sections), func(gtx C, i int) D {
			return sections[i](gtx)
		})
	})
}

func episodeDisplayParts(node *DynamicNode) (code, title string) {
	if node == nil {
		return "", ""
	}
	if node.Episode == nil {
		return node.Label, ""
	}
	return fmt.Sprintf("S%02dE%02d", node.Episode.Season, node.Episode.Episode), node.Episode.Title
}

// buildEpisodeMetaLine returns user-facing editorial facts. File release data is
// kept in the separate technical metadata line.
func buildEpisodeMetaLine(ep *papi.Episode) string {
	var parts []string
	if aired := formatAiredDate(ep.Aired); aired != "" {
		parts = append(parts, "Aired "+aired)
	}
	if ep.Runtime > 0 {
		parts = append(parts, fmt.Sprintf("%d min", ep.Runtime))
	}
	if ep.Rating > 0 {
		parts = append(parts, fmt.Sprintf("%.1f", ep.Rating))
	}
	return strings.Join(parts, "  ·  ")
}

func buildShowSummary(show *papi.Show) string {
	seasons, episodes := showCounts(show)
	meta := buildShowMetaLine(show.Year, float64(show.Rating))
	if meta != "" {
		meta += "  ·  "
	}
	return meta + fmt.Sprintf("%d seasons · %d episodes", seasons, episodes)
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
