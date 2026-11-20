package app

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	gioapp "gioui.org/app"
	"gioui.org/io/event"
)

func TestWebResolvedTitleWithoutFrames(t *testing.T) {
	wakes := make(chan struct{}, 16)
	tracker, peer := playbackIPCPeer(t, "", func() { wakes <- struct{}{} })
	a := New(nil, nil, nil, nil, nil)
	defer a.Close()
	if err := a.startWebUI("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	a.playerStatus = PlayerStatusRunning
	a.playerFromWeb = true
	a.playerTitle = webPlaybackTitle
	a.playerTracker = tracker
	a.playerDone = make(chan playerResult, 1)

	// Process a wake-up without drawing, as happens behind fullscreen mpv.
	pump := func() {
		t.Helper()
		wake := true
		if err := a.run(func() event.Event {
			if wake {
				wake = false
				return gioapp.ConfigEvent{}
			}
			return gioapp.DestroyEvent{}
		}); err != nil {
			t.Fatal(err)
		}
	}
	check := func(want string, state PlayerStatus) {
		t.Helper()
		pump()
		response := webRequest(a.webUI.handler(), "GET", "/api/status", "")
		var status webStatus
		if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		if status.Title != want || a.playerTitle != want || status.State != state {
			t.Fatalf("title state = %+v, display = %q; want %q, %s", status, a.playerTitle, want, state)
		}
	}
	send := func(data string) {
		t.Helper()
		if _, err := fmt.Fprintf(peer, "{\"event\":\"property-change\",\"name\":\"force-media-title\",\"data\":%s}\n", data); err != nil {
			t.Fatal(err)
		}
		select {
		case <-wakes:
		case <-time.After(time.Second):
			t.Fatal("title change did not wake the event loop")
		}
	}
	check(webPlaybackTitle, PlayerStatusRunning)
	send(`"A resolved video — café <script>"`)
	check("A resolved video — café <script>", PlayerStatusRunning)
	send(`"Next playlist video"`)
	check("Next playlist video", PlayerStatusRunning)
	send("null")
	check(webPlaybackTitle, PlayerStatusRunning)
	send(`"Final title"`)
	check("Final title", PlayerStatusRunning)

	tracker.stopIPC()
	<-tracker.done
	a.playerDone <- playerResult{tracker: tracker}
	check("", PlayerStatusIdle)
	if a.playerTracker != nil {
		t.Fatal("completed session retained its title tracker")
	}
	tracker.updateTitle("Late old-session title")
	check("", PlayerStatusIdle)

	// A new session starts with its own empty tracker, never the previous title.
	a.playerStatus = PlayerStatusRunning
	a.playerFromWeb = true
	a.playerTracker = newPlaybackTracker("")
	check(webPlaybackTitle, PlayerStatusRunning)
	a.playerFromWeb = false
	a.playerTitle = "Catalog (2024)"
	a.playerTracker.updateTitle("Unrelated mpv title")
	check("Catalog (2024)", PlayerStatusRunning)
}

func TestPlaybackTitleInvalidAndDuplicateEvents(t *testing.T) {
	wakes := make(chan struct{}, 16)
	tracker, peer := playbackIPCPeer(t, "", func() { wakes <- struct{}{} })
	// A valid change is also a barrier for preceding malformed events.
	for _, data := range []string{`42`, `{}`, `false`, `null`, `""`, `"  "`, `"Resolved"`, `"Resolved"`} {
		if _, err := fmt.Fprintf(peer, "{\"event\":\"property-change\",\"name\":\"force-media-title\",\"data\":%s}\n", data); err != nil {
			t.Fatal(err)
		}
	}
	_ = peer.Close()
	<-tracker.done
	if tracker.resolvedTitle() != "Resolved" || len(wakes) != 1 {
		t.Fatalf("title = %q, wake-ups = %d", tracker.resolvedTitle(), len(wakes))
	}
}

// Current yt-dlp hooks publish the title as stream metadata; a forced title
// (older hooks or user configs) takes precedence while it is set.
func TestPlaybackTitleSources(t *testing.T) {
	wakes := make(chan struct{}, 16)
	tracker, peer := playbackIPCPeer(t, "", func() { wakes <- struct{}{} })
	for _, step := range []struct{ name, data, want string }{
		{"metadata/by-key/title", `"Me at the zoo"`, "Me at the zoo"},
		{"force-media-title", `"Forced"`, "Forced"},
		{"metadata/by-key/title", `"Other metadata"`, "Forced"},
		{"force-media-title", `""`, "Other metadata"},
		{"metadata/by-key/title", `null`, ""},
	} {
		if _, err := fmt.Fprintf(peer, "{\"event\":\"property-change\",\"name\":%q,\"data\":%s}\n", step.name, step.data); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(time.Second)
		for tracker.resolvedTitle() != step.want && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if got := tracker.resolvedTitle(); got != step.want {
			t.Fatalf("after %s=%s: title = %q, want %q", step.name, step.data, got, step.want)
		}
	}
}
