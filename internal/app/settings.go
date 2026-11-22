package app

import (
	"errors"
	"image"
	"image/color"
	"slices"
	"strconv"
	"strings"

	"gioui.org/font"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// ErrRestart is returned by Run and RunSetup after the settings were saved:
// the caller restarts pépé so every setting takes effect.
var ErrRestart = errors.New("restart requested after saving the settings")

// ConfigSource is where the settings form reads and saves the configuration.
type ConfigSource struct {
	Path string
	// Overrides maps config keys to the environment variable or flag that
	// replaces the file value, so a saved value is not silently ignored.
	Overrides map[string]string
}

type settingKind int

const (
	settingText settingKind = iota
	settingToggle
	settingChoice
)

type settingRow struct {
	key, label string
	kind       settingKind
	hint       string    // text rows: shown when empty
	choices    [2]string // choice rows
	// field returns a pointer to the Config field: *string, *bool,
	// *ControlProfile, or **bool where nil means true.
	field func(*Config) any
}

// get returns the row's value as written to the config file: a string or bool.
func (r settingRow) get(c *Config) any {
	switch p := r.field(c).(type) {
	case *string:
		return *p
	case *ControlProfile:
		return string(*p)
	case *bool:
		return *p
	case **bool:
		return *p == nil || **p
	}
	panic("unsupported setting field for " + r.key)
}

// set stores a form value, "true" or "false" for toggles.
func (r settingRow) set(c *Config, value string) {
	switch p := r.field(c).(type) {
	case *string:
		*p = value
	case *ControlProfile:
		*p = ControlProfile(value)
	case *bool:
		*p = value == "true"
	case **bool:
		on := value == "true"
		*p = &on
	default:
		panic("unsupported setting field for " + r.key)
	}
}

// settingRows are the everyday settings, in the order they are added to the
// config file; advanced player settings stay in the file.
var settingRows = []settingRow{
	{key: "polochon_url", label: "Polochon URL", hint: "https://polochon.example",
		field: func(c *Config) any { return &c.PolochonURL }},
	{key: "polochon_token", label: "Polochon token", hint: "Not set",
		field: func(c *Config) any { return &c.Token }},
	{key: "mpv_path", label: "mpv path", hint: "mpv",
		field: func(c *Config) any { return &c.MPVPath }},
	{key: "fullscreen", label: "Fullscreen window", kind: settingToggle,
		field: func(c *Config) any { return &c.Fullscreen }},
	{key: "player_fullscreen", label: "Fullscreen playback", kind: settingToggle,
		field: func(c *Config) any { return &c.PlayerFullscreen }},
	{key: "controls", label: "Controls", kind: settingChoice, choices: [2]string{string(ControlsKeyboard), string(ControlsHDMICEC)},
		field: func(c *Config) any { return &c.Controls }},
	{key: "help_bar", label: "Help bar", kind: settingToggle,
		field: func(c *Config) any { return &c.HelpBar }},
	{key: "mpv_osd", label: "Playback overlay", kind: settingChoice, choices: [2]string{"pepe", "native"},
		field: func(c *Config) any { return &c.MPVOSD }},
	{key: "save_progress", label: "Remember progress", kind: settingToggle,
		field: func(c *Config) any { return &c.SaveProgress }},
	{key: "web_ui_addr", label: "Web remote address", hint: "Disabled (for example :8081)",
		field: func(c *Config) any { return &c.WebUIAddr }},
}

// settingsSaveRow is the cursor position of the Save & restart button.
var settingsSaveRow = len(settingRows)

// settingsForm edits the config file's values, never the effective
// configuration: environment and flag overrides must not be written back.
type settingsForm struct {
	source   ConfigSource
	file     *Config  // file values the form starts from
	values   []string // one per settingRows entry
	saved    []string // values as read, to report unsaved changes
	readOnly bool

	cursor       int
	editing      int // row being edited, -1 when none; values change on commit
	editor       widget.Editor
	focusEditor  bool // focus the editor during the next layout
	hideKeyboard bool // hide the soft keyboard during the next layout

	message string
	failed  bool // message reports an error

	list   layout.List
	motion listMotion
}

func newSettingsForm(source ConfigSource) *settingsForm {
	f := &settingsForm{source: source, editing: -1, list: layout.List{Axis: layout.Vertical}}
	f.editor.SingleLine = true
	cfg := DefaultConfig()
	if source.Path == "" {
		f.readOnly = true
		f.message = "No config file: settings are read-only."
	} else if file, err := ReadConfigFile(source.Path); err != nil {
		f.readOnly, f.failed = true, true
		f.message = err.Error() + ". Fix or remove the file, then start pépé again."
	} else {
		cfg = file
	}
	f.file = cfg
	for _, row := range settingRows {
		f.values = append(f.values, settingString(row.get(cfg)))
	}
	f.saved = slices.Clone(f.values)
	return f
}

func settingString(value any) string {
	if v, ok := value.(bool); ok {
		return strconv.FormatBool(v)
	}
	return value.(string)
}

func (f *settingsForm) dirty() bool { return !slices.Equal(f.values, f.saved) }

// config returns the file configuration with the form's values.
func (f *settingsForm) config() *Config {
	cfg := *f.file
	for i, row := range settingRows {
		row.set(&cfg, f.values[i])
	}
	cfg.normalize()
	return &cfg
}

func (f *settingsForm) setMessage(message string, failed bool) {
	f.message, f.failed = message, failed
}

// setValue changes a value and clears a stale save result.
func (f *settingsForm) setValue(i int, value string) {
	if f.values[i] != value {
		f.values[i] = value
		f.setMessage("", false)
	}
}

func (f *settingsForm) move(delta int) {
	f.cursor = min(max(f.cursor+delta, 0), settingsSaveRow)
}

func (f *settingsForm) startEdit(i int) {
	f.editing = i
	f.editor.SetText(f.values[i])
	n := f.editor.Len()
	f.editor.SetCaret(n, n)
	f.focusEditor = true
}

// commitEdit keeps the edited text, trimmed like values read from the file.
func (f *settingsForm) commitEdit() {
	if f.editing < 0 {
		return
	}
	f.setValue(f.editing, strings.TrimSpace(f.editor.Text()))
	f.endEdit()
}

// endEdit leaves the field, discarding the editor text unless commitEdit kept it.
func (f *settingsForm) endEdit() {
	f.editing = -1
	f.focusEditor = false
	f.hideKeyboard = true
	f.editor.SetText("")
}

// activeSettings returns the form receiving input, if any.
func (a *App) activeSettings() *settingsForm {
	if a.setup != nil {
		return a.setup.form
	}
	if a.browse.settings != nil && a.browse.focus == browseFocusContent && a.detail == nil {
		return a.browse.settings
	}
	return nil
}

// settingsEditing reports whether a text field owns the keyboard, so typed
// letters must not reach the global key bindings.
func (a *App) settingsEditing() bool {
	form := a.activeSettings()
	return form != nil && form.editing >= 0
}

// openSettings shows the Settings rail page, restoring unsaved edits from when
// it was last closed; otherwise it reads the file again.
func (a *App) openSettings() {
	if a.browse.settings != nil {
		return
	}
	a.browse.closePage()
	form := a.browse.settingsDraft
	if form == nil {
		form = newSettingsForm(a.configSource)
	}
	a.browse.settingsDraft = nil
	a.browse.settings = form
}

// settingsAction handles action on form and reports whether it consumed it.
// While a field is edited every action is consumed.
func (a *App) settingsAction(form *settingsForm, action inputAction) bool {
	if form.editing >= 0 {
		switch action {
		case actionActivate:
			form.commitEdit()
		case actionBack:
			form.endEdit()
		case actionUp:
			form.commitEdit()
			form.move(-1)
		case actionDown, actionToggleSection:
			form.commitEdit()
			form.move(1)
		}
		return true
	}
	switch action {
	case actionUp:
		form.move(-1)
	case actionDown:
		form.move(1)
	case actionActivate:
		a.activateSetting(form, form.cursor)
	default:
		return false
	}
	return true
}

// clickSetting focuses and activates a row with the pointer.
func (a *App) clickSetting(form *settingsForm, i int) {
	if a.setup == nil {
		a.browse.focus = browseFocusContent
	}
	if form.editing == i {
		return
	}
	form.commitEdit()
	form.cursor = i
	a.activateSetting(form, i)
}

func (a *App) activateSetting(form *settingsForm, i int) {
	if form.readOnly {
		return
	}
	if i == settingsSaveRow {
		a.saveSettings(form)
		return
	}
	row := settingRows[i]
	switch row.kind {
	case settingText:
		form.startEdit(i)
	case settingToggle:
		form.setValue(i, strconv.FormatBool(form.values[i] != "true"))
	case settingChoice:
		next := row.choices[0]
		if form.values[i] == next {
			next = row.choices[1]
		}
		form.setValue(i, next)
	}
}

// saveSettings validates and writes the form, then asks the caller to restart.
func (a *App) saveSettings(form *settingsForm) {
	form.commitEdit()
	if form.readOnly {
		return
	}
	if a.playerStatus == PlayerStatusRunning {
		form.setMessage("Stop playback before saving.", true)
		return
	}
	cfg := form.config()
	effective := *cfg
	effective.applyEnv()
	if err := effective.Validate(); err != nil {
		form.setMessage(err.Error(), true)
		return
	}
	if err := SaveConfigFile(form.source.Path, cfg); err != nil {
		form.setMessage(err.Error(), true)
		return
	}
	form.saved = slices.Clone(form.values)
	form.setMessage("Saved. Restarting…", false)
	a.logger().Info("settings saved, restarting", "path", form.source.Path)
	a.restartRequested = true
	if a.closeWindow != nil {
		a.closeWindow()
	}
}

func settingsHints(form *settingsForm) []hint {
	if form.editing >= 0 {
		return []hint{{key: "enter", label: "done"}, {key: "esc", label: "cancel"}}
	}
	hints := []hint{{key: "↑↓", label: "move"}}
	if !form.readOnly {
		label := "change"
		if form.cursor == settingsSaveRow {
			label = "save & restart"
		} else if settingRows[form.cursor].kind == settingText {
			label = "edit"
		}
		hints = append(hints, hint{key: "enter", label: label})
	}
	return hints
}

func (f *settingsForm) summary() string {
	if f.source.Path == "" {
		return "No config file"
	}
	return f.source.Path
}

// ── Layout ────────────────────────────────────────────────────────────────────

const (
	settingsMaxWidth   = unit.Dp(1100)
	settingsLabelWidth = unit.Dp(380)
	settingsRowHeight  = unit.Dp(64)
)

func (a *App) layoutSettingsPage(gtx C) D {
	form := a.browse.settings
	if a.browse.focus != browseFocusContent {
		form.commitEdit()
	}
	return layout.UniformInset(dp24).Layout(gtx, func(gtx C) D {
		return a.layoutSettingsForm(gtx, form, a.browse.focus == browseFocusContent)
	})
}

// layoutSettingsForm draws the rows and the Save button in a scrolling list.
// focused is false while the mode rail has focus.
func (a *App) layoutSettingsForm(gtx C, form *settingsForm, focused bool) D {
	a.syncSettingsEditor(gtx, form)
	gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(settingsMaxWidth))
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return a.focusList(gtx, &form.list, &form.motion, form, settingsSaveRow+1, form.cursor, focused,
		focusStyle{paint: func(gtx C, i int, size image.Point) {
			// The Save button is its own pill, and a field being edited keeps
			// its neutral surface for the caret.
			if i != settingsSaveRow && form.editing != i {
				paintFocusRow(gtx, size)
			}
		}},
		func(gtx C, i int) D {
			if i == settingsSaveRow {
				return a.layoutSettingsSave(gtx, form, focused && form.cursor == i)
			}
			return a.layoutSettingRow(gtx, form, i, focused && form.cursor == i)
		})
}

