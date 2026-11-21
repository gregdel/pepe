package app

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gioui.org/io/key"
	"gioui.org/widget"

	"github.com/gregdel/pepe/internal/driver"
)

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, env := range configEnv {
		t.Setenv(env.name, "")
	}
}

// newSettingsFixture opens the Settings rail page on a config file holding
// content, with the window close recorded in closed.
func newSettingsFixture(t *testing.T, content string, overrides map[string]string) (f *headlessFixture, path string, closed *bool) {
	t.Helper()
	clearConfigEnv(t)
	path = filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	f = newHeadlessFixture(t)
	closed = new(bool)
	f.app.closeWindow = func() { *closed = true }
	f.app.configSource = ConfigSource{Path: path, Overrides: overrides}
	headlessKey(t, f, "LeftArrow")
	headlessKey(t, f, "UpArrow") // Smart list wraps to Settings
	headlessKey(t, f, "RightArrow")
	if f.app.browse.settings == nil || f.app.browse.focus != browseFocusContent {
		t.Fatal("Up from Smart list did not open the Settings page")
	}
	return f, path, closed
}

func settingIndex(t *testing.T, key string) int {
	t.Helper()
	i := slices.IndexFunc(settingRows, func(row settingRow) bool { return row.key == key })
	if i < 0 {
		t.Fatalf("no setting %q", key)
	}
	return i
}

func moveSettingsTo(t *testing.T, f *headlessFixture, form *settingsForm, row int) {
	t.Helper()
	for form.cursor < row {
		headlessKey(t, f, "DownArrow")
	}
	for form.cursor > row {
		headlessKey(t, f, "UpArrow")
	}
}

// clickSettingRow clicks a row button; its label also names the nested text.
func clickSettingRow(t *testing.T, f *headlessFixture, label string) {
	t.Helper()
	id, ok := headlessButtonID(f.driver.Tree(), label)
	if !ok {
		t.Fatalf("no %q row", label)
	}
	headlessClick(t, f, driver.ClickTarget{ID: &id})
}

func typeSetting(t *testing.T, f *headlessFixture, text string) {
	t.Helper()
	if err := f.driver.Type(text); err != nil {
		t.Fatal(err)
	}
}

func TestHeadlessSettingsEditAndSave(t *testing.T) {
	const content = "# keep me\npolochon_token: replace-me\nmpv_stop_timeout: 4s\n"
	f, path, closed := newSettingsFixture(t, content, nil)
	form := f.app.browse.settings
	tree := f.driver.Tree()
	for _, label := range []string{"Settings", path, "Polochon URL", "Polochon token", "Help bar", "Save & restart", "https://polochon.example"} {
		if !headlessHasLabel(tree, label) {
			t.Fatalf("Settings page is missing %q", label)
		}
	}
	if !headlessHasLabel(tree, "replace-me") {
		t.Fatal("the example token is not shown")
	}

	// Typed letters reach the field, not the global h/j/k/l/q/r/s bindings.
	headlessKey(t, f, "Enter")
	if !f.app.settingsEditing() {
		t.Fatal("Enter did not edit the URL")
	}
	for _, name := range []string{"H", "J", "Q", "R", "S", "LeftArrow", "RightArrow", "Home"} {
		headlessKey(t, f, name)
	}
	if !f.app.settingsEditing() || f.app.browse.settings != form || f.app.browse.focus != browseFocusContent || *closed {
		t.Fatal("keys typed into a field acted on the UI")
	}
	typeSetting(t, f, "http://deck.local:8080")
	headlessKey(t, f, "Enter")
	if f.app.settingsEditing() || form.values[0] != "http://deck.local:8080" {
		t.Fatalf("Enter did not keep the URL: %q", form.values[0])
	}
	if !headlessHasLabel(f.driver.Tree(), "Unsaved changes") {
		t.Fatal("unsaved changes are not reported")
	}

	// Escape cancels an edit without leaving the page.
	mpv := settingIndex(t, "mpv_path")
	moveSettingsTo(t, f, form, mpv)
	headlessKey(t, f, "Enter")
	typeSetting(t, f, "-broken")
	headlessKey(t, f, "Escape")
	if form.values[mpv] != "mpv" || f.app.browse.settings != form {
		t.Fatalf("Escape kept %q or left the page", form.values[mpv])
	}

	fullscreen := settingIndex(t, "fullscreen")
	moveSettingsTo(t, f, form, fullscreen)
	headlessKey(t, f, "Enter")
	osd := settingIndex(t, "mpv_osd")
	moveSettingsTo(t, f, form, osd)
	headlessKey(t, f, "Enter")
	if form.values[fullscreen] != "true" || form.values[osd] != "native" {
		t.Fatalf("Enter did not change toggle/choice: %q %q", form.values[fullscreen], form.values[osd])
	}

	// The example token is rejected before anything is written.
	moveSettingsTo(t, f, form, settingsSaveRow)
	headlessKey(t, f, "Enter")
	if !form.failed || !strings.Contains(form.message, "example value") || *closed {
		t.Fatalf("example token was accepted: %q", form.message)
	}
	if data, _ := os.ReadFile(path); string(data) != content {
		t.Fatalf("invalid settings were written: %q", data)
	}

	moveSettingsTo(t, f, form, settingIndex(t, "polochon_token"))
	headlessKey(t, f, "Enter")
	if err := f.driver.Key("A", []string{"Shortcut"}); err != nil {
		t.Fatal(err)
	}
	typeSetting(t, f, "secret")
	headlessKey(t, f, "DownArrow") // commits and moves on
	if !headlessHasLabel(f.driver.Tree(), "secret") {
		t.Fatal("the token is not shown")
	}
	if form.message != "" || form.failed {
		t.Fatalf("the save error outlived the fix: %q", form.message)
	}
	moveSettingsTo(t, f, form, settingsSaveRow)
	headlessKey(t, f, "Enter")
	if form.failed || !*closed || f.app.runResult(nil) != ErrRestart {
		t.Fatalf("valid settings did not save and restart: %q", form.message)
	}
	saved, err := ReadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.PolochonURL != "http://deck.local:8080" || saved.Token != "secret" || !saved.Fullscreen || saved.MPVOSD != "native" || saved.MPVStopTimeout.String() != "4s" {
		t.Fatalf("saved config = %+v", saved)
	}
	if data, _ := os.ReadFile(path); !strings.HasPrefix(string(data), "# keep me\n") {
		t.Fatalf("comment lost: %q", data)
	}
}

