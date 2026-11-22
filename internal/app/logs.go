package app

import (
	"fmt"
	"image/color"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/unit"
)

// logsPage shows the in-memory log, newest at the bottom. The list follows
// new entries until the user scrolls up.
type logsPage struct {
	list    layout.List
	entries []logEntry // snapshot taken by the last frame
}

func (a *App) openLogs() {
	a.browse.closePage()
	a.browse.logs = &logsPage{list: layout.List{Axis: layout.Vertical, ScrollToEnd: true}}
}

func (p *logsPage) scroll(delta int) {
	p.list.ScrollBy(float32(delta))
}

func (p *logsPage) summary() string {
	if len(p.entries) == 1 {
		return "1 entry"
	}
	return fmt.Sprintf("%d entries", len(p.entries))
}

// syncLogs snapshots the log once per frame, before the header and page use it.
func (a *App) syncLogs() {
	if page := a.browse.logs; page != nil {
		a.logsVisible.Store(true)
		a.logs.consumeChange()
		page.entries, _, _ = a.logs.since(0)
	}
}

func (a *App) layoutLogs(gtx C) D {
	page := a.browse.logs
	return layout.UniformInset(dp24).Layout(gtx, func(gtx C) D {
		if len(page.entries) == 0 {
			return layout.Center.Layout(gtx, func(gtx C) D {
				return a.body(gtx, spBody, "No log entries yet.", colorSubtle, 0)
			})
		}
		gtx.Constraints.Min = gtx.Constraints.Max
		dims := page.list.Layout(gtx, len(page.entries), func(gtx C, i int) D {
			return a.layoutLogEntry(gtx, page.entries[i])
		})
		if a.browse.focus == browseFocusContent {
			defer a.markSelected(gtx, dims.Size).Pop()
		}
		return dims
	})
}

func (a *App) layoutLogEntry(gtx C, entry logEntry) D {
	levelColor := colorSubtle
	switch entry.Level {
	case "ERROR":
		levelColor = colorError
	case "WARN":
		levelColor = colorWarn
	}
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return layout.Inset{Top: dp8, Bottom: dp8}.Layout(gtx, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				meta := func(text string, col color.NRGBA, weight font.Weight) layout.FlexChild {
					return layout.Rigid(func(gtx C) D {
						return layout.Inset{Right: dp12}.Layout(gtx, func(gtx C) D {
							return a.label(gtx, spTiny, text, col, weight)
						})
					})
				}
				return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
					meta(entry.Time.Format("15:04:05"), colorSubtle, 0),
					meta(entry.Level, levelColor, font.Bold),
					meta(entry.Source, colorSubtle, font.Medium),
				)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(2)}.Layout),
			layout.Rigid(func(gtx C) D {
				return a.body(gtx, sp, entry.Message, colorFg, 0)
			}),
		)
	})
}