// syncSettingsEditor applies pending focus changes and commits an edit whose
// editor lost focus to the pointer.
func (a *App) syncSettingsEditor(gtx C, form *settingsForm) {
	if form.focusEditor {
		form.focusEditor = false
		gtx.Execute(key.FocusCmd{Tag: &form.editor})
		gtx.Execute(key.SoftKeyboardCmd{Show: true})
	} else if form.editing >= 0 && !gtx.Focused(&form.editor) {
		form.commitEdit()
	}
	if form.hideKeyboard && form.editing < 0 {
		gtx.Execute(key.SoftKeyboardCmd{Show: false})
	}
	form.hideKeyboard = false
}

func (a *App) layoutSettingRow(gtx C, form *settingsForm, i int, selected bool) D {
	row := settingRows[i]
	editing := form.editing == i
	content := func(gtx C) D {
		fg, sub := colorFg, colorSubtle
		if selected && !editing {
			fg, sub = colorSelectFg, colorSelectFg
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				gtx.Constraints.Min.X = min(gtx.Dp(settingsLabelWidth), gtx.Constraints.Max.X)
				gtx.Constraints.Max.X = gtx.Constraints.Min.X
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx C) D { return a.label(gtx, spBody, row.label, fg, font.Medium) }),
					layout.Rigid(func(gtx C) D {
						source := form.source.Overrides[row.key]
						if source == "" {
							return D{}
						}
						return a.label(gtx, spTiny, "Overridden by "+source, sub, 0)
					}),
				)
			}),
			layout.Flexed(1, func(gtx C) D {
				if editing {
					ed := material.Editor(a.th, &form.editor, row.hint)
					ed.TextSize = spBody
					ed.Color = colorFg
					ed.HintColor = colorSubtle
					return ed.Layout(gtx)
				}
				text, col := settingDisplay(row, form.values[i])
				if text == "" {
					text, col = row.hint, colorSubtle
				}
				if selected || col == colorFg {
					col = fg
				}
				return a.label(gtx, spBody, text, col, 0)
			}),
		)
	}
	return a.clickableLayout(gtx, "setting:"+row.key, row.label, func(gtx C) D {
		if editing {
			// A neutral surface keeps the caret and selection visible.
			return a.rowFrame(gtx, settingsRowHeight, colorSurface2, true, true, content)
		}
		return a.focusRowShell(gtx, selected, false, settingsRowHeight, content)
	}, func() { a.clickSetting(form, i) })
}

