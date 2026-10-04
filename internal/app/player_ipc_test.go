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
func playbackIPCPeer(t *testing.T, key string) (*playbackTracker, net.Conn) {
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
	tracker := startIPC(socket, key, time.Second)
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
	for range 2 {
		if !scanner.Scan() {
			t.Fatalf("missing property subscription: %v", scanner.Err())
		}
	}
	return tracker, conn
}

func TestPlayerIPCEndFileResume(t *testing.T) {
	for _, key := range []string{movieMediaKey("first"), episodeMediaKey("show", 3, 4)} {
		for _, reason := range []string{"eof", "stop", "quit", "error", "redirect", "", "unknown"} {
			t.Run(key+"/"+reason, func(t *testing.T) {
				tracker, conn := playbackIPCPeer(t, key)
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
				a.resumePos[key] = 42
				a.resumePos["movie:unrelated"] = 99
				a.playerDone = make(chan playerResult, 1)
				a.playerStatus = PlayerStatusRunning
				a.playerDone <- playerResult{tracker: tracker}
				a.drainChannels()
				resume, exists := a.resumePos[key]
				if reason == "eof" {
					if exists {
						t.Fatal("finished media retained its resume position")
					}
				} else if !exists || resume != 180 {
					t.Fatalf("interrupted resume = %v, exists = %v", resume, exists)
				}
				args := buildMPVArgs("mpv", "Title", "video", nil, PlayerModeGUI, resume, "socket", false, "config")
				if got := strings.Contains(strings.Join(args, " "), "--start=180"); got != (reason != "eof") {
					t.Fatalf("replay arguments = %v", args)
				}
				if a.resumePos["movie:unrelated"] != 99 || a.playerStatus != PlayerStatusIdle || a.playerDone != nil {
					t.Fatal("completion changed unrelated resume or left stale player state")
				}
			})
		}
	}
}

func TestPlayerIPCRedirectContinuesTracking(t *testing.T) {
	tracker, conn := playbackIPCPeer(t, "movie:first")
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
				for range 2 {
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
	tracker, _ := playbackIPCPeer(t, "movie:first")
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
