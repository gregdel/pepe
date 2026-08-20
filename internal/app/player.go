package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/odwrtw/polochon/lib/papi"
)

const (
	minimumResumePosition = 30.0
	playbackTrackerWait   = 100 * time.Millisecond
	mpvConnectAttempts    = 30
)

type PlayerStatus string

const (
	PlayerStatusIdle    PlayerStatus = "Idle"
	PlayerStatusRunning PlayerStatus = "Running"
	PlayerStatusError   PlayerStatus = "Error"
)

type PlayerMode string

const (
	PlayerModeGUI PlayerMode = "gui"
	PlayerModeKMS PlayerMode = "kms"
)

type playerCommand struct {
	mediaKey string
	sockPath string
	args     []string
	mode     PlayerMode
}

type playbackSource struct {
	mediaKey  string
	title     string
	video     papi.Downloadable
	subtitles []*papi.Subtitle
}

func movieMediaKey(id string) string {
	return "movie:" + id
}

func episodeMediaKey(showID string, season, episode int) string {
	return fmt.Sprintf("episode:%s:%d:%d", showID, season, episode)
}

func mpvSocketPath() string {
	return fmt.Sprintf("/tmp/polochon-mpv-%d.sock", os.Getpid())
}

func (a *App) launchMovie(media *MediaItem) {
	if media == nil {
		a.setPlayerError("movie is not available")
		return
	}
	movie, ok := a.client.getMovie(media.ID)
	if !ok {
		a.setPlayerError("movie is not available")
		return
	}
	a.launchPlayback(playbackSource{
		mediaKey:  movieMediaKey(movie.ImdbID),
		title:     media.Title,
		video:     movie,
		subtitles: movie.Subtitles,
	})
}

func (a *App) launchEpisode(node *DynamicNode) {
	if node == nil || node.Episode == nil {
		a.setPlayerError("episode is not available")
		return
	}
	ep := node.Episode
	a.launchPlayback(playbackSource{
		mediaKey:  episodeMediaKey(node.ShowID, ep.Season, ep.Episode),
		title:     node.Label,
		video:     ep,
		subtitles: ep.Subtitles,
	})
}

func (a *App) launchPlayback(source playbackSource) {
	command, err := a.buildPlayerCommand(source)
	if err != nil {
		a.setPlayerError(err.Error())
		return
	}
	a.startPlayer(command)
	a.playbackTracker = startIPC(command.sockPath, command.mediaKey)
}

func (a *App) setPlayerError(message string) {
	a.playerStatus = PlayerStatusError
	a.playerError = message
}

func (a *App) buildPlayerCommand(source playbackSource) (playerCommand, error) {
	if source.mediaKey == "" || source.video == nil {
		return playerCommand{}, fmt.Errorf("playback source is incomplete")
	}

	videoURL, err := a.client.getDownloadURL(source.video)
	if err != nil {
		return playerCommand{}, fmt.Errorf("video URL: %w", err)
	}

	command := playerCommand{
		mediaKey: source.mediaKey,
		sockPath: mpvSocketPath(),
		mode:     detectPlayerMode(),
	}
	command.args = buildMPVArgs(
		a.mpvPath,
		source.title,
		videoURL,
		a.externalSubtitleURLs(source.subtitles),
		command.mode,
		a.resumePos[source.mediaKey],
		command.sockPath,
		a.playerFullscreen,
	)
	command.args = replaceMpvToken(command.args, a.client.getToken())
	return command, nil
}

func (a *App) externalSubtitleURLs(subtitles []*papi.Subtitle) []string {
	urls := make([]string, 0, len(subtitles))
	for _, subtitle := range subtitles {
		if subtitle == nil || subtitle.Embedded {
			continue
		}
		url, err := a.client.getDownloadURL(subtitle)
		if err == nil {
			urls = append(urls, url)
		}
	}
	return urls
}

func (a *App) startPlayer(command playerCommand) {
	if len(command.args) == 0 {
		return
	}
	a.playerStatus = PlayerStatusRunning
	a.playerMode = command.mode
	a.playerError = ""
	a.playerDone = make(chan error, 1)

	go func() {
		cmd := exec.Command(command.args[0], command.args[1:]...) //nolint:gosec
		a.playerDone <- cmd.Run()
		if a.invalidate != nil {
			a.invalidate()
		}
	}()
}

func (a *App) applyPlaybackSnapshot(tracker *playbackTracker) {
	if tracker == nil {
		return
	}
	tracker.wait(playbackTrackerWait)
	mediaKey, pos, eof := tracker.snapshot()
	if mediaKey == "" {
		return
	}
	if eof {
		delete(a.resumePos, mediaKey)
		return
	}
	if pos > minimumResumePosition {
		a.resumePos[mediaKey] = pos
	}
}

