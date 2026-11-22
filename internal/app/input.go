package app

import (
	"slices"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/layout"
)

type inputAction int

const (
	actionUnknown inputAction = iota
	actionUp
	actionDown
	actionLeft
	actionRight
	actionActivate
	actionBack
	actionHome
	actionToggleSection
	actionRefresh
	actionSubtitles
	actionToggleWatched
	actionQuit
)

type keyBinding struct {
	name     key.Name
	action   inputAction
	optional key.Modifiers
}

var keyBindings = []keyBinding{
	{name: key.NameUpArrow, action: actionUp},
	{name: key.NameDownArrow, action: actionDown},
	{name: key.NameLeftArrow, action: actionLeft},
	{name: key.NameRightArrow, action: actionRight},
	{name: key.NameReturn, action: actionActivate},
	{name: key.NameEnter, action: actionActivate},
	{name: key.NameEscape, action: actionBack},
	{name: key.NameHome, action: actionHome},
	// CEC colours use the same S/R/Tab/W bindings as the keyboard.
	{name: "H", action: actionLeft, optional: key.ModShift},
	{name: "J", action: actionDown, optional: key.ModShift},
	{name: "K", action: actionUp, optional: key.ModShift},
	{name: "L", action: actionRight, optional: key.ModShift},
	{name: "Q", action: actionQuit, optional: key.ModShift},
	{name: "R", action: actionRefresh, optional: key.ModShift},
	{name: "S", action: actionSubtitles, optional: key.ModShift},
	{name: "W", action: actionToggleWatched, optional: key.ModShift},
	{name: key.NameTab, action: actionToggleSection},
}

// editKeys leave or commit an edited settings field; every other key, letters
// and Left/Right/Home included, belongs to the focused editor.
var editKeys = []key.Name{key.NameUpArrow, key.NameDownArrow, key.NameReturn, key.NameEnter, key.NameEscape, key.NameTab}

// keyFilters scopes the bindings to the focus tag, keeping only editKeys while
// a settings field is edited. Gio delivers a key to any matching filter instead
// of inserting its text, so the bindings must not match while an editor has
// focus.
func keyFilters(focus event.Tag, editing bool) []event.Filter {
	filters := make([]event.Filter, 0, len(keyBindings))
	for _, binding := range keyBindings {
		if !editing || slices.Contains(editKeys, binding.name) {
			filters = append(filters, key.Filter{Focus: focus, Name: binding.name, Optional: binding.optional})
		}
	}
	return filters
}

func actionForKey(name key.Name) inputAction {
	for _, binding := range keyBindings {
		if binding.name == name {
			return binding.action
		}
	}
	return actionUnknown
}

// handleKeys reads bindings scoped to keyTag, which holds focus unless a
// settings field is edited (syncSettingsEditor focuses the editor). Filters
// are rebuilt per key: Gio replays the keys after a focus change under the new
// focus.
func (a *App) handleKeys(gtx layout.Context) {
	for {
		// Reading its FocusFilter every frame keeps keyTag focusable.
		filters := []event.Filter{key.FocusFilter{Target: a.keyTag}}
		if form := a.activeSettings(); form != nil && form.editing >= 0 {
			filters = append(filters, keyFilters(&form.editor, true)...)
		} else {
			if !gtx.Focused(a.keyTag) {
				gtx.Execute(key.FocusCmd{Tag: a.keyTag})
			}
			filters = append(filters, keyFilters(a.keyTag, false)...)
		}
		e, ok := gtx.Event(filters...)
		if !ok {
			return
		}
		ke, ok := e.(key.Event)
		if !ok || ke.State != key.Press || ke.Modifiers&^key.ModShift != 0 {
			continue
		}
		a.dispatchAction(actionForKey(ke.Name))
	}
}

func (a *App) dispatchAction(action inputAction) {
	if action == actionUnknown {
		return
	}
	if a.setup != nil {
		a.handleSetupAction(action)
		return
	}
	if a.subPopup != nil {
		a.handlePopupAction(action)
		return
	}
	if a.detail != nil {
		a.handleDetailAction(action)
		return
	}
	a.handleBrowseAction(action)
}

func (a *App) handlePopupAction(action inputAction) {
	switch action {
	case actionUp:
		if a.subPopup.cursor > 0 {
			a.subPopup.cursor--
		}
	case actionDown:
		if a.subPopup.state == SubtitlePopupReady && a.subPopup.cursor < len(a.subPopup.entries)-1 {
			a.subPopup.cursor++
		}
	case actionActivate:
		a.subPopupSelect()
	case actionBack, actionLeft, actionHome:
		a.closeSubtitlePopup()
	}
}

func (a *App) handleBrowseAction(action inputAction) {
	if form := a.activeSettings(); form != nil && a.settingsAction(form, action) {
		return
	}
	switch action {
	case actionUp:
		a.navigateUp()
	case actionDown:
		a.navigateDown()
	case actionLeft:
		a.navigateLeft()
	case actionBack:
		a.navigateEscape()
	case actionHome:
		a.navigateHome()
	case actionRight, actionActivate:
		a.navigateRight()
	case actionToggleSection:
		a.toggleSection()
	case actionRefresh:
		a.startRefresh()
	case actionSubtitles:
		a.openSubtitlePopup()
	case actionToggleWatched:
		a.toggleWatched()
	case actionQuit:
		if a.closeWindow != nil {
			a.closeWindow()
		}
	}
}

func (a *App) handleDetailAction(action inputAction) {
	detail := a.detail
	if detail.kind == detailShow {
		a.handleShowAction(action, detail.show)
		return
	}

	switch action {
	case actionActivate:
		a.launchMovie(&MediaItem{ID: detail.movieID, Title: detail.movieTitle, Type: mediaKindMovie})
	case actionLeft, actionBack:
		a.popDetail()
	case actionHome:
		a.navigateHome()
	case actionUp:
		a.scrollDetail(-1)
	case actionDown:
		a.scrollDetail(1)
	case actionSubtitles:
		a.openSubtitlePopup()
	case actionToggleWatched:
		a.toggleWatched()
	case actionQuit:
		if a.closeWindow != nil {
			a.closeWindow()
		}
	}
}

func (a *App) handleShowAction(action inputAction, show *showPage) {
	switch action {
	case actionUp:
		show.up()
	case actionDown:
		show.down()
	case actionBack:
		a.popDetail()
	case actionHome:
		a.navigateHome()
	case actionLeft:
		if show.focus == focusTabs {
			if show.seasonCursor == 0 {
				a.popDetail()
				return
			}
			show.left()
		} else {
			a.popDetail()
		}
	case actionRight:
		if show.focus == focusTabs {
			show.right()
		}
	case actionActivate:
		if show.focus == focusTabs {
			show.focus = focusEpisodes
			return
		}
		if episode := show.currentEpisode(); episode != nil {
			a.launchEpisode(episode)
		}
	case actionSubtitles:
		a.openSubtitlePopup()
	case actionToggleWatched:
		a.toggleWatched()
	case actionQuit:
		if a.closeWindow != nil {
			a.closeWindow()
		}
	}
}
