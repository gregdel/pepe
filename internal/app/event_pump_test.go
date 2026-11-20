package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"image"
	"net/http"
	"testing"
	"time"

	gioapp "gioui.org/app"
	"gioui.org/io/event"
	"gioui.org/op"
	"gioui.org/unit"
)

// Behind direct-DRM mpv the compositor stops releasing buffers, so the event
// loop can block in FrameEvent.Frame for the whole playback. The pump must
// still deliver web remote actions to mpv.
func TestEventPumpControlsPlaybackWhileFrameBlocks(t *testing.T) {
	listener, socket := controlListener(t)
	received := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			received <- err.Error()
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		var request struct {
			Command json.RawMessage `json:"command"`
		}
		scanner := bufio.NewScanner(conn)
		if !scanner.Scan() || json.Unmarshal(scanner.Bytes(), &request) != nil {
			received <- "missing playback command"
			return
		}
		_, _ = fmt.Fprintln(conn, `{"request_id":1,"error":"success"}`)
		received <- string(request.Command)
	}()

	wake := make(chan struct{}, 1)
	a := New(nil, nil, nil, func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}, nil)
	defer a.Close()
	if err := a.startWebUI("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	a.playerStatus = PlayerStatusRunning
	a.playerSocket = socket
	a.playerSession = 1
	a.playerDone = make(chan playerResult, 1)
	a.publishWebStatus()

	framing, release := make(chan struct{}), make(chan struct{})
	loopDone := make(chan error, 1)
	go func() {
		frames := 0
		loopDone <- a.run(func() event.Event {
			if frames++; frames > 1 {
				return gioapp.DestroyEvent{}
			}
			return gioapp.FrameEvent{
				Now: time.Now(), Size: image.Pt(1920, 1080),
				Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
				Frame: func(*op.Ops) {
					close(framing)
					<-release
				},
			}
		})
	}()
	<-framing
	defer func() {
		close(release)
		if err := <-loopDone; err != nil {
			t.Error(err)
		}
	}()
	defer a.pumpEvents(wake)()

	response := webRequest(a.webUI.handler(), "POST", "/api/action", `{"action":"ok"}`)
	if response.Code != http.StatusAccepted {
		t.Fatalf("action: %s", response.Body.String())
	}
	want, _ := json.Marshal(playerControlPause.args())
	select {
	case got := <-received:
		if got != string(want) {
			t.Fatalf("mpv command = %s, want %s", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("remote action did not reach mpv while the frame was blocked")
	}
	waitForCache(t, func() bool {
		response := webRequest(a.webUI.handler(), "GET", "/api/status", "")
		var status webStatus
		return json.Unmarshal(response.Body.Bytes(), &status) == nil && status.QueuedCommands == 0
	})
}
