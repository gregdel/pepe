package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func controlListener(t *testing.T) (net.Listener, string) {
	t.Helper()
	dir, _, socket, err := createPlayerRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	if err := listener.(*net.UnixListener).SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return listener, socket
}

func TestPlayerControlCommandsAndReplies(t *testing.T) {
	for _, command := range []playerControl{playerControlPause, playerControlBack, playerControlNext} {
		for _, success := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/success=%t", command, success), func(t *testing.T) {
				listener, socket := controlListener(t)
				peerDone := make(chan error, 1)
				go func() {
					conn, err := listener.Accept()
					if err != nil {
						peerDone <- err
						return
					}
					defer func() { _ = conn.Close() }()
					_ = conn.SetDeadline(time.Now().Add(time.Second))
					var request struct {
						Command   json.RawMessage `json:"command"`
						RequestID int             `json:"request_id"`
					}
					scanner := bufio.NewScanner(conn)
					if !scanner.Scan() {
						peerDone <- fmt.Errorf("missing newline-framed command")
						return
					}
					if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
						peerDone <- err
						return
					}
					want, _ := json.Marshal(command.args())
					if string(request.Command) != string(want) || request.RequestID != 1 {
						peerDone <- fmt.Errorf("incorrect request: %s", scanner.Bytes())
						return
					}
					result := "invalid parameter"
					if success {
						result = "success"
					}
					_, err = fmt.Fprintf(conn, "not json\n{\"event\":\"property-change\"}\n{\"request_id\":99,\"error\":\"failure\"}\n{\"request_id\":1,\"error\":%q}\n", result)
					peerDone <- err
				}()
				err := sendPlayerControl(context.Background(), socket, command.args())
				if (err == nil) != success {
					t.Fatalf("control result = %v", err)
				}
				if err := <-peerDone; err != nil {
					t.Fatal(err)
				}
			})
		}
	}
	if playerControl("run").args() != nil || sendPlayerControl(context.Background(), "", nil) == nil {
		t.Fatal("arbitrary IPC command accepted")
	}
}

