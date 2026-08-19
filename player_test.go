package main

import "testing"

func TestPlaybackMediaKeys(t *testing.T) {
	if got := movieMediaKey("tt123"); got != "movie:tt123" {
		t.Fatalf("movie media key = %q", got)
	}
	if got := episodeMediaKey("tt456", 2, 3); got != "episode:tt456:2:3" {
		t.Fatalf("episode media key = %q", got)
	}
}

func TestBuildMPVArgsUsesNamedResumeThreshold(t *testing.T) {
	args := buildMPVArgs("mpv", "Movie", "https://video", []string{"https://subtitle"}, PlayerModeGUI, minimumResumePosition+1, "/tmp/mpv.sock", true)
	want := map[string]bool{
		"--start=31":                       false,
		"--fs":                             false,
		"--sub-file=https://subtitle":      false,
		"--input-ipc-server=/tmp/mpv.sock": false,
		"https://video":                    false,
	}
	for _, arg := range args {
		if _, ok := want[arg]; ok {
			want[arg] = true
		}
	}
	for arg, found := range want {
		if !found {
			t.Errorf("MPV args missing %q: %#v", arg, args)
		}
	}
}

func TestPlaybackTrackerSnapshot(t *testing.T) {
	tracker := newPlaybackTracker("movie:tt123")
	tracker.updatePos(91.5)

	mediaKey, pos, eof := tracker.snapshot()
	if mediaKey != "movie:tt123" {
		t.Fatalf("mediaKey = %q", mediaKey)
	}
	if pos != 91.5 {
		t.Fatalf("pos = %v", pos)
	}
	if eof {
		t.Fatal("eof = true")
	}
}

func TestApplyPlaybackSnapshotStoresResumePosition(t *testing.T) {
	tracker := newPlaybackTracker("movie:tt123")
	tracker.updatePos(91.5)
	close(tracker.done)

	a := &App{resumePos: make(map[string]float64)}
	a.applyPlaybackSnapshot(tracker)

	if got := a.resumePos["movie:tt123"]; got != 91.5 {
		t.Fatalf("resume position = %v", got)
	}
}

func TestApplyPlaybackSnapshotClearsResumeOnEOF(t *testing.T) {
	tracker := newPlaybackTracker("movie:tt123")
	tracker.updatePos(91.5)
	tracker.markEOF()
	close(tracker.done)

	a := &App{resumePos: map[string]float64{"movie:tt123": 42}}
	a.applyPlaybackSnapshot(tracker)

	if _, ok := a.resumePos["movie:tt123"]; ok {
		t.Fatal("resume position was not cleared")
	}
}

func TestDetectPlayerMode(t *testing.T) {
	emptyEnv := func(string) string { return "" }

	if got := detectPlayerModeFor("darwin", emptyEnv); got != PlayerModeGUI {
		t.Fatalf("macOS player mode = %q, want %q", got, PlayerModeGUI)
	}
	if got := detectPlayerModeFor("linux", emptyEnv); got != PlayerModeKMS {
		t.Fatalf("headless Linux player mode = %q, want %q", got, PlayerModeKMS)
	}
	if got := detectPlayerModeFor("linux", func(name string) string {
		if name == "DISPLAY" {
			return ":0"
		}
		return ""
	}); got != PlayerModeGUI {
		t.Fatalf("desktop Linux player mode = %q, want %q", got, PlayerModeGUI)
	}
}
