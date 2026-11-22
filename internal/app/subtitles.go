package app

import (
	"image"
	"image/color"
	"path/filepath"
	"time"

	"gioui.org/font"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	polochon "github.com/odwrtw/polochon/lib"
)

const subtitleDoneDuration = 1500 * time.Millisecond

// ── Subtitle popup ────────────────────────────────────────────────────────────

// currentVideo returns the polochon.Video for the currently highlighted item, or nil.
func (a *App) currentVideo() polochon.Video {
	if d := a.detail; d != nil {
		if d.kind == detailMovie {
			movie, ok := a.client.getMovie(d.movieID)
			if !ok {
				return nil
			}
			return movie
		}
		if ep := d.show.currentEpisode(); ep != nil && ep.Episode != nil {
			return ep.Episode
		}
		return nil
	}

	if a.browse.onPage() {
		return nil
	}
	media := a.selectedBrowseMedia()
	if media == nil || media.Type != mediaKindMovie {
		return nil
	}
	movie, ok := a.client.getMovie(media.ID)
	if !ok {
		return nil
	}
	return movie
}

func (a *App) openSubtitlePopup() {
	video := a.currentVideo()
	if video == nil {
		return
	}
	popup := &SubtitlePopup{
		video:    video,
		lang:     polochon.EN,
		state:    SubtitlePopupLoading,
		resultCh: make(chan subPopupListResult, 1),
		list:     layout.List{Axis: layout.Vertical},
	}
	a.subPopup = popup

	go func() {
		entries, err := a.client.listAvailableSubtitles(video, polochon.EN)
		popup.resultCh <- subPopupListResult{entries: entries, err: err}
		if a.invalidate != nil {
			a.invalidate()
		}
	}()
}

func (a *App) closeSubtitlePopup() {
	a.subPopup = nil
}

func (a *App) drainSubtitlePopup() {
	if a.subPopup == nil {
		return
	}

	select {
	case result := <-a.subPopup.resultCh:
		if result.err != nil {
			a.subPopup.state = SubtitlePopupError
			a.subPopup.errMsg = result.err.Error()
		} else {
			a.subPopup.state = SubtitlePopupReady
			a.subPopup.entries = result.entries
		}
	default:
	}

	if a.subPopup.downloadDone != nil {
		select {
		case result := <-a.subPopup.downloadDone:
			if result.err != nil {
				a.subPopup.state = SubtitlePopupError
				a.subPopup.errMsg = result.err.Error()
			} else {
				a.subPopup.state = SubtitlePopupDone
				a.subPopup.doneAt = time.Now()
			}
			a.subPopup.downloadDone = nil
		default:
		}
	}

	if a.subPopup.state == SubtitlePopupDone && time.Since(a.subPopup.doneAt) > subtitleDoneDuration {
		a.subPopup = nil
	}
}

func (a *App) subPopupSelect() {
	p := a.subPopup
	if p == nil {
		return
	}
	switch p.state {
	case SubtitlePopupDone, SubtitlePopupError:
		a.closeSubtitlePopup()
	case SubtitlePopupReady:
		if len(p.entries) == 0 {
			return
		}
		entry := p.entries[p.cursor]
		p.state = SubtitlePopupDownloading
		p.downloadDone = make(chan subPopupDownloadResult, 1)
		go func() {
			err := a.client.downloadSubtitle(p.video, p.lang, entry)
			p.downloadDone <- subPopupDownloadResult{err: err}
			if a.invalidate != nil {
				a.invalidate()
			}
		}()
	}
}

