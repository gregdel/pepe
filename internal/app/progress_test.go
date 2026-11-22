package app

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func testLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func readProgressFile(t *testing.T, path string) map[string]progressEntry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file progressFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if file.Version != progressVersion {
		t.Fatalf("version = %d", file.Version)
	}
	return file.Items
}

func TestRecordPlaybackWatchedRules(t *testing.T) {
	for _, tc := range []struct {
		name          string
		pos, duration float64
		eof           bool
		want          progressEntry
		stored        bool
	}{
		{name: "interrupted", pos: 600, duration: 3000, want: progressEntry{Pos: 600}, stored: true},
		{name: "credits", pos: 2700, duration: 3000, want: progressEntry{Watched: true}, stored: true},
		{name: "eof", pos: 100, eof: true, want: progressEntry{Watched: true}, stored: true},
		{name: "unknown duration", pos: 2900, want: progressEntry{Pos: 2900}, stored: true},
		{name: "barely started", pos: 10, duration: 3000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newProgressStore()
			p.now = func() time.Time { return time.Unix(1000, 0) }
			p.recordPlayback("movie:tt1", tc.pos, tc.duration, tc.eof)
			got, ok := p.entries["movie:tt1"]
			if ok != tc.stored {
				t.Fatalf("stored = %v", ok)
			}
			if tc.stored {
				tc.want.At = 1000
			}
			if got != tc.want {
				t.Fatalf("entry = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestApplyPlaybackSnapshotUsesDuration(t *testing.T) {
	tracker := newPlaybackTracker("movie:tt123")
	tracker.updatePos(5500)
	tracker.updateDuration(6000)
	close(tracker.done)
	a := &App{progress: newProgressStore()}
	a.progress.entries["movie:tt123"] = progressEntry{Pos: 42}
	a.applyPlaybackSnapshot(tracker)
	if entry := a.progress.entries["movie:tt123"]; !entry.Watched || entry.Pos != 0 {
		t.Fatalf("stopped in the credits = %+v, want watched", entry)
	}
}

func TestToggleWatched(t *testing.T) {
	p := newProgressStore()
	p.entries["episode:s:1:2"] = progressEntry{Pos: 300, At: 1}
	p.toggleWatched("episode:s:1:2")
	if entry := p.entries["episode:s:1:2"]; !entry.Watched || entry.Pos != 0 || p.position("episode:s:1:2") != 0 {
		t.Fatalf("marked = %+v, want watched without resume", entry)
	}
	p.toggleWatched("episode:s:1:2")
	if _, ok := p.entries["episode:s:1:2"]; ok {
		t.Fatal("unmarking kept the entry")
	}
	p.toggleWatched("")
	if len(p.entries) != 0 {
		t.Fatal("empty key was stored")
	}

	// A partial rewatch keeps the mark; unmarking keeps its resume point.
	p.entries["movie:a"] = progressEntry{Watched: true, At: 1}
	p.recordPlayback("movie:a", 2400, 6000, false)
	p.toggleWatched("movie:a")
	if entry := p.entries["movie:a"]; entry.Watched || p.position("movie:a") != 2400 {
		t.Fatalf("unmarked rewatch = %+v, want resume at 2400", entry)
	}
}

func TestCloseRecordsRunningPlayback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "progress.json")
	tracker := newPlaybackTracker("movie:tt123")
	tracker.updatePos(1800)
	tracker.updateDuration(6000)
	a := New(nil, nil, nil, nil, nil)
	a.playerTracker = tracker
	a.progress.open(path, testLogger)
	a.Close()
	if pos := readProgressFile(t, path)["movie:tt123"].Pos; pos != 1800 {
		t.Fatalf("saved position = %v, want 1800", pos)
	}
}

func TestProgressSavesCoalescedOnClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "pepe", "progress.json")
	p := newProgressStore()
	p.delay = time.Hour
	p.open(path, testLogger)
	p.recordPlayback("movie:a", 120, 0, false)
	p.toggleWatched("movie:b")
	p.toggleWatched("movie:c")
	p.toggleWatched("movie:c")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("progress written before the save delay: %v", err)
	}
	p.close()
	items := readProgressFile(t, path)
	if len(items) != 2 || items["movie:a"].Pos != 120 || !items["movie:b"].Watched {
		t.Fatalf("saved = %+v", items)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("state dir = %v, %v; want only progress.json", entries, err)
	}

	reloaded := newProgressStore()
	reloaded.open(path, testLogger)
	defer reloaded.close()
	if reloaded.position("movie:a") != 120 || !reloaded.watched("movie:b") {
		t.Fatalf("reloaded = %+v", reloaded.entries)
	}
}

func TestProgressSavesAfterDelay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "progress.json")
	p := newProgressStore()
	p.delay = 10 * time.Millisecond
	p.open(path, testLogger)
	defer p.close()
	p.toggleWatched("movie:a")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("progress was not saved after the delay")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !readProgressFile(t, path)["movie:a"].Watched {
		t.Fatal("saved progress lacks the mark")
	}
}

