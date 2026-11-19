package app

import (
	"fmt"
	"image"
	"strings"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"github.com/odwrtw/polochon/lib/papi"
)

// ── Show detail page ──────────────────────────────────────────────────────────
//
// A persistent poster column anchors the show identity beside the season and
// episode controls. Focus and playback remain owned by showPage.

func (a *App) layoutShowDetail(gtx C, s *showPage, alpha float32) D {
	show, ok := a.client.getShow(s.showID)
	if !ok {
		return a.detailMessage(gtx, "Show details not available")
	}
	showID := show.ImdbID
	return a.detailShell(gtx, alpha, showPosterWidth+detailColumnGap+showDetailWidth, func(gtx C) D {
		return a.showFanart(gtx, showID)
	}, func(gtx C) D {
		contentWidth := min(gtx.Dp(showDetailWidth), max(0, gtx.Constraints.Max.X-gtx.Dp(showPosterWidth+detailColumnGap)))
		return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return detailArtwork(gtx, alpha, func(gtx C) D {
					return a.showPoster(gtx, showID, showPosterWidth, radiusPoster)
				})
			}),
			layout.Rigid(layout.Spacer{Width: detailColumnGap}.Layout),
			layout.Rigid(func(gtx C) D {
				gtx.Constraints.Min.X = contentWidth
				gtx.Constraints.Max.X = contentWidth
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx C) D { return a.showHeader(gtx, s, show) }),
					layout.Rigid(layout.Spacer{Height: dp24}.Layout),
					layout.Rigid(func(gtx C) D { return a.showTabs(gtx, s) }),
					layout.Rigid(layout.Spacer{Height: dp24}.Layout),
					layout.Flexed(1, func(gtx C) D {
						return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
							layout.Flexed(0.4, func(gtx C) D { return a.showEpisodeList(gtx, s) }),
							layout.Rigid(layout.Spacer{Width: detailColumnGap}.Layout),
							layout.Flexed(0.6, func(gtx C) D { return a.showEpisodeInfo(gtx, s) }),
						)
					}),
				)
			}),
		)
	})
}

// showHeader keeps show identity above the season and episode controls.
func (a *App) showHeader(gtx C, s *showPage, show *papi.Show) D {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			return a.body(gtx, spTitle, s.title, colorFg, 2)
		}),
		layout.Rigid(layout.Spacer{Height: dp8}.Layout),
		layout.Rigid(func(gtx C) D {
			return a.body(gtx, spMeta, buildShowSummary(show), colorSubtle, 2)
		}),
	)
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
	keepSelectedVisible(&s.tabsList, s.seasonCursor, gtx.Constraints.Max, &s.tabsSize)

	return layout.Inset{Top: dp4, Bottom: dp4}.Layout(gtx, func(gtx C) D {
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
						col = colorFg
					}
					return a.label(gtx, sp, season.Label, col, weight)
				})
				call := macro.Stop()

				switch {
				case focused:
					defer a.markSelected(gtx, dims.Size).Pop()
					fillRounded(gtx, dims.Size, radiusRow, colorSelectBg)
					focusRing(gtx, dims.Size, radiusRow)
				case current:
					fillRounded(gtx, dims.Size, radiusRow, colorSurface2)
				}
				call.Add(gtx.Ops)
				return dims
			})
		})
	})
}

// showEpisodeList shares the page's reading surface with the episode synopsis.
func (a *App) showEpisodeList(gtx C, s *showPage) D {
	return layout.Inset{Top: dp8, Bottom: dp8}.Layout(gtx, func(gtx C) D {
		season := s.currentSeason()
		if season == nil || len(season.Children) == 0 {
			return layout.UniformInset(dp16).Layout(gtx, func(gtx C) D {
				return a.label(gtx, sp, "No episodes", colorFg, 0)
			})
		}

		keepSelectedVisible(&s.epList, s.epCursor, gtx.Constraints.Max, &s.epListSize)

		episodes := season.Children
		return s.epList.Layout(gtx, len(episodes), func(gtx C, i int) D {
			node := episodes[i]
			selected := i == s.epCursor && s.focus == focusEpisodes

			return a.rowShell(gtx, selected, false, browseRowHeight, func(gtx C) D {
				titleCol := colorFg
				subCol := colorSubtle
				if selected {
					titleCol = colorSelectFg
					subCol = colorSelectFg
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
						return layout.Inset{Left: dp12}.Layout(gtx, func(gtx C) D {
							return a.label(gtx, spTiny, fmt.Sprintf("%d min", node.Episode.Runtime), subCol, 0)
						})
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
				return a.body(gtx, spBrowse, title, colorFg, 2)
			}),
		)
	})

	playText := "▶ Play"
	if pos := a.resumePos[episodeMediaKey(node.ShowID, ep.Season, ep.Episode)]; pos > minimumResumePosition {
		playText = "▶ Resume " + formatResumePos(pos)
	}
	sections = append(sections, spacerFn(dp12))
	sections = append(sections, func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return a.detailActionButton(gtx, "detail:show:play", "Play episode", playText, true, func() {
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
			return a.prose(gtx, plot)
		})
	}

	if technical := formatTechnicalMetadata(ep.Size, ep.Quality, ep.VideoCodec, ep.AudioCodec, ep.Container, ep.ReleaseGroup); technical != "" {
		sections = append(sections, spacerFn(dp16))
		sections = append(sections, func(gtx C) D {
			return a.body(gtx, spMeta, technical, colorSubtle, 1)
		})
	}

	if a.playerStatus == PlayerStatusError {
		sections = append(sections, spacerFn(dp12), a.renderPlayerStatus)
	}

	gtx.Constraints.Min = image.Point{}
	return layout.Inset{Top: dp8, Bottom: dp8}.Layout(gtx, func(gtx C) D {
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
