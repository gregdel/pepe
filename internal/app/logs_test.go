package app

import (
	"log/slog"
	"testing"
)

func TestHeadlessLogsPage(t *testing.T) {
	f := newHeadlessFixture(t)
	headlessKey(t, f, "LeftArrow")
	headlessKey(t, f, "UpArrow")
	if f.app.browse.logs == nil || !headlessHasLabel(f.driver.Tree(), "No log entries yet.") {
		t.Fatal("Up from Smart list did not open the empty Logs page")
	}
	nav := f.app.browse.nav
	f.app.logs.add(slog.LevelError, "mpv", "[ytdl_hook] ERROR: Unsupported URL")
	f.app.logs.add(slog.LevelInfo, "pepe", "player started")
	headlessKey(t, f, "RightArrow")
	tree := f.driver.Tree()
	for _, label := range []string{"Logs", "2 entries", "ERROR", "mpv", "[ytdl_hook] ERROR: Unsupported URL", "player started"} {
		if !headlessHasLabel(tree, label) {
			t.Fatalf("Logs page is missing %q", label)
		}
	}
	if !f.app.logsVisible.Load() || f.app.browse.focus != browseFocusContent {
		t.Fatal("Logs page is not focused or not marked visible for redraws")
	}
	headlessKey(t, f, "UpArrow")
	headlessKey(t, f, "DownArrow")
	headlessKey(t, f, "Escape")
	if f.app.browse.onPage() || f.app.browse.nav != nav || f.app.browse.focus != browseFocusModeRail {
		t.Fatal("Back did not return from Logs to the unchanged catalog")
	}
	if f.app.logsVisible.Load() {
		t.Fatal("closed Logs page still requests redraws for new entries")
	}
}