// layoutSubtitlePopup draws the full-screen dimmed overlay with the popup box.
func (a *App) layoutSubtitlePopup(gtx C) D {
	p := a.subPopup
	if p == nil {
		return D{}
	}

	// The list and download goroutines invalidate when they finish; only the
	// auto-close needs a scheduled frame.
	if p.state == SubtitlePopupDone && !p.doneAt.IsZero() {
		gtx.Execute(op.InvalidateCmd{At: p.doneAt.Add(subtitleDoneDuration)})
	}

	// Dim the whole screen. The popup is modal: its handler is foremost, so
	// pointer input never reaches detail or browse targets beneath it.
	defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
	for {
		if _, ok := gtx.Event(pointer.Filter{Target: p, Kinds: pointer.Press | pointer.Release | pointer.Scroll}); !ok {
			break
		}
	}
	event.Op(gtx.Ops, p)
	paint.Fill(gtx.Ops, color.NRGBA{A: 0xb0})

	// Center a box: 60% width, up to 70% height.
	maxW := gtx.Constraints.Max.X * 6 / 10
	maxH := gtx.Constraints.Max.Y * 7 / 10
	boxX := (gtx.Constraints.Max.X - maxW) / 2
	boxY := (gtx.Constraints.Max.Y - maxH) / 2

	// Draw box background.
	boxRect := image.Rectangle{
		Min: image.Pt(boxX, boxY),
		Max: image.Pt(boxX+maxW, boxY+maxH),
	}
	r := gtx.Dp(radiusCard)
	boxStack := clip.RRect{
		Rect: boxRect,
		NW:   r, NE: r, SW: r, SE: r,
	}.Push(gtx.Ops)
	paint.Fill(gtx.Ops, colorSurface2)
	boxStack.Pop()

	// Lay out content inside the box.
	boxGtx := gtx
	boxGtx.Constraints = layout.Exact(image.Pt(maxW, maxH))
	macro := op.Record(gtx.Ops)
	a.layoutSubtitlePopupContent(boxGtx, p)
	call := macro.Stop()
	offset := op.Offset(image.Pt(boxX, boxY)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	offset.Pop()

	return D{Size: gtx.Constraints.Max}
}

func (a *App) layoutSubtitlePopupContent(gtx C, p *SubtitlePopup) D {
	filename := ""
	if f := p.video.GetFile(); f != nil && f.Path != "" {
		filename = filepath.Base(f.Path)
	}

	inset := layout.UniformInset(dp24)
	return inset.Layout(gtx, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return a.label(gtx, sp, "Subtitles — English", colorPrimary, font.Bold)
			}),
			layout.Rigid(func(gtx C) D {
				if filename == "" {
					return D{}
				}
				return layout.Inset{Top: dp4}.Layout(gtx, func(gtx C) D {
					return a.label(gtx, spTiny, filename, colorSubtle, 0)
				})
			}),
			layout.Rigid(layout.Spacer{Height: dp16}.Layout),
			layout.Flexed(1, func(gtx C) D {
				return a.layoutSubtitlePopupBody(gtx, p)
			}),
		)
	})
}

func (a *App) layoutSubtitlePopupBody(gtx C, p *SubtitlePopup) D {
	switch p.state {
	case SubtitlePopupLoading:
		return a.label(gtx, sp, "Searching…", colorFg, 0)
	case SubtitlePopupDownloading:
		return a.label(gtx, sp, "Downloading…", colorFg, 0)
	case SubtitlePopupDone:
		return a.label(gtx, spTitle, "Downloaded!", colorPrimary, font.Bold)
	case SubtitlePopupError:
		return a.body(gtx, sp, "Error: "+p.errMsg, colorTerminal, 0)
	case SubtitlePopupReady:
		if len(p.entries) == 0 {
			return a.label(gtx, sp, "No subtitles found", colorFg, 0)
		}
		return a.focusList(gtx, &p.list, &p.motion, p, len(p.entries), p.cursor, true, rowFocus,
			func(gtx C, i int) D { return a.drawSubtitleEntry(gtx, p.entries[i], i == p.cursor) })
	}
	return D{}
}

// drawSubtitleEntry renders one subtitle result row as two aligned columns:
// left = Source (fixed 180dp), right = Release (fills remaining width).
func (a *App) drawSubtitleEntry(gtx C, entry *polochon.SubtitleEntry, selected bool) D {
	const sourceColDp = unit.Dp(180)

	return a.focusRowShell(gtx, selected, false, browseRowHeight, func(gtx C) D {
		fg := colorFg
		fgSecondary := colorSubtle
		if selected {
			fg = colorSelectFg
			fgSecondary = colorSelectFg
		}
		return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				// Fixed-width source column — pin X only, let Y be natural.
				colW := gtx.Dp(sourceColDp)
				gtx.Constraints.Min.X = colW
				gtx.Constraints.Max.X = colW
				weight := font.Normal
				if selected {
					weight = font.Bold
				}
				return a.label(gtx, sp, entry.Source, fgSecondary, weight)
			}),
			layout.Flexed(1, func(gtx C) D {
				weight := font.Normal
				if selected {
					weight = font.Bold
				}
				return a.label(gtx, sp, entry.Description, fg, weight)
			}),
		)
	})
}