func TestPlayerControlTimeoutCancellationAndUnavailable(t *testing.T) {
	for _, cancelEarly := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%t", cancelEarly), func(t *testing.T) {
			listener, socket := controlListener(t)
			read := make(chan struct{})
			peerDone := make(chan struct{})
			go func() {
				defer close(peerDone)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(time.Second))
				scanner := bufio.NewScanner(conn)
				if scanner.Scan() {
					close(read)
				}
				// A silent peer must not hold the control worker indefinitely.
				for scanner.Scan() {
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			if cancelEarly {
				go func() {
					select {
					case <-read:
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			err := sendPlayerControl(ctx, socket, playerControlPause.args())
			if err == nil || strings.Contains(err.Error(), socket) {
				t.Fatalf("timeout/cancel result = %v", err)
			}
			<-peerDone
		})
	}
	_, socket := controlListener(t)
	if err := sendPlayerControl(context.Background(), socket+"-missing", playerControlPause.args()); err == nil || strings.Contains(err.Error(), socket) {
		t.Fatalf("unavailable result = %v", err)
	}
}

func TestPlayerControlsSerializeAndCancelQueuedCommands(t *testing.T) {
	listener, socket := controlListener(t)
	commands := []playerControl{playerControlPause, playerControlBack, playerControlNext}
	seen := make(chan string, len(commands))
	peerDone := make(chan error, 1)
	go func() {
		for range commands {
			conn, err := listener.Accept()
			if err != nil {
				peerDone <- err
				return
			}
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			var request struct {
				Command json.RawMessage `json:"command"`
			}
			err = json.NewDecoder(conn).Decode(&request)
			if err == nil {
				seen <- string(request.Command)
				_, err = fmt.Fprintln(conn, `{"request_id":1,"error":"success"}`)
			}
			_ = conn.Close()
			if err != nil {
				peerDone <- err
				return
			}
		}
		peerDone <- nil
	}()
	controls := newPlayerControls(socket, nil)
	defer func() { controls.cancel(); <-controls.done }()
	for _, command := range commands {
		controls.commands <- command.args()
	}
	for _, command := range commands {
		want, _ := json.Marshal(command.args())
		select {
		case got := <-seen:
			if got != string(want) {
				t.Fatalf("control order = %s, want %s", got, want)
			}
		case <-time.After(time.Second):
			t.Fatal("control worker stalled")
		}
		select {
		case err := <-controls.results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("control result missing")
		}
	}
	if err := <-peerDone; err != nil {
		t.Fatal(err)
	}
	controls.cancel()
	<-controls.done
	// Even if a command arrives after cancellation, this session cannot execute it.
	controls.commands <- playerControlPause.args()
	if len(controls.commands) != 1 {
		t.Fatal("canceled session consumed a late command")
	}
}

func TestPlayerControlStateAndStop(t *testing.T) {
	a := New(nil, nil, nil, nil, nil)
	defer a.Close()
	a.handlePlayerControl(playerControlPause)
	if a.webCommandError == "" || a.playerStatus != PlayerStatusIdle {
		t.Fatal("idle control changed player status or failed silently")
	}
	a.playerStatus = PlayerStatusRunning
	a.playerStop = make(chan struct{})
	a.pendingPlayback = &playbackSource{url: "https://media/next"}
	stop := a.playerStop
	a.handlePlayerControl(playerControlPause)
	if a.playerControls != nil || a.webCommandError != "Playback is stopping." {
		t.Fatal("control accepted while replacing playback")
	}
	a.playerControls = newPlayerControls("/nonexistent", nil)
	controls := a.playerControls
	a.handlePlayerControl(playerControlStop)
	if a.pendingPlayback != nil || !a.playerStopping || a.playerControls != nil {
		t.Fatal("Stop left replacement or control worker active")
	}
	select {
	case <-stop:
	default:
		t.Fatal("Stop did not signal process worker")
	}
	<-controls.done
	a.handlePlayerControl(playerControlStop) // no double-close panic
}

func TestRemoteActionsDrivePlayer(t *testing.T) {
	a := New(nil, nil, nil, nil, nil)
	defer a.Close()
	if a.handleRemoteAction(actionUp) {
		t.Fatal("idle remote action did not go to the library UI")
	}
	queued := func() []any {
		t.Helper()
		select {
		case args := <-a.playerControls.commands:
			return args
		default:
			t.Fatal("remote action was not queued for mpv")
			return nil
		}
	}
	done := make(chan struct{})
	close(done)
	a.playerStatus = PlayerStatusRunning
	a.playerControls = &playerControls{commands: make(chan []any, 16), cancel: func() {}, done: done}
	for _, scripted := range []bool{true, false} {
		a.playerScripted = scripted
		for action, want := range map[inputAction]string{
			actionUp:        `["script-message-to","pepe","remote","up"]`,
			actionActivate:  `["script-message-to","pepe","remote","ok"]`,
			actionBack:      `["script-message-to","pepe","remote","back"]`,
			actionSubtitles: `["script-message-to","pepe","remote","subtitles"]`,
			actionRefresh:   "",
			actionQuit:      "",
		} {
			if !scripted {
				want = map[inputAction]string{
					actionUp: `["keypress","UP"]`, actionActivate: `["cycle","pause"]`,
					actionSubtitles: `["cycle","sub"]`,
				}[action]
				if action == actionBack {
					continue // stops playback, checked below
				}
			}
			if handled := a.handleRemoteAction(action); handled != (want != "") {
				t.Fatalf("scripted=%t action %v handled = %t", scripted, action, handled)
			}
			if want == "" {
				continue
			}
			if got, _ := json.Marshal(queued()); string(got) != want {
				t.Fatalf("scripted=%t action %v sent %s, want %s", scripted, action, got, want)
			}
		}
	}
	if !a.handleRemoteAction(actionToggleSection) || len(a.playerControls.commands) != 0 || a.webCommandError == "" {
		t.Fatal("section switch moved the hidden library UI during playback")
	}
	a.playerStop = make(chan struct{})
	if !a.handleRemoteAction(actionBack) || !a.playerStopping {
		t.Fatal("Back without the player script did not stop playback")
	}
}
