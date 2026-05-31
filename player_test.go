package main

import "testing"

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
