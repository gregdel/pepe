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
	// Previous/next mpv playlist entry: queued episodes or a web playlist.
	playerControlPlaylistPrev playerControl = "playlist-prev"
	playerControlPlaylistNext playerControl = "playlist-next"
	// Absolute seek from the web remote's progress bar; it carries a
	// position, so it is not a valid() fixed command.
	playerControlSeek playerControl = "seek"
)

func (command playerControl) valid() bool {
	switch command {
	case playerControlPause, playerControlBack, playerControlNext, playerControlStop,
		playerControlPlaylistPrev, playerControlPlaylistNext:
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
	case playerControlPlaylistPrev:
		return []any{"playlist-prev"}
	case playerControlPlaylistNext:
		return []any{"playlist-next"}
	default:
		return nil
	}
}

// playerControls serializes commands for one immutable playback socket. It is
// canceled and discarded before any replacement can start, including its replies.
type playerControls struct {
	commands chan []any // mpv IPC commands
	results  chan error
	cancel   context.CancelFunc
	done     chan struct{}
}

func newPlayerControls(socket string, invalidate func()) *playerControls {
	ctx, cancel := context.WithCancel(context.Background())
	controls := &playerControls{
		commands: make(chan []any, 16), results: make(chan error, 16),
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

func sendPlayerControl(parent context.Context, socket string, args []any) error {
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
	a.queuePlayerCommand(command.args())
}

// handlePlayerSeek seeks the playing entry to position seconds. playlistPos is
// the entry the phone showed, so a seek never lands in the next episode.
func (a *App) handlePlayerSeek(position float64, playlistPos int) {
	if a.playerStatus != PlayerStatusRunning {
		a.webCommandError = "No playback is running."
		return
	}
	if a.playerTracker != nil {
		if pos, _ := a.playerTracker.playlist(); pos != playlistPos {
			a.webCommandError = "Playback changed; try again."
			return
		}
	}
	a.queuePlayerCommand([]any{"seek", position, "absolute"})
}

func (a *App) queuePlayerCommand(args []any) {
	if a.playerStopping || a.pendingPlayback != nil {
		a.webCommandError = "Playback is stopping."
		return
	}
	if a.playerControls == nil {
		a.playerControls = newPlayerControls(a.playerSocket, a.invalidate)
	}
	select {
	case a.playerControls.commands <- args:
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

// remoteActions name the web remote actions that drive mpv while it plays.
var remoteActions = map[inputAction]string{
	actionUp: "up", actionDown: "down", actionLeft: "left", actionRight: "right",
	actionActivate: "ok", actionBack: "back", actionSubtitles: "subtitles",
}

// remoteActionArgs maps a remote action to mpv. pepe.lua handles it when
// loaded, so its subtitle selector receives the arrows; otherwise it maps to
// fixed player commands.
func remoteActionArgs(action inputAction, scripted bool) []any {
	name, ok := remoteActions[action]
	switch {
	case !ok:
		return nil
	case scripted:
		return []any{"script-message-to", "pepe", "remote", name}
	}
	switch action {
	case actionLeft:
		return playerControlBack.args()
	case actionRight:
		return playerControlNext.args()
	case actionActivate:
		return playerControlPause.args()
	case actionSubtitles:
		return []any{"cycle", "sub"}
	case actionUp:
		return []any{"keypress", "UP"}
	case actionDown:
		return []any{"keypress", "DOWN"}
	}
	return nil
}

// handleRemoteAction sends web remote navigation to mpv while it plays, so the
// remote never moves the hidden library UI. It reports whether it handled it.
func (a *App) handleRemoteAction(action inputAction) bool {
	if a.playerStatus != PlayerStatusRunning {
		return false
	}
	if action == actionToggleSection || action == actionToggleWatched {
		a.webCommandError = "Not available during playback."
		return true
	}
	// Without pepe.lua there is no player menu for Back to close.
	if action == actionHome || action == actionBack && !a.playerScripted {
		a.handlePlayerControl(playerControlStop)
		return true
	}
	args := remoteActionArgs(action, a.playerScripted)
	if args == nil {
		return false
	}
	a.queuePlayerCommand(args)
	return true
}
