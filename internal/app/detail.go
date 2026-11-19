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

const (
	detailFadeDuration = 140 * time.Millisecond
	moviePosterWidth   = unit.Dp(280)
	showPosterWidth    = unit.Dp(240)
	movieDetailWidth   = proseWidth // narrow panel, wider fanart feather
	showDetailWidth    = unit.Dp(1300)
	detailColumnGap    = dp32
)

// layoutDetail fades in artwork without fading the text or its reading surface.
func (a *App) layoutDetail(gtx C) D {
	d := a.detail
	if d == nil {
		return D{}
	}

	alpha := float32(1)
	if a.playerStatus != PlayerStatusRunning {
		now := gtx.Now
		if now.IsZero() {
			now = time.Now()
		}
		since := now.Sub(d.enteredAt)
		if since < detailFadeDuration {
			alpha = max(0, float32(since)/float32(detailFadeDuration))
			gtx.Execute(op.InvalidateCmd{})
		}
	}
	if d.kind == detailShow {
		return a.layoutShowDetail(gtx, d.show, alpha)
	}
	return a.layoutMovieDetail(gtx, d, alpha)
}

func detailArtwork(gtx C, alpha float32, artwork layout.Widget) D {
	if alpha >= 1 {
		return artwork(gtx)
	}
	stack := paint.PushOpacity(gtx.Ops, alpha)
	dims := artwork(gtx)
	stack.Pop()
	return dims
}

// detailShell protects the full-height reading area for both detail layouts.
func (a *App) detailShell(gtx C, alpha float32, readingWidth unit.Dp, fanart, content layout.Widget) D {
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx C) D { return detailArtwork(gtx, alpha, fanart) }),
		layout.Expanded(func(gtx C) D {
			edge := gtx.Dp(a.viewportInsets.Left + readingWidth + dp12)
			detailScrim(gtx, edge)
			return D{Size: gtx.Constraints.Max}
		}),
		layout.Stacked(func(gtx C) D {
			insets := a.viewportInsets
			insets.Bottom = dp16
			if a.playerStatus == PlayerStatusRunning {
				insets.Top = dp16
			}
			return insets.Layout(gtx, func(gtx C) D {
				gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
				return content(gtx)
			})
		}),
	)
}

// ── Movie detail page ─────────────────────────────────────────────────────────

func (a *App) layoutMovieDetail(gtx C, d *detailScreen, alpha float32) D {
	movie, ok := a.client.getMovie(d.movieID)
	if !ok {
		return a.detailMessage(gtx, "Movie details not available")
	}
	movieID := movie.ImdbID
	id := d.movieID

	playText := "Play"
	if pos := a.resumePos[movieMediaKey(movieID)]; pos > minimumResumePosition {
		playText = "Resume " + formatResumePos(pos)
	}

	var sections []func(C) D
	sections = append(sections, func(gtx C) D {
		return a.body(gtx, spTitle, d.movieTitle, colorFg, 2)
	})

	if metaLine := buildMetaLine(movie.Year, movie.Runtime, "", float64(movie.Rating), movie.Genres); metaLine != "" {
		sections = append(sections, spacerFn(dp8))
		sections = append(sections, func(gtx C) D {
			return a.body(gtx, spMeta, metaLine, colorSubtle, 2)
		})
	}

	sections = append(sections, spacerFn(dp16))
	sections = append(sections, func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return a.detailActionButton(gtx, "detail:movie:play", "Play movie", playText, playIcon, true, func() {
					a.launchMovie(&MediaItem{ID: d.movieID, Title: d.movieTitle, Type: mediaKindMovie})
				})
			}),
			layout.Rigid(layout.Spacer{Width: dp12}.Layout),
			layout.Rigid(func(gtx C) D {
				return a.detailActionButton(gtx, "detail:movie:subtitles", "Movie subtitles", "Subtitles", nil, false, a.openSubtitlePopup)
			}),
		)
	})

	if movie.Plot != "" {
		p := movie.Plot
		sections = append(sections, spacerFn(dp20))
		sections = append(sections, func(gtx C) D {
			return a.prose(gtx, p)
		})
	}

	if movie.Tagline != "" {
		tl := movie.Tagline
		sections = append(sections, spacerFn(dp12))
		sections = append(sections, func(gtx C) D {
			lbl := material.Label(a.th, spMeta, tl)
			lbl.Color = colorSubtle
			lbl.Font.Style = font.Italic
			lbl.MaxLines = 2
			lbl.LineHeightScale = 1.4
			return lbl.Layout(gtx)
		})
	}

	if technical := formatTechnicalMetadata(movie.Size, movie.Quality, movie.VideoCodec, movie.AudioCodec, movie.Container, movie.ReleaseGroup); technical != "" {
		sections = append(sections, spacerFn(dp16))
		sections = append(sections, func(gtx C) D {
			return a.body(gtx, spMeta, technical, colorSubtle, 1)
		})
	}

	subText := formatSubtitleSummary(movie.EmbeddedSubtitles, movie.Subtitles)
	sections = append(sections, spacerFn(dp12))
	sections = append(sections, func(gtx C) D {
		return a.body(gtx, spMeta, subText, colorSubtle, 1)
	})

	if a.playerStatus != "" && a.playerStatus != PlayerStatusIdle {
		sections = append(sections, spacerFn(dp16), a.renderPlayerStatus)
	}

	return a.detailShell(gtx, alpha, moviePosterWidth+detailColumnGap+movieDetailWidth, func(gtx C) D {
		return a.movieFanart(gtx, movieID)
	}, func(gtx C) D {
		panelWidth := min(gtx.Dp(movieDetailWidth), max(0, gtx.Constraints.Max.X-gtx.Dp(moviePosterWidth+detailColumnGap)))

		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return detailArtwork(gtx, alpha, func(gtx C) D {
					return a.movieThumb(gtx, id, moviePosterWidth, radiusPoster)
				})
			}),
			layout.Rigid(layout.Spacer{Width: detailColumnGap}.Layout),
			layout.Rigid(func(gtx C) D {
				gtx.Constraints.Min = image.Pt(panelWidth, 0)
				gtx.Constraints.Max.X = panelWidth
				return a.detailList.Layout(gtx, len(sections), func(gtx C, i int) D {
					return sections[i](gtx)
				})
			}),
			layout.Flexed(1, func(gtx C) D { return D{Size: gtx.Constraints.Min} }),
		)
	})
}

func (a *App) detailMessage(gtx C, msg string) D {
	return a.viewportInsets.Layout(gtx, func(gtx C) D {
		return a.label(gtx, sp, msg, colorFg, 0)
	})
}

// buildMetaLine returns "1999  ·  2h 16m  ·  1080p  ·  8.7/10  ·  Action, Sci-Fi"
// from resolved values; an empty quality is omitted.
func buildMetaLine(year, runtime int, quality string, rating float64, genres []string) string {
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
	if quality != "" {
		parts = append(parts, quality)
	}
	if rating > 0 {
		parts = append(parts, formatRating(rating))
	}
	if g := formatGenres(genres); g != "" {
		parts = append(parts, g)
	}
	return strings.Join(parts, "  ·  ")
}

// ── Player status ─────────────────────────────────────────────────────────────

func (a *App) renderPlayerStatus(gtx C) D {
	status := a.playerStatus
	if status == "" || status == PlayerStatusIdle {
		return D{}
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
		lines = append(lines, labelLine{text: "Error:", color: colorFg, size: sp})
		lines = append(lines, labelLine{text: a.playerError, color: colorTerminal, size: sp})
	}
	return a.renderLines(gtx, lines)
}
