package app

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// Connect a fake mpv peer and consume the tracker's property subscriptions.
func playbackIPCPeer(t *testing.T, key string, onTitle func()) (*playbackTracker, net.Conn) {
	t.Helper()
	dir, _, socket, err := createPlayerRuntime("secret")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	if err := listener.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	tracker := startIPC(socket, key, time.Second, onTitle)
	t.Cleanup(func() {
		tracker.stopIPC()
		select {
		case <-tracker.done:
		case <-time.After(time.Second):
			t.Error("IPC tracker did not stop")
		}
	})
	conn, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(conn)
	for _, property := range []string{"time-pos", "eof-reached", "force-media-title", "metadata/by-key/title"} {
		if !scanner.Scan() || !strings.Contains(scanner.Text(), `"`+property+`"`) {
			t.Fatalf("missing %s subscription: %s, %v", property, scanner.Text(), scanner.Err())
		}
	}
	return tracker, conn
}

func TestPlayerIPCEndFileResume(t *testing.T) {
	for _, key := range []string{movieMediaKey("first"), episodeMediaKey("show", 3, 4)} {
		for _, reason := range []string{"eof", "stop", "quit", "error", "redirect", "", "unknown"} {
			t.Run(key+"/"+reason, func(t *testing.T) {
				tracker, conn := playbackIPCPeer(t, key, nil)
				endEvent := fmt.Sprintf("{\"event\":\"end-file\",\"reason\":%q}\n", reason)
				if reason == "" {
					endEvent = "{\"event\":\"end-file\"}\n"
				}
				_, err := fmt.Fprint(conn, "not json\n{\"event\":\"property-change\",\"name\":\"time-pos\",\"data\":180}\n{\"event\":\"property-change\",\"name\":\"eof-reached\",\"data\":false}\n"+endEvent)
				if err != nil {
					t.Fatal(err)
				}
				_ = conn.Close()
				select {
				case <-tracker.done:
				case <-time.After(time.Second):
					t.Fatal("IPC tracker did not finish")
				}
				_, pos, eof := tracker.snapshot()
				if pos != 180 || eof != (reason == "eof") {
					t.Fatalf("snapshot = %v, %v", pos, eof)
				}
				a := New(nil, nil, nil, nil, nil)
				a.progress.entries[key] = progressEntry{Pos: 42}
				a.progress.entries["movie:unrelated"] = progressEntry{Pos: 99}
				a.playerDone = make(chan playerResult, 1)
				a.playerStatus = PlayerStatusRunning
				a.playerDone <- playerResult{tracker: tracker}
				a.drainChannels()
				entry, exists := a.progress.entries[key]
				resume := entry.Pos
				if reason == "eof" {
					if !entry.Watched || resume != 0 {
						t.Fatalf("finished media = %+v, want watched without resume position", entry)
					}
				} else if !exists || resume != 180 {
					t.Fatalf("interrupted resume = %v, exists = %v", resume, exists)
				}
				args := buildMPVArgs("mpv", "Title", "video", nil, PlayerModeGUI, resume, "socket", false, "config")
				if got := strings.Contains(strings.Join(args, " "), "--start=180"); got != (reason != "eof") {
					t.Fatalf("replay arguments = %v", args)
				}
				if a.progress.entries["movie:unrelated"].Pos != 99 || a.playerStatus != PlayerStatusIdle || a.playerDone != nil {
					t.Fatal("completion changed unrelated resume or left stale player state")
				}
			})
		}
	}
}

func TestPlayerIPCRedirectContinuesTracking(t *testing.T) {
	tracker, conn := playbackIPCPeer(t, "movie:first", nil)
	_, err := fmt.Fprint(conn, "{\"event\":\"end-file\",\"reason\":\"redirect\"}\n{\"event\":\"property-change\",\"name\":\"time-pos\",\"data\":180}\n{\"event\":\"end-file\",\"reason\":\"eof\"}\n")
	if err != nil {
		t.Fatal(err)
	}
	tracker.finishIPC()
	_, pos, eof := tracker.snapshot()
	if pos != 180 || !eof {
		t.Fatalf("redirected playback snapshot = %v, %v", pos, eof)
	}
}

func TestPlaybackTrackerFinishDrainsBeforeStopping(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tracker := newPlaybackTracker("movie:first")
		go func() {
			time.Sleep(10 * time.Millisecond)
			select {
			case <-tracker.stop:
				t.Error("IPC stopped before final events could drain")
			default:
				tracker.markEOF()
			}
			close(tracker.done)
		}()
		tracker.finishIPC()
		_, _, eof := tracker.snapshot()
		if !eof {
			t.Fatal("final EOF was lost")
		}
	})
}

func TestPlayerCompletionPublishesSettledTracker(t *testing.T) {
	for _, connected := range []bool{false, true} {
		t.Run(fmt.Sprint(connected), func(t *testing.T) {
			a := New(nil, nil, &Config{MPVStartupTimeout: 15 * time.Second}, nil, nil)
			gate := t.TempDir() + "/exit"
			t.Setenv("PEPE_TEST_EXIT_GATE", gate)
			command := lifecycleCommand(t, a, "while [ ! -f \"$PEPE_TEST_EXIT_GATE\" ]; do sleep 0.01; done\nexit 0\n")
			if connected {
				listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: command.sockPath, Net: "unix"})
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = listener.Close() }()
				_ = listener.SetDeadline(time.Now().Add(time.Second))
				startLifecyclePlayer(t, a, command)
				conn, err := listener.Accept()
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(time.Second))
				scanner := bufio.NewScanner(conn)
				for range 3 {
					if !scanner.Scan() {
						t.Fatal("missing IPC subscription")
					}
				}
			} else {
				startLifecyclePlayer(t, a, command)
			}
			if err := os.WriteFile(gate, nil, 0600); err != nil {
				t.Fatal(err)
			}
			select {
			case result := <-a.playerDone:
				if result.err != nil {
					t.Fatal(result.err)
				}
				select {
				case <-result.tracker.done:
				default:
					t.Fatal("result published before IPC tracker stopped")
				}
			case <-time.After(time.Second):
				t.Fatal("IPC blocked process completion")
			}
		})
	}
}

func TestPlaybackTrackerFinishCancelsSilentPeer(t *testing.T) {
	tracker, _ := playbackIPCPeer(t, "movie:first", nil)
	finished := make(chan struct{})
	go func() {
		tracker.finishIPC()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("silent IPC peer blocked completion")
	}
	select {
	case <-tracker.done:
	default:
		t.Fatal("completion returned before tracker stopped")
	}
}
