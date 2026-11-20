package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/odwrtw/polochon/lib/papi"
)

const (
	minimumResumePosition = 30.0
	playbackTrackerWait   = 100 * time.Millisecond
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
	mediaKey   string
	title      string
	sockPath   string
	runtimeDir string
	args       []string
	mode       PlayerMode
	fromWeb    bool
}

type playerResult struct {
	err             error
	tracker         *playbackTracker
	retainedRuntime string
}

// Managed launchers confirm worker teardown and recovery before runtime cleanup.
type playerPolicy struct {
	managedLauncher bool
	connectTimeout  time.Duration
	stopTimeout     time.Duration
}

func playerPolicyFromConfig(config *Config) playerPolicy {
	var policy playerPolicy
	if config != nil {
		policy.managedLauncher = config.MPVManagedLauncher
		policy.connectTimeout = config.MPVStartupTimeout
		policy.stopTimeout = config.MPVStopTimeout
	}
	return policy.withDefaults()
}

// withDefaults also supports apps constructed directly without New.
func (p playerPolicy) withDefaults() playerPolicy {
	if p.connectTimeout <= 0 {
		p.connectTimeout = defaultMPVStartupTimeout
	}
	if p.stopTimeout <= 0 {
		p.stopTimeout = defaultMPVStopTimeout
	}
	return p
}

func launcherComplete(runtimeDir string) bool {
	data, err := os.ReadFile(filepath.Join(runtimeDir, "launcher-complete"))
	return err == nil && string(data) == "stopped-and-restored\n"
}

type playbackSource struct {
	mediaKey  string
	title     string
	video     papi.Downloadable
	subtitles []*papi.Subtitle
	url       string // nonempty only for credential-free web playback
}

func movieMediaKey(id string) string {
	return "movie:" + id
}

func episodeMediaKey(showID string, season, episode int) string {
	return fmt.Sprintf("episode:%s:%d:%d", showID, season, episode)
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
		title:     formatTitle(movie.Title, movie.Year),
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
	title := node.Label
	if show, ok := a.client.getShow(node.ShowID); ok && show != nil && show.Title != "" {
		title = show.Title + " " + title
	}
	a.launchPlayback(playbackSource{
		mediaKey:  episodeMediaKey(node.ShowID, ep.Season, ep.Episode),
		title:     title,
		video:     ep,
		subtitles: ep.Subtitles,
	})
}

const (
	maxPlaybackURLLength = 8192
	webPlaybackTitle     = "Web UI video"
)

// validatePlaybackURL preserves signed URLs and never includes the input in errors.
func validatePlaybackURL(raw string) (string, error) {
	if len(raw) > maxPlaybackURLLength || !utf8.ValidString(raw) {
		return "", fmt.Errorf("URL is too long or is not valid UTF-8")
	}
	for _, char := range raw {
		if char < 0x20 || char == 0x7f {
			return "", fmt.Errorf("URL contains a control character")
		}
	}
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.Opaque != "" {
		return "", fmt.Errorf("an absolute HTTP or HTTPS URL is required")
	}
	return raw, nil
}

func (a *App) launchURL(raw string) error {
	videoURL, err := validatePlaybackURL(raw)
	if err != nil {
		return err
	}
	a.launchPlayback(playbackSource{title: webPlaybackTitle, url: videoURL})
	return nil
}

func (a *App) launchPlayback(source playbackSource) {
	if a.playerStatus == PlayerStatusRunning {
		a.pendingPlayback = &source
		a.requestPlayerStop()
		if a.invalidate != nil {
			a.invalidate()
		}
		return
	}
	command, err := a.buildPlayerCommand(source)
	if err != nil {
		a.setPlayerError(err.Error())
		return
	}
	a.startPlayer(command)
}

func (a *App) setPlayerError(message string) {
	if a.playerStatus == PlayerStatusRunning {
		return
	}
	a.logger().Error("playback failed", "error", message)
	a.playerStatus = PlayerStatusError
	a.playerError = message
}

