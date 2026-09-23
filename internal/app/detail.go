package app

import (
	"fmt"
	"image"
	"strings"
	"time"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// ── Detail screen dispatch ────────────────────────────────────────────────────

const detailFadeDuration = 140 * time.Millisecond

// layoutDetail renders the pushed detail page with a short fade-in transition.
func (a *App) layoutDetail(gtx C) D {
	d := a.detail
	if d == nil {
		return D{}
	}

	content := func(gtx C) D {
		if d.kind == detailShow {
			return a.layoutShowDetail(gtx, d.show)
		}
		return a.layoutMovieDetail(gtx, d)
	}

	// Fade-in on push (skipped during playback to keep the UI quiet).
	if a.playerStatus == PlayerStatusRunning {
		return content(gtx)
	}
	since := time.Since(d.enteredAt)
	if since >= detailFadeDuration {
		return content(gtx)
	}
	alpha := float32(since) / float32(detailFadeDuration)
	gtx.Execute(op.InvalidateCmd{})

	macro := op.Record(gtx.Ops)
	dims := content(gtx)
	call := macro.Stop()
	stack := paint.PushOpacity(gtx.Ops, alpha)
	call.Add(gtx.Ops)
	stack.Pop()
	return dims
}

// detailShell draws fanart + scrim behind the page content.
func (a *App) detailShell(gtx C, fanart layout.Widget, content layout.Widget) D {
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(fanart),
		layout.Expanded(func(gtx C) D {
			detailScrim(gtx)
			return D{Size: gtx.Constraints.Max}
		}),
		layout.Stacked(func(gtx C) D {
			return detailSafeInsets(gtx).Layout(gtx, content)
		}),
	)
}

// ── Movie detail page ─────────────────────────────────────────────────────────

func (a *App) layoutMovieDetail(gtx C, d *detailScreen) D {
	movie, ok := a.client.getMovie(d.movieID)
	if !ok {
		return a.detailMessage(gtx, "Movie details not available")
	}
	movieID := movie.ImdbID
	id := d.movieID

	var sections []func(C) D
	sections = append(sections, func(gtx C) D {
		return a.body(gtx, spTitle, d.movieTitle, colorFg, 2)
	})

	if movie.Tagline != "" {
		tl := movie.Tagline
		sections = append(sections, spacerFn(dp8))
		sections = append(sections, func(gtx C) D {
			lbl := material.Label(a.th, spBody, tl)
			lbl.Color = colorSubtle
			lbl.Font.Style = font.Italic
			lbl.MaxLines = 2
			lbl.LineHeightScale = 1.4
			return lbl.Layout(gtx)
		})
	}

	if metaLine := buildMetaLine(movie.Year, movie.Runtime, movie.Genres); metaLine != "" {
		sections = append(sections, spacerFn(dp8))
		sections = append(sections, func(gtx C) D {
			return a.body(gtx, spMeta, metaLine, colorSubtle, 2)
		})
	}

	sections = append(sections, spacerFn(dp16))
	sections = append(sections, func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return a.detailActionButton(gtx, "detail:movie:play", "Play movie", "▶ Play", true, func() {
					a.launchMovie(&MediaItem{ID: d.movieID, Title: d.movieTitle, Type: mediaKindMovie})
				})
			}),
			layout.Rigid(layout.Spacer{Width: dp12}.Layout),
			layout.Rigid(func(gtx C) D {
				return a.detailActionButton(gtx, "detail:movie:subtitles", "Movie subtitles", "Subtitles", false, a.openSubtitlePopup)
			}),
		)
	})

	if movie.Plot != "" {
		p := movie.Plot
		sections = append(sections, spacerFn(dp20))
		sections = append(sections, func(gtx C) D {
			return a.body(gtx, spBody, p, colorFg, 0)
		})
	}

	if technical := formatTechnicalMetadata(movie.Size, movie.Quality, movie.VideoCodec, movie.AudioCodec, movie.Container, movie.ReleaseGroup); technical != "" {
		sections = append(sections, spacerFn(dp16))
		sections = append(sections, func(gtx C) D {
			return a.body(gtx, spMeta, technical, colorSubtle, 2)
		})
	}

	subText := formatSubtitles(movie.EmbeddedSubtitles, movie.Subtitles)
	sections = append(sections, spacerFn(dp20))
	sections = append(sections, func(gtx C) D {
		return a.body(gtx, sp, subText, colorFg, 0)
	})

	if dt := formatDateAdded(movie.DateAdded); dt != "" {
		sections = append(sections, spacerFn(dp12))
		sections = append(sections, func(gtx C) D {
			return a.renderKV(gtx, "Added", dt)
		})
	}

	movieKey := movieMediaKey(movieID)
	if pos, ok := a.resumePos[movieKey]; ok && pos > minimumResumePosition {
		p := pos
		sections = append(sections, spacerFn(dp8))
		sections = append(sections, func(gtx C) D {
			return a.renderKV(gtx, "Resume", formatResumePos(p))
		})
	}

	sections = append(sections, spacerFn(dp16))
	sections = append(sections, func(gtx C) D {
		return a.renderPlayerStatus(gtx)
	})

	return a.detailShell(gtx, func(gtx C) D {
		return a.movieFanart(gtx, movieID)
	}, func(gtx C) D {
		posterWidth := gtx.Dp(unit.Dp(280))
		gap := gtx.Dp(dp32)
		panelWidth := min(gtx.Dp(unit.Dp(900)), max(0, gtx.Constraints.Max.X-posterWidth-gap))

		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Start}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return a.movieThumb(gtx, id, unit.Dp(280), radiusPoster)
			}),
			layout.Rigid(layout.Spacer{Width: dp32}.Layout),
			layout.Rigid(func(gtx C) D {
				gtx.Constraints = layout.Exact(image.Pt(panelWidth, gtx.Constraints.Max.Y))
				return detailPanel(gtx, layout.UniformInset(dp32), func(gtx C) D {
					return a.detailList.Layout(gtx, len(sections), func(gtx C, i int) D {
						return sections[i](gtx)
					})
				})
			}),
			layout.Flexed(1, func(gtx C) D { return D{Size: gtx.Constraints.Min} }),
		)
	})
}

