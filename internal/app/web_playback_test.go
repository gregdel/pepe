package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	gioapp "gioui.org/app"
	"gioui.org/io/event"
)

// Inactive windows receive wake-ups from Invalidate without a FrameEvent.
// Simulate those events while a URL playback session is running.
func TestWebPlaybackControlsWithoutFrames(t *testing.T) {
	listener, socket := controlListener(t)
	commands := []playerControl{playerControlPause, playerControlBack, playerControlNext}
	peerDone := make(chan error, 1)
	go func() {
		for _, command := range commands {
			conn, err := listener.Accept()
			if err != nil {
				peerDone <- err
				return
			}
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			var request struct {
				Command json.RawMessage `json:"command"`
			}
			scanner := bufio.NewScanner(conn)
			if !scanner.Scan() {
				err = fmt.Errorf("missing playback command")
			} else if err = json.Unmarshal(scanner.Bytes(), &request); err == nil {
				want, _ := json.Marshal(command.args())
				if string(request.Command) != string(want) {
					err = fmt.Errorf("command = %s, want %s", request.Command, want)
				} else {
					_, err = fmt.Fprintln(conn, `{"request_id":1,"error":"success"}`)
				}
			}
			_ = conn.Close()
			if err != nil {
				peerDone <- err
				return
			}
		}
		peerDone <- nil
	}()

	a := New(nil, nil, nil, nil, nil)
	defer a.Close()
	if err := a.startWebUI("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	a.playerStatus = PlayerStatusRunning
	a.playerFromWeb = true
	a.playerTitle = "Web UI video"
	a.playerSocket = socket
	a.playerSession = 1
	a.playerStop = make(chan struct{})
	a.playerDone = make(chan playerResult, 1)
	a.publishWebStatus()
	handler := a.webUI.handler()

	queue := func(command playerControl) {
		t.Helper()
		response := webRequest(handler, "POST", "/api/player", fmt.Sprintf(`{"command":%q}`, command))
		if response.Code != http.StatusAccepted {
			t.Fatalf("queue %s: %s", command, response.Body.String())
		}
	}
	status := func() webStatus {
		t.Helper()
		response := webRequest(handler, "GET", "/api/status", "")
		var result webStatus
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}

	step := 0
	stop := a.playerStop
	if err := a.run(func() event.Event {
		// ConfigEvent stands in for Gio's private wake-up event: no drawing.
		step++
		switch step {
		case 1:
			queue(commands[0])
		case 2, 4, 6:
			if a.playerControls == nil {
				t.Fatal("playback control was not dispatched without a frame")
			}
			waitForCache(t, func() bool { return len(a.playerControls.results) == 1 })
		case 3, 5:
			if got := status(); got.CommandError != "" || got.QueuedCommands != 0 {
				t.Fatalf("control status = %+v", got)
			}
			queue(commands[(step-1)/2])
		case 7:
			if err := <-peerDone; err != nil {
				t.Fatal(err)
			}
			queue(playerControlStop)
		case 8:
			select {
			case <-stop:
			default:
				t.Fatal("Stop was not dispatched without a frame")
			}
			if got := status(); !got.Stopping || got.CommandError != "" {
				t.Fatalf("stop status = %+v", got)
			}
			a.playerDone <- playerResult{}
		case 9:
			if got := status(); got.State != PlayerStatusIdle || got.Stopping || got.Source != "" {
				t.Fatalf("completion status = %+v", got)
			}
			return gioapp.DestroyEvent{}
		default:
			t.Fatal("event loop did not exit")
		}
		return gioapp.ConfigEvent{}
	}); err != nil {
		t.Fatal(err)
	}
}