func TestEditKeyFiltersLeaveTextToEditor(t *testing.T) {
	var editor widget.Editor
	var names []key.Name
	for _, filter := range keyFilters(&editor, true) {
		f := filter.(key.Filter)
		if f.Focus != &editor {
			t.Errorf("%q filter is not scoped to the editor", f.Name)
		}
		names = append(names, f.Name)
	}
	for _, name := range []key.Name{"H", "J", "K", "L", "Q", "R", "S", key.NameLeftArrow, key.NameRightArrow, key.NameHome} {
		if slices.Contains(names, name) {
			t.Errorf("editing filters capture %q", name)
		}
	}
	for _, name := range []key.Name{key.NameReturn, key.NameEscape, key.NameUpArrow, key.NameDownArrow, key.NameTab} {
		if !slices.Contains(names, name) {
			t.Errorf("editing filters miss %q", name)
		}
	}
}

func TestHeadlessSettingsPointer(t *testing.T) {
	f, _, _ := newSettingsFixture(t, "polochon_url: http://p\n", map[string]string{"controls": "-controls"})
	form := f.app.browse.settings
	if !headlessHasLabel(f.driver.Tree(), "Overridden by -controls") {
		t.Fatal("flag override is not shown")
	}
	headlessKey(t, f, "LeftArrow") // the rail has focus; a click returns it
	clickSettingRow(t, f, "Help bar")
	help := settingIndex(t, "help_bar")
	if form.cursor != help || form.values[help] != "false" || f.app.browse.focus != browseFocusContent {
		t.Fatalf("click did not toggle Help bar: cursor %d value %q", form.cursor, form.values[help])
	}
	clickSettingRow(t, f, "mpv path")
	if !f.app.settingsEditing() || form.editing != settingIndex(t, "mpv_path") {
		t.Fatal("click did not edit mpv path")
	}
	typeSetting(t, f, "2")
	clickSettingRow(t, f, "Controls")
	if form.values[settingIndex(t, "mpv_path")] != "mpv2" || form.values[settingIndex(t, "controls")] != "hdmi-cec" || f.app.settingsEditing() {
		t.Fatal("clicking another row did not commit the edit and change Controls")
	}
}

