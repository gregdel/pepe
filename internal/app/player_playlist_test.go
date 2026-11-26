package app

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/odwrtw/polochon/lib/papi"
)

func playlistKeys(sources []playbackSource) []string {
	keys := make([]string, len(sources))
	for i, source := range sources {
		keys[i] = source.mediaKey
	}
	return keys
}

// The launched episode is queued with the whole show around it, in season
// and episode order, and only it is on mpv's command line.
func TestLaunchEpisodeQueuesWholeShow(t *testing.T) {
	client, err := NewHeadlessPlaybackClient(nil)
	if err != nil {
		t.Fatal(err)
	}
	show, ok := client.getShow("mock-neon")
	if !ok {
		t.Fatal("show missing")
	}
	node := createSeasonNodes(show)[0].Children[2] // S01E03
	a := New(nil, nil, nil, nil, nil)
	a.client, a.playerStatus = client, PlayerStatusRunning
	a.launchEpisode(node)
	source := a.pendingPlayback
	if source == nil || source.mediaKey != episodeMediaKey("mock-neon", 1, 3) {
		t.Fatalf("launched = %+v", source)
	}
	wantBefore := []string{episodeMediaKey("mock-neon", 1, 1), episodeMediaKey("mock-neon", 1, 2)}
	wantAfter := []string{episodeMediaKey("mock-neon", 2, 1), episodeMediaKey("mock-neon", 2, 2), episodeMediaKey("mock-neon", 2, 3)}
	if got := playlistKeys(source.before); !reflect.DeepEqual(got, wantBefore) {
		t.Fatalf("before = %v, want %v", got, wantBefore)
	}
	if got := playlistKeys(source.after); !reflect.DeepEqual(got, wantAfter) {
		t.Fatalf("after = %v, want %v", got, wantAfter)
	}
	if title := source.after[0].title; title != "Neon Nights S02E01 - Chapter 1" {
		t.Fatalf("queued title = %q", title)
	}

	a.progress.entries[wantAfter[1]] = progressEntry{Pos: 600}
	command, err := a.buildPlayerCommand(*source)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(command.runtimeDir) })
	if len(command.before) != 2 || len(command.after) != 3 {
		t.Fatalf("queued entries = %d before, %d after", len(command.before), len(command.after))
	}
	for _, entry := range append(append([]playlistEntry(nil), command.before...), command.after...) {
		if !strings.HasPrefix(entry.url, "http") || entry.title == "" {
			t.Fatalf("entry = %+v", entry)
		}
		if want := map[bool]float64{true: 600}[entry.mediaKey == wantAfter[1]]; entry.start != want {
			t.Fatalf("%s start = %v, want %v", entry.mediaKey, entry.start, want)
		}
	}
	urls := 0
	for _, arg := range command.args {
		if strings.HasPrefix(arg, "http") {
			urls++
		}
	}
	if urls != 1 || !strings.HasPrefix(command.args[len(command.args)-1], "http") {
		t.Fatalf("command line must carry only the launched URL, last: %v", command.args)
	}
}

func TestLaunchEpisodeWithoutShowPlaysAlone(t *testing.T) {
	client := NewEmptyAPIClient(nil)
	a := &App{client: client, playerStatus: PlayerStatusRunning}
	ep := testEpisode(time.Time{})
	ep.Season, ep.Episode = 1, 2
	a.launchEpisode(&DynamicNode{ShowID: "missing", Episode: ep, Label: "S01E02"})
	if a.pendingPlayback == nil || len(a.pendingPlayback.before)+len(a.pendingPlayback.after) != 0 {
		t.Fatalf("pending = %+v", a.pendingPlayback)
	}
}

func TestMPVPathListEscapesSeparators(t *testing.T) {
	got := mpvPathList([]string{"https://host:8080/a.srt", `https://host/b\c:d.srt`})
	if want := `https\://host\:8080/a.srt:https\://host/b\\c\:d.srt`; got != want {
		t.Fatalf("path list = %q, want %q", got, want)
	}
	if got := mpvPathList(nil); got != "" {
		t.Fatalf("empty path list = %q", got)
	}
}

type loadfileLine struct {
	Command struct {
		Name    string            `json:"name"`
		URL     string            `json:"url"`
		Flags   string            `json:"flags"`
		Index   int               `json:"index"`
		Options map[string]string `json:"options"`
	} `json:"command"`
	RequestID int64 `json:"request_id"`
}