func settingDisplay(row settingRow, value string) (string, color.NRGBA) {
	switch row.kind {
	case settingToggle:
		if value == "true" {
			return "On", colorFg
		}
		return "Off", colorSubtle
	}
	return value, colorFg
}

func (a *App) layoutSettingsSave(gtx C, form *settingsForm, selected bool) D {
	status, statusCol := form.message, colorSubtle
	if form.failed {
		statusCol = colorTerminal
	} else if status == "" && form.dirty() {
		status = "Unsaved changes"
	}
	return layout.Inset{Left: dp8, Top: dp24, Bottom: dp8}.Layout(gtx, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				gtx.Constraints.Min.X = 0
				return a.clickableLayout(gtx, "setting:save", "Save & restart", func(gtx C) D {
					bg, fg := colorSurface2, colorFg
					if selected {
						bg, fg = colorSelectBg, colorSelectFg
					}
					if form.readOnly {
						fg = colorMuted
					}
					return a.pill(gtx, selected, bg, func(gtx C) D {
						return a.label(gtx, spBody, "Save & restart", fg, font.Bold)
					})
				}, func() { a.clickSetting(form, settingsSaveRow) })
			}),
			layout.Rigid(func(gtx C) D {
				if status == "" {
					return D{}
				}
				return layout.Inset{Top: dp12}.Layout(gtx, func(gtx C) D {
					gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(proseWidth))
					return a.body(gtx, spMeta, status, statusCol, 0)
				})
			}),
		)
	})
}