func detectPlayerMode() PlayerMode {
	return detectPlayerModeFor(runtime.GOOS, os.Getenv)
}

func detectPlayerModeFor(goos string, getenv func(string) string) PlayerMode {
	if goos != "linux" {
		return PlayerModeGUI
	}
	for _, name := range []string{"WAYLAND_DISPLAY", "DISPLAY"} {
		if getenv(name) != "" {
			return PlayerModeGUI
		}
	}
	sessionType := strings.ToLower(strings.TrimSpace(getenv("XDG_SESSION_TYPE")))
	if sessionType == "wayland" || sessionType == "x11" {
		return PlayerModeGUI
	}
	return PlayerModeKMS
}

func buildMPVArgs(mpvPath, title, videoURL string, subtitleURLs []string, mode PlayerMode, startPos float64, sockPath string, fullscreen bool) []string {
	if strings.TrimSpace(mpvPath) == "" {
		mpvPath = "mpv"
	}
	args := []string{mpvPath, "--http-header-fields=X-Auth-Token: $POLOCHON_TOKEN"}
	if startPos > minimumResumePosition {
		args = append(args, fmt.Sprintf("--start=%d", int(startPos)))
	}
	if mode == PlayerModeKMS {
		args = append(args, "--vo=gpu", "--gpu-context=drm", "--opengl-es=yes", "--fs", "--no-border", "--ontop")
	} else if fullscreen {
		args = append(args, "--fs", "--no-border")
	}
	if title != "" {
		args = append(args, "--title="+title)
	}
	for _, subtitleURL := range subtitleURLs {
		if subtitleURL != "" {
			args = append(args, "--sub-file="+subtitleURL)
		}
	}
	args = append(args, "--input-ipc-server="+sockPath, videoURL)
	return args
}

func replaceMpvToken(args []string, token string) []string {
	if token == "" {
		return args
	}
	out := make([]string, len(args))
	for i, arg := range args {
		out[i] = strings.ReplaceAll(arg, "$POLOCHON_TOKEN", token)
	}
	return out
}

// playbackTracker is updated by the mpv IPC goroutine without waking the UI.
type playbackTracker struct {
	mu       sync.RWMutex
	mediaKey string
	pos      float64
	eof      bool
	done     chan struct{}
}

func newPlaybackTracker(mediaKey string) *playbackTracker {
	return &playbackTracker{
		mediaKey: mediaKey,
		done:     make(chan struct{}),
	}
}

func (t *playbackTracker) updatePos(pos float64) {
	t.mu.Lock()
	t.pos = pos
	t.mu.Unlock()
}

func (t *playbackTracker) markEOF() {
	t.mu.Lock()
	t.eof = true
	t.mu.Unlock()
}

func (t *playbackTracker) snapshot() (mediaKey string, pos float64, eof bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.mediaKey, t.pos, t.eof
}

func (t *playbackTracker) wait(timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-t.done:
	case <-timer.C:
	}
}

// startIPC launches ipcLoop in a goroutine and returns a passive playback tracker.
func startIPC(sockPath, mediaKey string) *playbackTracker {
	tracker := newPlaybackTracker(mediaKey)
	go ipcLoop(sockPath, tracker)
	return tracker
}

// mpvEvent is the JSON event structure sent by mpv over IPC.
type mpvEvent struct {
	Event string `json:"event"`
	Name  string `json:"name"`
	Data  any    `json:"data"`
}

// ipcLoop connects to the mpv IPC socket and processes events.
func ipcLoop(sockPath string, tracker *playbackTracker) {
	defer close(tracker.done)

	var conn net.Conn
	for range mpvConnectAttempts {
		c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: sockPath, Net: "unix"})
		if err == nil {
			conn = c
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if conn == nil {
		return
	}
	defer func() { _ = conn.Close() }()

	// Send observe_property commands.
	_, _ = fmt.Fprintf(conn, "{\"command\": [\"observe_property\", 1, \"time-pos\"]}\n")
	_, _ = fmt.Fprintf(conn, "{\"command\": [\"observe_property\", 2, \"eof-reached\"]}\n")

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Bytes()
		var ev mpvEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.Event != "property-change" {
			continue
		}
		switch ev.Name {
		case "time-pos":
			if pos, ok := ev.Data.(float64); ok && pos > minimumResumePosition {
				tracker.updatePos(pos)
			}
		case "eof-reached":
			if reached, ok := ev.Data.(bool); ok && reached {
				tracker.markEOF()
				return
			}
		}
	}
}

// formatResumePos formats seconds as "H:MM:SS" (>= 3600s) or "MM:SS" (< 3600s).
func formatResumePos(secs float64) string {
	total := int(secs)
	s := total % 60
	m := (total / 60) % 60
	h := total / 3600
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}