func (a *App) detailMessage(gtx C, msg string) D {
	return layout.UniformInset(dp48).Layout(gtx, func(gtx C) D {
		return a.label(gtx, sp, msg, colorMuted, 0)
	})
}

// buildMetaLine returns "1999  ·  2h 16m  ·  Action, Sci-Fi" from resolved values.
func buildMetaLine(year, runtime int, genres []string) string {
	var parts []string
	if year > 0 {
		parts = append(parts, fmt.Sprintf("%d", year))
	}
	if runtime > 0 {
		h, m := runtime/60, runtime%60
		if h > 0 {
			parts = append(parts, fmt.Sprintf("%dh %dm", h, m))
		} else {
			parts = append(parts, fmt.Sprintf("%dm", m))
		}
	}
	if g := formatGenres(genres); g != "" {
		parts = append(parts, g)
	}
	return strings.Join(parts, "  ·  ")
}

// ── Player status ─────────────────────────────────────────────────────────────

func (a *App) renderPlayerStatus(gtx C) D {
	status := a.playerStatus
	if status == "" {
		status = PlayerStatusIdle
	}

	var lines []labelLine
	statusText := string(status)
	if status == PlayerStatusRunning {
		statusText = "Playing"
	}
	lines = append(lines, labelLine{text: "Player", color: colorPrimary, size: spTiny, weight: font.Bold})
	lines = append(lines, labelLine{text: fmt.Sprintf("Status: %s", statusText), color: colorFg, size: sp})
	if a.playerMode != "" {
		lines = append(lines, labelLine{text: fmt.Sprintf("Mode: %s", a.playerMode), color: colorFg, size: sp})
	}
	if status == PlayerStatusError && a.playerError != "" {
		lines = append(lines, labelLine{text: "Error:", color: colorMuted, size: sp})
		lines = append(lines, labelLine{text: a.playerError, color: colorTerminal, size: sp})
	}
	return a.renderLines(gtx, &a.playerLinesList, lines)
}