func TestHeadlessSettingsReadOnlyWithoutConfigFile(t *testing.T) {
	f := newHeadlessFixture(t)
	f.app.selectRailOption(railSettings)
	headlessKey(t, f, "RightArrow")
	headlessKey(t, f, "Enter")
	form := f.app.browse.settings
	if !form.readOnly || f.app.settingsEditing() || !headlessHasLabel(f.driver.Tree(), "No config file: settings are read-only.") {
		t.Fatal("settings without a config file are editable")
	}
}

func TestHeadlessSetupScreenSavesSettings(t *testing.T) {
	clearConfigEnv(t)
	path := filepath.Join(t.TempDir(), "pepe", "config.yaml")
	if _, err := WriteConfigTemplate(path); err != nil {
		t.Fatal(err)
	}
	_, problem := LoadConfig(path)
	closed := false
	a := NewSetup(ConfigSource{Path: path}, problem, true, nil, func() { closed = true })
	d, err := driver.New(driver.Config{Width: 1024, Height: 768, PxPerDp: 1, PxPerSp: 1}, a.Layout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	f := &headlessFixture{app: a, driver: d}
	if !headlessHasLabel(d.Tree(), "polochon_token still has the example value") || !headlessHasLabel(d.Tree(), "Polochon token") {
		t.Fatal("setup screen does not show the problem and the form")
	}
	form := a.setup.form
	moveSettingsTo(t, f, form, settingIndex(t, "polochon_token"))
	headlessKey(t, f, "Enter")
	if err := d.Key("A", []string{"Shortcut"}); err != nil {
		t.Fatal(err)
	}
	headlessKey(t, f, "Q") // typed, not quit
	if closed || !a.settingsEditing() {
		t.Fatal("q quit the setup screen while editing")
	}
	hasQuit := func() bool { return slices.ContainsFunc(a.contextHints(), func(h hint) bool { return h.key == "q" }) }
	if hasQuit() {
		t.Fatal("the help bar offers q to quit while q is typed")
	}
	typeSetting(t, f, "token")
	headlessKey(t, f, "Enter")
	if !hasQuit() {
		t.Fatal("the help bar lost q quit after editing")
	}
	moveSettingsTo(t, f, form, settingsSaveRow)
	headlessKey(t, f, "Enter")
	if !closed || !errors.Is(a.runResult(nil), ErrRestart) {
		t.Fatalf("setup did not save and restart: %q", form.message)
	}
	saved, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("saved setup config is invalid: %v", err)
	}
	if saved.Token != "token" {
		t.Fatalf("saved token = %q, want the typed value only", saved.Token)
	}
}

func TestHeadlessSettingsSaveAfterPlayerError(t *testing.T) {
	f, _, closed := newSettingsFixture(t, "polochon_url: http://p\npolochon_token: t\nmpv_path: /missing/mpv\n", nil)
	form := f.app.browse.settings
	moveSettingsTo(t, f, form, settingsSaveRow)
	f.app.playerStatus = PlayerStatusRunning
	headlessKey(t, f, "Enter")
	if !form.failed || *closed {
		t.Fatal("settings were saved during playback")
	}
	// A failed launch leaves the error status until the next playback.
	f.app.playerStatus = PlayerStatusError
	headlessKey(t, f, "Enter")
	if form.failed || !*closed {
		t.Fatalf("a playback error blocks saving: %q", form.message)
	}
}

func TestHeadlessSettingsKeepUnsavedEditsWhenLeaving(t *testing.T) {
	f, path, _ := newSettingsFixture(t, "polochon_url: http://p\n", nil)
	help := settingIndex(t, "help_bar")
	moveSettingsTo(t, f, f.app.browse.settings, help)
	headlessKey(t, f, "Enter")
	for _, leave := range []string{"Escape", "Home", "Tab"} {
		headlessKey(t, f, leave)
		if f.app.browse.settings != nil {
			t.Fatalf("%s did not leave Settings", leave)
		}
		f.app.selectRailOption(railSettings)
		f.app.browse.focus = browseFocusContent
		if form := f.app.browse.settings; form == nil || form.values[help] != "false" || !form.dirty() {
			t.Fatalf("%s lost the unsaved edit", leave)
		}
	}

	// Reverting the edit drops the draft, so the file is read again.
	headlessKey(t, f, "Enter")
	if err := os.WriteFile(path, []byte("polochon_url: http://changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	headlessKey(t, f, "Escape")
	f.app.selectRailOption(railSettings)
	if got := f.app.browse.settings.values[0]; got != "http://changed" {
		t.Fatalf("clean form was not read again: %q", got)
	}
}
