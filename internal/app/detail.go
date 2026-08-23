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
	"github.com/dustin/go-humanize"
	polochon "github.com/odwrtw/polochon/lib"
	"github.com/odwrtw/polochon/lib/papi"
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
func (a *App) detailShell(gtx C, fanartKey string, fanartFn func() (image.Image, error), content layout.Widget) D {
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx C) D {
			return a.fanart(gtx, fanartKey, fanartFn)
		}),
		layout.Expanded(func(gtx C) D {
			scrim(gtx)
			return D{Size: gtx.Constraints.Max}
		}),
		layout.Stacked(func(gtx C) D {
			return layout.Inset{Top: dp48, Bottom: dp24, Left: dp48, Right: dp48}.Layout(gtx, content)
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

	// Kick off async NFO fetch if not yet cached.
	nfoKey := "nfo:" + movieID
	a.nfos.fetch(nfoKey, func() (*polochon.Movie, error) {
		return a.client.fetchMovieNFO(movieID)
	})

	// Merge NFO data with API data — NFO wins for text fields.
	nfoData, _ := a.nfos.get(nfoKey)
	plot := movie.Plot
	tagline := movie.Tagline
	runtime := movie.Runtime
	genres := movie.Genres
	if nfoData != nil {
		if nfoData.Plot != "" {
			plot = nfoData.Plot
		}
		if nfoData.Tagline != "" {
			tagline = nfoData.Tagline
		}
		if nfoData.Runtime != 0 {
			runtime = nfoData.Runtime
		}
		if len(nfoData.Genres) > 0 {
			genres = nfoData.Genres
		}
	}

	// Info column sections (right of the poster).
	var sections []func(C) D

	sections = append(sections, func(gtx C) D {
		return a.body(gtx, spTitle, d.movieTitle, colorFg, 0)
	})
	sections = append(sections, spacerFn(dp8))

	if tagline != "" {
		tl := tagline
		sections = append(sections, func(gtx C) D {
			lbl := material.Label(a.th, spBody, tl)
			lbl.Color = colorSubtle
			lbl.Font.Style = font.Italic
			lbl.MaxLines = 0
			return lbl.Layout(gtx)
		})
		sections = append(sections, spacerFn(dp8))
	}

	if metaLine := buildMetaLine(movie.Year, runtime, genres); metaLine != "" {
		sections = append(sections, func(gtx C) D {
			return a.label(gtx, spMeta, metaLine, colorSubtle, 0)
		})
		sections = append(sections, spacerFn(dp12))
	}

	if badges := a.buildMovieBadges(movie); len(badges) > 0 {
		sections = append(sections, func(gtx C) D {
			return a.renderBadgeRow(gtx, badges)
		})
		sections = append(sections, spacerFn(dp20))
	}

	if plot != "" {
		p := plot
		sections = append(sections, func(gtx C) D {
			return a.body(gtx, spBody, p, colorFg, 0)
		})
		sections = append(sections, spacerFn(dp16))
	}

	subText := formatSubtitles(movie.EmbeddedSubtitles, movie.Subtitles)
	sections = append(sections, func(gtx C) D {
		return a.body(gtx, spBody, subText, colorFg, 0)
	})
	sections = append(sections, spacerFn(dp12))

	if movie.ReleaseGroup != "" {
		rg := movie.ReleaseGroup
		sections = append(sections, func(gtx C) D {
			return a.renderKV(gtx, "Release", rg)
		})
	}
	if dt := formatDateAdded(movie.DateAdded); dt != "" {
		sections = append(sections, func(gtx C) D {
			return a.renderKV(gtx, "Added", dt)
		})
	}

	movieKey := movieMediaKey(movieID)
	if pos, ok := a.resumePos[movieKey]; ok && pos > minimumResumePosition {
		p := pos
		sections = append(sections, func(gtx C) D {
			return a.renderKV(gtx, "Resume", formatResumePos(p))
		})
	}

	sections = append(sections, spacerFn(dp12))
	sections = append(sections, func(gtx C) D {
		return a.renderPlayerStatus(gtx)
	})

	return a.detailShell(gtx, "fanart:"+movieID, func() (image.Image, error) {
		return a.client.fetchMovieImage(movieID, func(m *papi.Movie) *papi.File { return m.FanartFile })
	}, func(gtx C) D {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Start}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return a.posterImage(gtx, "thumb:"+id, func() (image.Image, error) {
					return a.client.fetchMovieImage(id, func(m *papi.Movie) *papi.File { return m.ThumbFile })
				}, unit.Dp(280), radiusPoster)
			}),
			layout.Rigid(layout.Spacer{Width: dp32}.Layout),
			layout.Flexed(1, func(gtx C) D {
				return a.detailList.Layout(gtx, len(sections), func(gtx C, i int) D {
					return sections[i](gtx)
				})
			}),
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

func (a *App) buildMovieBadges(movie *papi.Movie) []badge {
	var badges []badge
	if movie.Size > 0 {
		badges = append(badges, badge{text: humanize.Bytes(uint64(movie.Size)), bg: colorSizeBadgeBg})
	}
	if q := formatQualityBadge(movie.Quality); q != "" {
		badges = append(badges, badge{text: q, bg: colorQualBadgeBg})
	}
	if c := formatCodecs(movie.VideoCodec, movie.AudioCodec); c != "" {
		badges = append(badges, badge{text: c, bg: colorCodecBadgeBg})
	}
	if ct := formatContainer(movie.Container); ct != "" {
		badges = append(badges, badge{text: ct, bg: colorBadgeBg})
	}
	return badges
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
