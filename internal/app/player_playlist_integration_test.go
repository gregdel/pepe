package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// playlistCheckScript logs each started entry, and once plays the inserted
// entry from the end of the appended one.
const playlistCheckScript = `
local jumped = false
mp.register_event("playback-restart", function()
    local subs = {}
    for _, track in ipairs(mp.get_property_native("track-list")) do
        if track.type == "sub" and track["external-filename"] then
            subs[#subs + 1] = track["external-filename"]:match("[^/]+$")
        end
    end
    mp.msg.warn(string.format("entry title=%s window=%s start=%d subs=%s", mp.get_property("media-title"), mp.get_property("title"),
        math.floor(mp.get_property_number("time-pos", -1) + 0.5), table.concat(subs, ",")))
    if mp.get_property("media-title") == "After" and not jumped then
        jumped = true
        mp.commandv("playlist-play-index", "0")
    end
end)
`

// Opt-in real-mpv test of queued episodes: per-entry options never leak
// from the launched file, and each entry is recorded under its own key.
// PEPE_TEST_MPV=1 go test ./internal/app -run TestMPVInterfaceIntegration -count=1
func TestMPVInterfaceIntegrationPlaylist(t *testing.T) {
	if os.Getenv("PEPE_TEST_MPV") != "1" {
		t.Skip("set PEPE_TEST_MPV=1 to exercise the installed mpv")
	}
	dir := t.TempDir()
	for _, name := range []string{"launched.srt", "before.srt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("1\n00:00:00,000 --> 00:00:09,000\n"+name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(dir, "check.lua")
	if err := os.WriteFile(script, []byte(playlistCheckScript), 0o600); err != nil {
		t.Fatal(err)
	}
	const video = "av://lavfi:color=size=64x36:rate=10:duration=3"
	socket := filepath.Join(dir, "mpv.sock")
	tracker := newPlaybackTracker("launched", nil)
	tracker.before = []playlistEntry{{mediaKey: "before", title: "Before", url: video, subtitleURLs: []string{filepath.Join(dir, "before.srt")}, start: 0}}
	tracker.after = []playlistEntry{{mediaKey: "after", title: "After", url: video, start: 0}}
	startIPC(socket, tracker, 5*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "mpv", "--no-config", "--load-scripts=no", "--vo=null", "--ao=null",
		"--msg-level=all=warn", "--script="+script, "--start=2", "--title=Launched", "--force-media-title=Launched",
		"--sub-file="+filepath.Join(dir, "launched.srt"), "--input-ipc-server="+socket, video)
	output, err := cmd.CombinedOutput()
	tracker.finishIPC()
	if err != nil {
		t.Fatalf("mpv: %v\n%s", err, output)
	}
	var entries []string
	for line := range strings.SplitSeq(string(output), "\n") {
		if _, entry, ok := strings.Cut(line, "[check] entry "); ok {
			entries = append(entries, entry)
		}
	}
	want := []string{
		"title=Launched window=Launched start=2 subs=launched.srt",
		"title=After window=After start=0 subs=",
		"title=Before window=Before start=0 subs=before.srt",
		"title=Launched window=Launched start=2 subs=launched.srt",
		"title=After window=After start=0 subs=",
	}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("entries =\n%s\nwant\n%s\nmpv output:\n%s", strings.Join(entries, "\n"), strings.Join(want, "\n"), output)
	}
	var keys []string
	var eofs []bool
	for _, record := range tracker.takeRecords() {
		keys, eofs = append(keys, record.mediaKey), append(eofs, record.eof)
	}
	if !reflect.DeepEqual(keys, []string{"launched", "after", "before", "launched", "after"}) ||
		!reflect.DeepEqual(eofs, []bool{true, false, true, true, true}) {
		t.Fatalf("records = %v, eof %v", keys, eofs)
	}
}