// requestPlayerStop is event-loop-only and safe to call repeatedly.
func (a *App) requestPlayerStop() {
	if a.playerStatus != PlayerStatusRunning {
		return
	}
	a.playerStopping = true
	a.cancelPlayerControls()
	if a.playerStop != nil {
		close(a.playerStop)
		a.playerStop = nil
	}
}

func (a *App) buildPlayerCommand(source playbackSource) (playerCommand, error) {
	var videoURL, token string
	var subtitleURLs []string
	var startPos float64
	if source.url != "" {
		if source.mediaKey != "" || source.video != nil || len(source.subtitles) != 0 {
			return playerCommand{}, fmt.Errorf("web playback cannot include catalog media")
		}
		var err error
		videoURL, err = validatePlaybackURL(source.url)
		if err != nil {
			return playerCommand{}, err
		}
	} else {
		if source.mediaKey == "" || source.video == nil {
			return playerCommand{}, fmt.Errorf("playback source is incomplete")
		}
		var err error
		videoURL, err = a.client.getDownloadURL(source.video)
		if err != nil {
			return playerCommand{}, fmt.Errorf("video URL: %w", err)
		}
		token = a.client.getToken()
		subtitleURLs = a.externalSubtitleURLs(source.subtitles)
		startPos = a.resumePos[source.mediaKey]
	}

	runtimeDir, configPath, sockPath, err := createPlayerRuntime(token)
	if err != nil {
		return playerCommand{}, err
	}
	command := playerCommand{
		mediaKey:   source.mediaKey,
		title:      source.title,
		sockPath:   sockPath,
		runtimeDir: runtimeDir,
		mode:       detectPlayerMode(),
		fromWeb:    source.url != "",
	}
	mpvTitle := source.title
	if command.fromWeb {
		// Let the yt-dlp hook supply its resolved title instead of forcing our placeholder.
		mpvTitle = ""
	}
	command.args = buildMPVArgs(
		resolveMPVPath(a.mpvPath, runtime.GOOS, exec.LookPath, isExecutableFile),
		mpvTitle,
		videoURL,
		subtitleURLs,
		command.mode,
		startPos,
		command.sockPath,
		a.playerFullscreen,
		configPath,
	)
	interfaceArgs, err := a.playerInterfaceArgs(runtimeDir, command.fromWeb)
	if err != nil {
		_ = os.RemoveAll(runtimeDir)
		return playerCommand{}, err
	}
	// Keep the media URL last for wrappers that add platform options before it.
	last := len(command.args) - 1
	command.args = append(append(command.args[:last:last], interfaceArgs...), command.args[last])
	if a.playerPolicy.managedLauncher {
		command.args = append([]string{command.args[0], "--managed-runtime=" + runtimeDir}, command.args[1:]...)
	}
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

func (a *App) startPlayer(command playerCommand) bool {
	if a.playerCleanupDir != "" {
		if !launcherComplete(a.playerCleanupDir) {
			_ = os.RemoveAll(command.runtimeDir)
			a.setPlayerError("previous launcher teardown is unconfirmed; playback is blocked")
			return false
		}
		_ = os.RemoveAll(a.playerCleanupDir)
		a.playerCleanupDir = ""
	}
	if a.playerStatus == PlayerStatusRunning {
		_ = os.RemoveAll(command.runtimeDir)
		return false
	}
	if len(command.args) == 0 {
		_ = os.RemoveAll(command.runtimeDir)
		a.setPlayerError("player command is empty")
		return false
	}

	cmd := exec.Command(command.args[0], command.args[1:]...) //nolint:gosec
	cmd.Env = mpvEnvironment(os.Environ())
	output, err := capturePlayerOutput(a.logger().With("source", "mpv"))
	if err != nil {
		a.logger().Warn("player output is not captured", "error", err)
	} else {
		cmd.Stdout, cmd.Stderr = output, output
	}
	err = cmd.Start()
	if output != nil {
		_ = output.Close() // the child holds its own copy
	}
	if err != nil {
		_ = os.RemoveAll(command.runtimeDir)
		if errors.Is(err, exec.ErrNotFound) {
			a.setPlayerError("mpv not found: install mpv 0.41+ with Lua, or set mpv_path in the config")
			return false
		}
		a.setPlayerError(err.Error())
		return false
	}

	started := time.Now()
	a.logger().Info("player started", "player", command.args[0], "mode", command.mode,
		"title", command.title, "media", command.args[len(command.args)-1])
	done := make(chan playerResult, 1)
	stop := make(chan struct{})
	policy := a.playerPolicy.withDefaults()
	tracker := startIPC(command.sockPath, command.mediaKey, policy.connectTimeout, a.invalidate)
	a.playerTracker = tracker
	a.playerDone = done
	a.playerStop = stop
	a.playerStatus = PlayerStatusRunning
	a.playerTitle = command.title
	a.playerMode = command.mode
	a.playerSocket = command.sockPath
	a.playerSession++
	a.playerFromWeb = command.fromWeb
	a.playerStopping = false
	a.playerError = ""
	if a.invalidate != nil {
		a.invalidate()
	}

	go func() {
		waited := make(chan error, 1)
		go func() { waited <- cmd.Wait() }()
		var err error
		stopped := false
		select {
		case err = <-waited:
		case <-stop:
			stopped = true
			_ = cmd.Process.Signal(syscall.SIGTERM)
			select {
			case err = <-waited:
			case <-time.After(policy.stopTimeout):
				_ = cmd.Process.Kill()
				err = <-waited
			}
		}
		// An ordinary mpv terminated by our Stop request is not a playback failure.
		// Managed launcher failures and forced termination must remain visible.
		var exitErr *exec.ExitError
		if stopped && !policy.managedLauncher && errors.As(err, &exitErr) {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signal() == syscall.SIGTERM {
				err = nil
			}
		}
		tracker.finishIPC()
		result := playerResult{err: err, tracker: tracker}
		if policy.managedLauncher && !launcherComplete(command.runtimeDir) {
			result.retainedRuntime = command.runtimeDir
			result.err = errors.Join(err, errors.New("managed launcher teardown is unconfirmed; private runtime retained"))
		} else {
			_ = os.RemoveAll(command.runtimeDir)
		}
		duration := time.Since(started).Round(time.Second)
		if result.err != nil {
			a.logger().Error("player failed", "error", result.err, "duration", duration)
		} else {
			a.logger().Info("player exited", "duration", duration)
		}
		done <- result
		if a.invalidate != nil {
			a.invalidate()
		}
	}()
	return true
}

func (a *App) applyPlaybackSnapshot(tracker *playbackTracker) {
	if tracker == nil {
		return
	}
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

// macOSMPVFallbacks are tried when the default "mpv" is not on PATH, as for apps
// opened from Finder (PATH=/usr/bin:/bin:/usr/sbin:/sbin): Homebrew on Apple
// Silicon and Intel, MacPorts, then the mpv.app bundle.
var macOSMPVFallbacks = []string{
	"/opt/homebrew/bin/mpv",
	"/usr/local/bin/mpv",
	"/opt/local/bin/mpv",
	"/Applications/mpv.app/Contents/MacOS/mpv",
}

// resolveMPVPath returns the player to execute. Configured paths and other
// platforms are left to exec; only the default name gets macOS fallbacks.
func resolveMPVPath(path, goos string, lookPath func(string) (string, error), isExecutable func(string) bool) string {
	if path != "mpv" || goos != "darwin" {
		return path
	}
	if _, err := lookPath(path); err == nil {
		return path
	}
	for _, candidate := range macOSMPVFallbacks {
		if isExecutable(candidate) {
			return candidate
		}
	}
	return path
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
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

func createPlayerRuntime(token string) (runtimeDir, configPath, sockPath string, err error) {
	if !utf8.ValidString(token) || strings.ContainsAny(token, "\r\n") {
		return "", "", "", fmt.Errorf("authentication token is invalid for mpv configuration")
	}
	for _, char := range token {
		if char < 0x20 || char == 0x7f {
			return "", "", "", fmt.Errorf("authentication token contains a control character")
		}
	}

	runtimeDir, err = os.MkdirTemp("", "pepe-mpv-")
	if err != nil {
		return "", "", "", fmt.Errorf("create private mpv runtime directory: %w", err)
	}
	if err := os.Chmod(runtimeDir, 0o700); err != nil {
		_ = os.RemoveAll(runtimeDir)
		return "", "", "", fmt.Errorf("secure private mpv runtime directory: %w", err)
	}
	configPath = filepath.Join(runtimeDir, "auth.conf")
	file, err := os.OpenFile(configPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		_ = os.RemoveAll(runtimeDir)
		return "", "", "", fmt.Errorf("create private mpv configuration: %w", err)
	}
	content := ""
	if token != "" {
		content = "http-header-fields=" + mpvConfigQuote("X-Auth-Token: "+token) + "\n"
	}
	_, writeErr := file.WriteString(content)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.RemoveAll(runtimeDir)
		if writeErr != nil {
			return "", "", "", fmt.Errorf("write private mpv configuration: %w", writeErr)
		}
		return "", "", "", fmt.Errorf("close private mpv configuration: %w", closeErr)
	}
	return runtimeDir, configPath, filepath.Join(runtimeDir, "mpv.sock"), nil
}

func mpvEnvironment(environment []string) []string {
	filtered := make([]string, 0, len(environment))
	for _, variable := range environment {
		if !strings.HasPrefix(variable, "POLOCHON_TOKEN=") {
			filtered = append(filtered, variable)
		}
	}
	return filtered
}

func mpvConfigQuote(value string) string {
	value = strings.NewReplacer(`\`, `\\`, `"`, `\"`, `,`, `\,`).Replace(value)
	return `"` + value + `"`
}

func buildMPVArgs(mpvPath, title, videoURL string, subtitleURLs []string, mode PlayerMode, startPos float64, sockPath string, fullscreen bool, configPath string) []string {
	if strings.TrimSpace(mpvPath) == "" {
		mpvPath = "mpv"
	}
	args := []string{mpvPath, "--include=" + configPath, "--msg-level=all=warn"}
	if startPos > minimumResumePosition {
		args = append(args, fmt.Sprintf("--start=%d", int(startPos)))
	}
	if mode == PlayerModeKMS {
		args = append(args, "--vo=gpu", "--gpu-context=drm", "--opengl-es=yes", "--fs", "--no-border", "--ontop")
	} else if fullscreen {
		args = append(args, "--fs", "--no-border")
	}
	if title != "" {
		// Make the playback title available to scripts and IPC, not just the window.
		args = append(args, "--title="+title, "--force-media-title="+title)
	}
	for _, subtitleURL := range subtitleURLs {
		if subtitleURL != "" {
			args = append(args, "--sub-file="+subtitleURL)
		}
	}
	args = append(args, "--input-ipc-server="+sockPath, videoURL)
	return args
}

// playbackTracker stores IPC state. Only title changes wake the UI.
type playbackTracker struct {
	mu       sync.RWMutex
	mediaKey string
	pos      float64
	eof      bool
	title    string
	onTitle  func()
	done     chan struct{}
	stop     chan struct{}
	stopOnce sync.Once
}

func newPlaybackTracker(mediaKey string) *playbackTracker {
	return &playbackTracker{
		mediaKey: mediaKey,
		done:     make(chan struct{}),
		stop:     make(chan struct{}),
	}
}

func (t *playbackTracker) stopIPC() {
	t.stopOnce.Do(func() { close(t.stop) })
}

// finishIPC lets final events drain after process exit before forcing shutdown.
// Only the player worker waits here; results publish a settled tracker.
func (t *playbackTracker) finishIPC() {
	t.wait(playbackTrackerWait)
	t.stopIPC()
	<-t.done
}

func (t *playbackTracker) updatePos(pos float64) {
	t.mu.Lock()
	t.pos = pos
	t.mu.Unlock()
}

func (t *playbackTracker) updateTitle(title string) {
	title = strings.TrimSpace(title)
	t.mu.Lock()
	changed := t.title != title
	t.title = title
	t.mu.Unlock()
	if changed && t.onTitle != nil {
		t.onTitle()
	}
}

func (t *playbackTracker) resolvedTitle() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.title
}

func (a *App) updatePlayerTitle() {
	if a.playerStatus != PlayerStatusRunning || !a.playerFromWeb || a.playerTracker == nil || a.playerStopping {
		return
	}
	a.playerTitle = a.playerTracker.resolvedTitle()
	if a.playerTitle == "" {
		a.playerTitle = webPlaybackTitle
	}
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

// startIPC launches ipcLoop in a goroutine and returns a playback tracker.
func startIPC(sockPath, mediaKey string, connectTimeout time.Duration, onTitle func()) *playbackTracker {
	tracker := newPlaybackTracker(mediaKey)
	tracker.onTitle = onTitle
	go ipcLoop(sockPath, tracker, connectTimeout)
	return tracker
}

// mpvEvent is the JSON event structure sent by mpv over IPC.
type mpvEvent struct {
	Event  string `json:"event"`
	Name   string `json:"name"`
	Data   any    `json:"data"`
	Reason string `json:"reason"`
}

// ipcLoop connects to the mpv IPC socket and processes events.
func ipcLoop(sockPath string, tracker *playbackTracker, connectTimeout time.Duration) {
	defer close(tracker.done)

	deadline := time.Now().Add(connectTimeout)
	dialer := net.Dialer{Deadline: deadline, Timeout: 100 * time.Millisecond}
	var conn net.Conn
	for time.Now().Before(deadline) {
		select {
		case <-tracker.stop:
			return
		default:
		}
		c, err := dialer.Dial("unix", sockPath)
		if err == nil {
			conn = c
			break
		}
		timer := time.NewTimer(min(100*time.Millisecond, time.Until(deadline)))
		select {
		case <-tracker.stop:
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
	if conn == nil {
		return
	}
	defer func() { _ = conn.Close() }()
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-tracker.stop:
			_ = conn.Close()
		case <-stopped:
		}
	}()

	// Send observe_property commands.
	_, _ = fmt.Fprintf(conn, "{\"command\": [\"observe_property\", 1, \"time-pos\"]}\n")
	_, _ = fmt.Fprintf(conn, "{\"command\": [\"observe_property\", 2, \"eof-reached\"]}\n")
	// yt-dlp sets this file-local option, which supplies media-title. Observe
	// it directly to avoid exposing media-title's URL/filename fallback.
	_, _ = fmt.Fprintf(conn, "{\"command\": [\"observe_property\", 3, \"force-media-title\"]}\n")

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Bytes()
		var ev mpvEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.Event == "end-file" && ev.Reason == "eof" {
			tracker.markEOF()
			return
		}
		if ev.Event != "property-change" {
			continue
		}
		switch ev.Name {
		case "force-media-title":
			if title, ok := ev.Data.(string); ok {
				tracker.updateTitle(title)
			} else if ev.Data == nil {
				tracker.updateTitle("")
			}
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

// capturePlayerOutput logs each line mpv or its wrapper writes. The caller
// passes the returned pipe to the child and closes its own copy. A separate
// pipe, unlike an io.Writer, keeps cmd.Wait from also waiting for background
// children that inherit it.
func capturePlayerOutput(logger *slog.Logger) (*os.File, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	go func() {
		defer func() { _ = reader.Close() }()
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 4096), 64<<10)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			level := slog.LevelWarn // mpv runs with --msg-level=all=warn
			if strings.Contains(strings.ToLower(line), "error") {
				level = slog.LevelError
			}
			logger.Log(context.Background(), level, line)
		}
		// Keep draining after an overlong line so the player never blocks.
		_, _ = io.Copy(io.Discard, reader)
	}()
	return writer, nil
}