// Queued episodes are inserted around the launched file with their own
// options, and each one is recorded when mpv ends it.
func TestPlayerIPCPlaylist(t *testing.T) {
	key := func(e int) string { return episodeMediaKey("show", 1, e) }
	entry := func(e int, start float64, subs ...string) playlistEntry {
		return playlistEntry{mediaKey: key(e), title: fmt.Sprintf("Show S01E%02d", e), url: fmt.Sprintf("https://media/e%d", e), subtitleURLs: subs, start: start}
	}
	command := playerCommand{
		mediaKey: key(3),
		title:    "Show S01E03",
		before:   []playlistEntry{entry(1, 0), entry(2, 0, "https://media/e2.srt")},
		after:    []playlistEntry{entry(4, 600), entry(5, 0)},
	}
	a := New(nil, nil, nil, nil, nil)
	wakes := make(chan struct{}, 64)
	a.invalidate = func() { wakes <- struct{}{} }
	tracker := a.commandTracker(command)
	conn, scanner := trackerIPCPeer(t, tracker)

	for _, property := range []string{"duration", "playlist-pos", "playlist-count", "pause"} {
		if !scanner.Scan() || !strings.Contains(scanner.Text(), `"`+property+`"`) {
			t.Fatalf("missing %s subscription: %s", property, scanner.Text())
		}
	}
	send := func(lines ...string) {
		t.Helper()
		for _, line := range lines {
			if _, err := fmt.Fprintln(conn, line); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Episodes are queued only once mpv plays the launched entry, numbered 1:
	// inserted earlier, mpv would start at the first one.
	send(`{"event":"property-change","name":"playlist-pos","data":-1}`, `{"event":"start-file","playlist_entry_id":1}`)
	var lines []loadfileLine
	for range 4 {
		var line loadfileLine
		if !scanner.Scan() || json.Unmarshal(scanner.Bytes(), &line) != nil {
			t.Fatalf("loadfile line = %s, %v", scanner.Text(), scanner.Err())
		}
		lines = append(lines, line)
	}
	type want struct {
		url, flags         string
		index              int
		start, subs, title string
	}
	for i, w := range []want{
		{"https://media/e1", "insert-at", 0, "none", "", "Show S01E01"},
		{"https://media/e2", "insert-at", 1, "none", `https\://media/e2.srt`, "Show S01E02"},
		{"https://media/e4", "append", -1, "600", "", "Show S01E04"},
		{"https://media/e5", "append", -1, "none", "", "Show S01E05"},
	} {
		c := lines[i].Command
		if c.Name != "loadfile" || c.URL != w.url || c.Flags != w.flags || c.Index != w.index ||
			c.Options["start"] != w.start || c.Options["sub-files"] != w.subs || c.Options["force-media-title"] != w.title || c.Options["title"] != w.title {
			t.Fatalf("loadfile %d = %+v, want %+v", i, c, w)
		}
		if _, ok := c.Options["sub-files"]; !ok {
			t.Fatalf("loadfile %d must clear the launched file's subtitles: %+v", i, c.Options)
		}
	}

	awaitRecord := func() {
		t.Helper()
		for {
			select {
			case <-wakes:
				tracker.mu.RLock()
				n := len(tracker.records)
				tracker.mu.RUnlock()
				if n > 0 {
					return
				}
			case <-time.After(2 * time.Second):
				t.Fatal("no playback record")
			}
		}
	}
	// S01E05 failed to queue.
	send(
		fmt.Sprintf(`{"request_id":%d,"error":"success","data":{"playlist_entry_id":2}}`, lines[0].RequestID),
		fmt.Sprintf(`{"request_id":%d,"error":"success","data":{"playlist_entry_id":3}}`, lines[1].RequestID),
		fmt.Sprintf(`{"request_id":%d,"error":"success","data":{"playlist_entry_id":4}}`, lines[2].RequestID),
		fmt.Sprintf(`{"request_id":%d,"error":"invalid parameter"}`, lines[3].RequestID),
		`{"event":"property-change","name":"duration","data":1300}`,
		`{"event":"property-change","name":"time-pos","data":1250}`,
		`{"event":"end-file","reason":"eof","playlist_entry_id":1}`,
	)
	awaitRecord()
	// The event loop stores each ended episode while mpv still plays.
	a.playerStatus, a.playerTracker = PlayerStatusRunning, tracker
	a.drainChannels()
	if !a.progress.watched(key(3)) {
		t.Fatalf("launched episode not watched during playback: %+v", a.progress.entries[key(3)])
	}

	// A late eof-reached from the ended entry must not mark S01E04, and the
	// title follows the force-media-title its loadfile set.
	send(
		`{"event":"start-file","playlist_entry_id":4}`,
		`{"event":"property-change","name":"eof-reached","data":true}`,
		`{"event":"property-change","name":"force-media-title","data":"Show S01E04"}`,
		`{"event":"property-change","name":"playlist-count","data":4}`,
		`{"event":"property-change","name":"playlist-pos","data":3}`,
		`{"event":"property-change","name":"duration","data":2600}`,
		`{"event":"property-change","name":"time-pos","data":100}`,
	)
	timeout := time.After(2 * time.Second)
	for pos, count := tracker.playlist(); a.playerTitle != "Show S01E04" || pos != 4 || count != 4; pos, count = tracker.playlist() {
		select {
		case <-wakes:
			a.drainChannels()
		case <-timeout:
			t.Fatalf("player title = %q", a.playerTitle)
		}
	}
	if a.playerTitle != "Show S01E04" {
		t.Fatalf("player title = %q", a.playerTitle)
	}
	send(`{"event":"end-file","reason":"stop","playlist_entry_id":4}`)
	awaitRecord()
	// Back to S01E01, quit near the credits. It has S01E04's length, so mpv
	// sends no duration change.
	send(
		`{"event":"start-file","playlist_entry_id":2}`,
		`{"event":"property-change","name":"time-pos","data":2400}`,
		`{"event":"end-file","reason":"quit","playlist_entry_id":2}`,
	)
	awaitRecord()
	_ = conn.Close()
	<-tracker.done

	a.playerDone = make(chan playerResult, 1)
	a.playerDone <- playerResult{tracker: tracker}
	a.drainChannels()
	if !a.progress.watched(key(3)) || !a.progress.watched(key(1)) {
		t.Fatalf("watched = %+v", a.progress.entries)
	}
	if entry := a.progress.entries[key(4)]; entry.Watched || entry.Pos != 100 {
		t.Fatalf("skipped episode = %+v, want resume at 100", entry)
	}
	for _, untouched := range []string{key(2), key(5)} {
		if _, ok := a.progress.entries[untouched]; ok {
			t.Fatalf("unplayed %s recorded", untouched)
		}
	}
	if mediaKey, _, _ := tracker.snapshot(); mediaKey != key(1) {
		t.Fatalf("last entry = %q", mediaKey)
	}
}

// An entry mpv did not name (added by the user) is never attributed to an
// episode, and its end does not close the launched entry twice.
func TestPlaybackTrackerUnknownEntry(t *testing.T) {
	tracker := newPlaybackTracker("movie:first", nil)
	tracker.endEntry(7, true)
	tracker.startEntry(9)
	tracker.updatePos(400)
	tracker.endEntry(9, false)
	tracker.endEntry(7, false)
	records := tracker.takeRecords()
	if len(records) != 2 || records[0].mediaKey != "movie:first" || !records[0].eof || records[1].mediaKey != "" {
		t.Fatalf("records = %+v", records)
	}
	if _, ok := tracker.unfinished(); ok {
		t.Fatal("ended entry reported as unfinished")
	}
}

func TestRevealEpisodeFocusesLastPlayed(t *testing.T) {
	show := testShow("show", "Show", 2024)
	show.Seasons = map[int]*papi.Season{}
	for season := 1; season <= 2; season++ {
		episodes := map[int]*papi.Episode{}
		for episode := 1; episode <= 3; episode++ {
			episodes[episode] = headlessEpisode(season, episode, "E")
		}
		show.Seasons[season] = &papi.Season{Season: season, Episodes: episodes}
	}
	a := &App{detail: &detailScreen{kind: detailShow, show: &showPage{showID: "show", seasons: createSeasonNodes(show), focus: focusTabs}}}
	a.revealEpisode(movieMediaKey("show"))
	if s := a.detail.show; s.seasonCursor != 0 || s.epCursor != 0 || s.focus != focusTabs {
		t.Fatalf("unrelated key moved focus: %+v", s)
	}
	a.revealEpisode(episodeMediaKey("show", 2, 3))
	if s := a.detail.show; s.seasonCursor != 1 || s.epCursor != 2 || s.focus != focusEpisodes {
		t.Fatalf("focus = season %d, episode %d, zone %v", s.seasonCursor, s.epCursor, s.focus)
	}
}
