package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

type playerControl string

const (
	playerControlPause playerControl = "pause"
	playerControlBack  playerControl = "seek-back"
	playerControlNext  playerControl = "seek-forward"
	playerControlStop  playerControl = "stop"
)

func (command playerControl) valid() bool {
	switch command {
	case playerControlPause, playerControlBack, playerControlNext, playerControlStop:
		return true
	default:
		return false
	}
}

func (command playerControl) args() []any {
	switch command {
	case playerControlPause:
		return []any{"cycle", "pause"}
	case playerControlBack:
		return []any{"seek", -10, "relative"}
	case playerControlNext:
		return []any{"seek", 10, "relative"}
	default:
		return nil
	}
}

// playerControls serializes commands for one immutable playback socket. It is
// canceled and discarded before any replacement can start, including its replies.
type playerControls struct {
	commands chan playerControl
	results  chan error
	cancel   context.CancelFunc
	done     chan struct{}
}

func newPlayerControls(socket string, invalidate func()) *playerControls {
	ctx, cancel := context.WithCancel(context.Background())
	controls := &playerControls{
		commands: make(chan playerControl, 16), results: make(chan error, 16),
		cancel: cancel, done: make(chan struct{}),
	}
	go func() {
		defer close(controls.done)
		for {
			select {
			case <-ctx.Done():
				return
			case command := <-controls.commands:
				if ctx.Err() != nil {
					return
				}
				err := sendPlayerControl(ctx, socket, command)
				select {
				case <-ctx.Done():
					return
				case controls.results <- err:
				}
				if invalidate != nil {
					invalidate()
				}
			}
		}
	}()
	return controls
}

func sendPlayerControl(parent context.Context, socket string, command playerControl) error {
	args := command.args()
	if args == nil {
		return fmt.Errorf("unknown playback command")
	}
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return fmt.Errorf("mpv controls are unavailable; try again once playback has started")
	}
	defer func() { _ = conn.Close() }()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("could not set mpv control deadline")
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	request := struct {
		Command   []any `json:"command"`
		RequestID int   `json:"request_id"`
	}{Command: args, RequestID: 1}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return fmt.Errorf("could not send playback command")
	}
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 1024), 16<<10)
	for scanner.Scan() {
		var reply struct {
			RequestID int    `json:"request_id"`
			Error     string `json:"error"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &reply); err != nil || reply.RequestID != request.RequestID {
			continue
		}
		if reply.Error != "success" {
			return fmt.Errorf("mpv rejected playback command")
		}
		return nil
	}
	return fmt.Errorf("mpv did not acknowledge playback command")
}

func (a *App) handlePlayerControl(command playerControl) {
	if !command.valid() {
		a.webCommandError = "Unknown playback command."
		return
	}
	if a.playerStatus != PlayerStatusRunning {
		a.webCommandError = "No playback is running."
		return
	}
	if command == playerControlStop {
		a.pendingPlayback = nil
		a.requestPlayerStop()
		return
	}
	if a.playerStopping || a.pendingPlayback != nil {
		a.webCommandError = "Playback is stopping."
		return
	}
	if a.playerControls == nil {
		a.playerControls = newPlayerControls(a.playerSocket, a.invalidate)
	}
	select {
	case a.playerControls.commands <- command:
	default:
		a.webCommandError = "Playback controls are busy; try again."
	}
}

func (a *App) cancelPlayerControls() {
	if a.playerControls != nil {
		a.playerControls.cancel()
		a.playerControls = nil
	}
}

func (a *App) drainPlayerControls() {
	if a.playerControls == nil {
		return
	}
	for {
		select {
		case err := <-a.playerControls.results:
			a.webCommandError = ""
			if err != nil {
				a.logger().Warn("player control failed", "error", err)
				a.webCommandError = err.Error()
			}
		default:
			return
		}
	}
}
