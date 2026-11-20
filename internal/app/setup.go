package app

import (
	"fmt"

	gioapp "gioui.org/app"
	"gioui.org/font"
	"gioui.org/io/system"
	"gioui.org/layout"
)

// setupScreen replaces the whole UI when pépé cannot start because its
// configuration is missing or invalid. Desktop launches (Finder, menu entries)
// have no visible stderr, so the problem must be shown in the window.
type setupScreen struct {
	configPath string
	problem    string
	created    bool // the config file was just written from the template
}

// NewSetup returns an App that only shows a configuration problem.
func NewSetup(configPath string, problem error, created bool, invalidate, closeWindow func()) *App {
	a := New(nil, nil, nil, invalidate, closeWindow)
	a.setup = &setupScreen{configPath: configPath, problem: problem.Error(), created: created}
	return a
}

// RunSetup drives the configuration problem screen in a Gio window.
func RunSetup(w *gioapp.Window, configPath string, problem error, created bool) error {
	a := NewSetup(configPath, problem, created, w.Invalidate, func() { w.Perform(system.ActionClose) })
	defer a.Close()
	return a.run(w.Event)
}

func (a *App) handleSetupAction(action inputAction) {
	if (action == actionQuit || action == actionBack) && a.closeWindow != nil {
		a.closeWindow()
	}
}

func setupHints() []hint {
	return []hint{{key: "q", label: "quit"}}
}

func (a *App) layoutSetup(gtx C) D {
	s := a.setup
	created := ""
	if s.created {
		created = "A new config file was created from the example. "
	}
	rows := []layout.FlexChild{
		layout.Rigid(func(gtx C) D { return a.label(gtx, spTitle, "Set up pépé", colorFg, font.Bold) }),
		layout.Rigid(spacerFn(dp24)),
		layout.Rigid(func(gtx C) D { return a.prose(gtx, s.problem) }),
		layout.Rigid(spacerFn(dp32)),
		layout.Rigid(func(gtx C) D { return a.label(gtx, spMeta, "Config file", colorSubtle, font.Medium) }),
		layout.Rigid(spacerFn(dp8)),
		layout.Rigid(func(gtx C) D { return a.body(gtx, spBody, s.configPath, colorFg, 0) }),
		layout.Rigid(spacerFn(dp24)),
		layout.Rigid(func(gtx C) D {
			return a.prose(gtx, fmt.Sprintf("%sSet polochon_url and polochon_token to your polochon server, then start pépé again.", created))
		}),
	}
	return layout.Inset{Top: dp48, Left: a.viewportInsets.Left, Right: a.viewportInsets.Right}.Layout(gtx, func(gtx C) D {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(proseWidth))
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
	})
}
