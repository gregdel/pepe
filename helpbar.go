package main

import (
	"gioui.org/layout"
)

// ── Help bar ──────────────────────────────────────────────────────────────────

// currentHints returns the context-aware key hints for the help bar.
func (a *App) currentHints() []hint {
	if a.subPopup != nil {
		return []hint{
			{key: "↑↓", label: "navigate"},
			{key: "enter", label: "download"},
			{key: "esc", label: "close"},
		}
	}

	if d := a.detail; d != nil {
		switch d.kind {
		case detailMovie:
			return []hint{
				{key: "enter", label: "play"},
				{key: "s", label: "subtitles"},
				{key: "esc", label: "back"},
			}
		case detailShow:
			return []hint{
				{key: "↑↓←→", label: "navigate"},
				{key: "enter", label: "play episode"},
				{key: "s", label: "subtitles"},
				{key: "esc", label: "back"},
			}
		}
	}

	if a.nav.isAtRoot() {
		return []hint{
			{key: "↑↓", label: "navigate"},
			{key: "enter", label: "browse"},
			{key: "r", label: "refresh"},
			{key: "q", label: "quit"},
		}
	}

	hints := []hint{
		{key: "↑↓←→", label: "navigate"},
		{key: "enter", label: "select"},
		{key: "esc", label: "back"},
		{key: "tab", label: "section"},
	}
	if a.currentVideo() != nil {
		hints = append(hints, hint{key: "s", label: "subtitles"})
	}
	hints = append(hints,
		hint{key: "r", label: "refresh"},
		hint{key: "q", label: "quit"},
	)
	return hints
}

func (a *App) layoutHelp(gtx C) D {
	hints := a.currentHints()

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(hairline),
		layout.Rigid(func(gtx C) D {
			return layout.Inset{Left: dp16, Right: dp16, Top: dp8, Bottom: dp8}.Layout(gtx, func(gtx C) D {
				children := make([]layout.FlexChild, 0, len(hints)*2)
				for i, h := range hints {
					if i > 0 {
						children = append(children, layout.Rigid(layout.Spacer{Width: dp20}.Layout))
					}
					h := h // capture
					children = append(children, layout.Rigid(func(gtx C) D {
						return a.hintItem(gtx, h)
					}))
				}
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
			})
		}),
	)
}
