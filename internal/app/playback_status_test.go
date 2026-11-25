package app

import (
	"errors"
	"fmt"
	"os/exec"
	"testing"
)

// playingFixture makes key the entry a running (stub) player is playing.
func playingFixture(t *testing.T, key string) *headlessFixture {
	t.Helper()
	f := newHeadlessFixture(t)
	f.app.playerStatus = PlayerStatusRunning
	f.app.playerTitle = "Playing title"
	f.app.playerStop = make(chan struct{})
	f.app.playerDone = make(chan playerResult, 1)
	f.app.playerTracker = newPlaybackTracker(key, nil)
	return f
}

func hasHint(a *App, label string) bool {
	for _, h := range a.currentHints() {
		if h.label == label {
			return true
		}
	}
	return false
}

func TestMovieDetailPlayingTitle(t *testing.T) {
	f := playingFixture(t, movieMediaKey("movie-new"))
	f.app.progress.entries[movieMediaKey("movie-new")] = progressEntry{Pos: 300}
	f.app.pushMovieDetail(&MediaItem{ID: "movie-new", Title: "New Movie", Type: mediaKindMovie})
	if err := f.driver.Refresh(); err != nil {
		t.Fatal(err)
	}
	tree := f.driver.Tree()
	if !headlessHasLabel(tree, "Now playing") || headlessHasLabel(tree, "Resume 5:00") {
		t.Fatal("playing movie's button does not say it is playing")
	}
	for _, label := range []string{"Player", "Status: Playing", "Mode: kms"} {
		if headlessHasLabel(tree, label) {
			t.Errorf("detail page shows player internals %q", label)
		}
	}
	if hasHint(f.app, "play") {
		t.Error("help bar offers play on the playing title")
	}

	headlessKey(t, f, "Enter")
	if f.app.pendingPlayback != nil || f.app.playerStopping || f.app.playerStop == nil {
		t.Fatal("Enter on the playing title restarted it")
	}

	// Another title still switches playback.
	f.app.popDetail()
	f.app.pushMovieDetail(&MediaItem{ID: "movie-old", Title: "Old Movie", Type: mediaKindMovie})
	if err := f.driver.Refresh(); err != nil {
		t.Fatal(err)
	}
	if !hasHint(f.app, "play") || !headlessHasLabel(f.driver.Tree(), "Play") {
		t.Fatal("other title lost its Play action")
	}
	headlessKey(t, f, "Enter")
	if f.app.pendingPlayback == nil || !f.app.playerStopping {
		t.Fatal("Enter on another title did not switch playback")
	}
}

func TestShowDetailPlayingEpisode(t *testing.T) {
	f := playingFixture(t, episodeMediaKey("show-one", 1, 2))
	f.app.progress.entries[episodeMediaKey("show-one", 1, 2)] = progressEntry{Pos: 300}
	f.app.pushShowDetail(&MediaItem{ID: "show-one", Title: "Fixture Show", Type: mediaKindShow})
	if err := f.driver.Refresh(); err != nil {
		t.Fatal(err)
	}
	// With S01E01 focused, "Now playing" is only in the playing bar and on
	// the S01E02 row.
	if n := len(headlessLabelNodes(f.driver.Tree(), "Now playing")); n != 2 {
		t.Fatalf("Now playing labels = %d, want 2", n)
	}
	headlessKey(t, f, "DownArrow")
	headlessKey(t, f, "DownArrow")
	show := f.app.detail.show
	if show.focus != focusEpisodes || show.epCursor != 1 {
		t.Fatalf("episode focus = (focus=%v, episode=%d)", show.focus, show.epCursor)
	}
	tree := f.driver.Tree()
	if n := len(headlessLabelNodes(tree, "Now playing")); n != 3 {
		t.Fatalf("Now playing labels with the playing episode focused = %d, want 3", n)
	}
	if headlessHasLabel(tree, "Resume from 5:00") {
		t.Error("playing episode shows its stale resume position")
	}
	if hasHint(f.app, "play episode") {
		t.Error("help bar offers play on the playing episode")
	}
	headlessKey(t, f, "Enter")
	if f.app.pendingPlayback != nil || f.app.playerStopping {
		t.Fatal("Enter on the playing episode restarted it")
	}
}

func TestPlaybackErrorIsScopedToItsTitle(t *testing.T) {
	f := newHeadlessFixture(t)
	f.app.recordPlayerError(movieMediaKey("movie-new"), errors.New(`video URL: Get "https://private/video?token=secret"`))
	f.app.pushMovieDetail(&MediaItem{ID: "movie-old", Title: "Old Movie", Type: mediaKindMovie})
	if err := f.driver.Refresh(); err != nil {
		t.Fatal(err)
	}
	if headlessHasLabel(f.driver.Tree(), "Playback failed") || headlessHasLabel(f.driver.Tree(), "Details in Logs") {
		t.Fatal("another title's failure leaked onto this page")
	}
	f.app.popDetail()
	f.app.pushMovieDetail(&MediaItem{ID: "movie-new", Title: "New Movie", Type: mediaKindMovie})
	if err := f.driver.Refresh(); err != nil {
		t.Fatal(err)
	}
	tree := f.driver.Tree()
	if !headlessHasLabel(tree, "Playback failed") || !headlessHasLabel(tree, "Details in Logs") {
		t.Fatal("failure missing from the failed title")
	}
	if headlessHasLabel(tree, f.app.playerError) {
		t.Fatal("raw error shown on the TV")
	}

	f.app.popDetail()
	f.app.recordPlayerError(episodeMediaKey("show-one", 1, 1), errMPVNotFound)
	f.app.pushShowDetail(&MediaItem{ID: "show-one", Title: "Fixture Show", Type: mediaKindShow})
	if err := f.driver.Refresh(); err != nil {
		t.Fatal(err)
	}
	if !headlessHasLabel(f.driver.Tree(), "mpv isn't installed") {
		t.Fatal("episode failure missing from its info block")
	}
	headlessKey(t, f, "DownArrow")
	headlessKey(t, f, "DownArrow")
	if headlessHasLabel(f.driver.Tree(), "mpv isn't installed") {
		t.Fatal("episode failure shown on another episode")
	}
}

func TestPlaybackErrorReason(t *testing.T) {
	exit2 := exec.Command("sh", "-c", "exit 2").Run()
	exit7 := exec.Command("sh", "-c", "exit 7").Run()
	for _, tc := range []struct {
		err  error
		want string
	}{
		{errMPVNotFound, "mpv isn't installed"},
		{fmt.Errorf("%w: %w", errVideoURL, errors.New("no file")), "This file isn't available"},
		{errLauncherBlocked, "The previous player didn't close · restart pépé"},
		{errors.Join(exit7, errLauncherTeardown), "The previous player didn't close · restart pépé"},
		{exit2, "mpv couldn't open this file"},
		{exit7, "Playback failed"},
		{errors.New("https://private/video?token=secret"), "Playback failed"},
	} {
		if got := playbackErrorReason(tc.err); got != tc.want {
			t.Errorf("playbackErrorReason(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}
