package app

import (
	gioapp "gioui.org/app"
	"gioui.org/font"
	"gioui.org/io/system"
	"gioui.org/layout"
)

// setupScreen replaces the whole UI when pépé cannot start because its
// configuration is missing or invalid. Desktop launches (Finder, menu entries)
// have no visible stderr, so the problem is shown in the window, with the
// settings form to fix it.
type setupScreen struct {
	problem string
	created bool // the config file was just written from the template
	form    *settingsForm
}

// NewSetup returns an App that shows a configuration problem and the
// settings form editing source.
func NewSetup(source ConfigSource, problem error, created bool, invalidate, closeWindow func()) *App {
	a := New(nil, nil, nil, invalidate, closeWindow)
	a.configSource = source
	a.setup = &setupScreen{problem: problem.Error(), created: created, form: newSettingsForm(source)}
	return a
}

// RunSetup drives the configuration problem screen in a Gio window. It returns
// ErrRestart once valid settings were saved.
func RunSetup(w *gioapp.Window, source ConfigSource, problem error, created bool) error {
	a := NewSetup(source, problem, created, w.Invalidate, func() { w.Perform(system.ActionClose) })
	defer a.Close()
	return a.runResult(a.run(w.Event))
}

func (a *App) handleSetupAction(action inputAction) {
	if a.settingsAction(a.setup.form, action) {
		return
	}
	if (action == actionQuit || action == actionBack) && a.closeWindow != nil {
		a.closeWindow()
	}
}

func (a *App) setupHints() []hint {
	hints := settingsHints(a.setup.form)
	if a.setup.form.editing >= 0 {
		// The edited field receives q as text.
		return hints
	}
	return append(hints, hint{key: "q", label: "quit"})
}

func (a *App) layoutSetup(gtx C) D {
	s := a.setup
	intro := "Fill in your Polochon server, then save to start pépé."
	if s.created {
		intro = "A new config file was created from the example. " + intro
	}
	// Keep the introduction compact so the whole form fits a 16:9 screen.
	return layout.Inset{Top: a.viewportInsets.Top, Left: a.viewportInsets.Left, Right: a.viewportInsets.Right}.Layout(gtx, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D { return a.label(gtx, spTitle, "Set up pépé", colorFg, font.Bold) }),
			layout.Rigid(spacerFn(dp16)),
			layout.Rigid(func(gtx C) D { return a.prose(gtx, s.problem) }),
			layout.Rigid(func(gtx C) D { return a.prose(gtx, intro) }),
			layout.Rigid(spacerFn(dp8)),
			layout.Rigid(func(gtx C) D {
				return a.body(gtx, spMeta, "Config file: "+s.form.summary(), colorSubtle, 0)
			}),
			layout.Rigid(spacerFn(dp16)),
			layout.Flexed(1, func(gtx C) D { return a.layoutSettingsForm(gtx, s.form, true) }),
		)
	})
}