func TestProgressCorruptFileIsMovedAside(t *testing.T) {
	for name, content := range map[string]string{
		"invalid":    "{not json",
		"empty":      "{}",
		"null":       "null",
		"version 0":  `{"v":0,"items":{"movie:x":{"watched":true,"at":1}}}`,
		"no version": `{"items":{"movie:x":{"watched":true,"at":1}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "progress.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			p := newProgressStore()
			p.open(path, testLogger)
			if len(p.entries) != 0 {
				t.Fatal("corrupt file produced entries")
			}
			p.toggleWatched("movie:a")
			p.close()
			if items := readProgressFile(t, path); len(items) != 1 || !items["movie:a"].Watched {
				t.Fatalf("saved = %+v", items)
			}
			if data, err := os.ReadFile(path + ".corrupt"); err != nil || string(data) != content {
				t.Fatalf("aside = %q, %v; want the original", data, err)
			}
		})
	}
}

func TestProgressUnreadableFileIsNeverOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "progress.json")
	if err := os.Mkdir(path, 0o700); err != nil { // reading a directory fails
		t.Fatal(err)
	}
	p := newProgressStore()
	p.open(path, testLogger)
	p.toggleWatched("movie:a")
	p.close()
	if info, err := os.Stat(path); err != nil || !info.IsDir() || !p.watched("movie:a") {
		t.Fatalf("path = %v, %v; in memory = %v", info, err, p.watched("movie:a"))
	}
}

func TestProgressNewerVersionIsNeverOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "progress.json")
	future := `{"v":2,"items":{}}`
	if err := os.WriteFile(path, []byte(future), 0o600); err != nil {
		t.Fatal(err)
	}
	p := newProgressStore()
	p.open(path, testLogger)
	p.toggleWatched("movie:a")
	p.close()
	if data, _ := os.ReadFile(path); string(data) != future || !p.watched("movie:a") {
		t.Fatalf("file = %s, in memory = %v", data, p.watched("movie:a"))
	}
}

func TestOpenProgressFollowsConfig(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		state := t.TempDir()
		t.Setenv("XDG_STATE_HOME", state)
		t.Setenv("HOME", t.TempDir())
		cfg := DefaultConfig()
		if !enabled {
			cfg.SaveProgress = &enabled
		}
		a := New(nil, nil, cfg, nil, nil)
		a.openProgress(cfg)
		a.progress.toggleWatched("movie:a")
		a.Close()
		path, err := DefaultProgressPath()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); (err == nil) != enabled {
			t.Fatalf("save_progress %v: progress file stat error %v", enabled, err)
		}
		if enabled && runtime.GOOS != "darwin" && runtime.GOOS != "windows" && !strings.HasPrefix(path, state) {
			t.Fatalf("progress path %s is outside XDG_STATE_HOME", path)
		}
	}
}

func TestHeadlessToggleWatched(t *testing.T) {
	f := newHeadlessFixture(t)
	a := f.app
	hasHint := func(label string) bool {
		for _, h := range a.currentHints() {
			if h.key == "w" && h.label == label {
				return true
			}
		}
		return false
	}
	headlessKey(t, f, "W")
	if len(a.progress.entries) != 0 {
		t.Fatal("w marked something at the browse root")
	}
	headlessKey(t, f, "RightArrow")
	media := a.selectedBrowseMedia()
	if media == nil || media.Type != mediaKindMovie || !hasHint("watched") {
		t.Fatalf("expected a movie terminal with a watched hint, got %+v", media)
	}
	headlessKey(t, f, "W")
	key := movieMediaKey(media.ID)
	if !a.progress.watched(key) || !hasHint("unwatched") {
		t.Fatal("w did not mark the selected movie")
	}
	a.pushMovieDetail(media)
	if err := f.driver.Refresh(); err != nil {
		t.Fatal(err)
	}
	if !headlessHasLabel(f.driver.Tree(), "Watched") {
		t.Fatal("movie detail does not show the watched mark")
	}
	headlessKey(t, f, "W")
	if a.progress.watched(key) {
		t.Fatal("w on movie detail did not unmark")
	}

	a.pushShowDetail(&MediaItem{ID: "show-one", Title: "Fixture Show", Type: mediaKindShow})
	headlessKey(t, f, "W")
	if len(a.progress.entries) != 0 || hasHint("watched") {
		t.Fatal("w on season tabs marked something")
	}
	headlessKey(t, f, "DownArrow")
	headlessKey(t, f, "W")
	if !a.progress.watched(episodeMediaKey("show-one", 1, 1)) {
		t.Fatalf("w did not mark the focused episode: %+v", a.progress.entries)
	}
}

func TestRemoteWatchedIgnoredDuringPlayback(t *testing.T) {
	a := New(nil, nil, nil, nil, nil)
	a.playerStatus = PlayerStatusRunning
	if !a.handleRemoteAction(actionToggleWatched) || a.webCommandError == "" {
		t.Fatal("watched was not refused during playback")
	}
}

func TestSaveProgressSetting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("polochon_url: http://p\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := ReadConfigFile(path)
	if err != nil || !cfg.saveProgress() {
		t.Fatalf("default save_progress = %v, %v", cfg.saveProgress(), err)
	}
	settingRows[settingIndex(t, "save_progress")].set(cfg, "false")
	if err := SaveConfigFile(path, cfg); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "save_progress: false") {
		t.Fatalf("config = %s", data)
	}
	if cfg, err = ReadConfigFile(path); err != nil || cfg.saveProgress() {
		t.Fatalf("saved save_progress = %v, %v", cfg.saveProgress(), err)
	}
}
