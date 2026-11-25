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
	scripted   bool // pepe.lua handles remote actions
	// before and after are queued around the launched file over IPC.
	before, after []playlistEntry
}

// playlistEntry is a queued episode. Command-line options apply to every
// playlist entry, so each entry sets its own start, title and subtitles.
type playlistEntry struct {
	mediaKey     string
	title        string
	url          string
	subtitleURLs []string
	start        float64
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
	// before and after are the show's other episodes, in order.
	before, after []playbackSource
}

func movieMediaKey(id string) string {
	return "movie:" + id
}

func episodeMediaKey(showID string, season, episode int) string {
	return fmt.Sprintf("episode:%s:%d:%d", showID, season, episode)
}

func (a *App) launchMovie(media *MediaItem) {
	if media == nil {
		a.setPlayerError("", errors.New("movie is not available"))
		return
	}
	movie, ok := a.client.getMovie(media.ID)
	if !ok {
		a.setPlayerError(movieMediaKey(media.ID), errors.New("movie is not available"))
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
		a.setPlayerError("", errors.New("episode is not available"))
		return
	}
	show, _ := a.client.getShow(node.ShowID)
	source := episodeSource(show, node)
	// Queue the whole show around the episode so playback continues and
	// mpv's previous/next reach every episode.
	if show != nil {
		var queue []*DynamicNode
		for _, season := range createSeasonNodes(show) {
			queue = append(queue, season.Children...)
		}
		for i, other := range queue {
			if other.Episode != nil && other.Episode.Season == node.Episode.Season && other.Episode.Episode == node.Episode.Episode {
				for _, earlier := range queue[:i] {
					source.before = append(source.before, episodeSource(show, earlier))
				}
				for _, later := range queue[i+1:] {
					source.after = append(source.after, episodeSource(show, later))
				}
				break
			}
		}
	}
	a.launchPlayback(source)
}

func episodeSource(show *papi.Show, node *DynamicNode) playbackSource {
	title := node.Label
	if show != nil && show.Title != "" {
		title = show.Title + " " + title
	}
	ep := node.Episode
	if ep == nil {
		return playbackSource{title: title}
	}
	return playbackSource{
		mediaKey:  episodeMediaKey(node.ShowID, ep.Season, ep.Episode),
		title:     title,
		video:     ep,
		subtitles: ep.Subtitles,
	}
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
	if source.mediaKey != "" && source.mediaKey == a.playingMediaKey() {
		return // already playing: restarting would only reload it
	}
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
		a.setPlayerError(source.mediaKey, err)
		return
	}
	a.startPlayer(command)
}

// playingMediaKey is the media key of the entry mpv is playing, or "" when
// nothing is playing, playback is being stopped, or the entry is not
// catalog media.
func (a *App) playingMediaKey() string {
	if a.playerStatus != PlayerStatusRunning || a.playerStopping || a.pendingPlayback != nil || a.playerTracker == nil {
		return ""
	}
	key, _, _ := a.playerTracker.snapshot()
	return key
}

func (a *App) setPlayerError(mediaKey string, err error) {
	if a.playerStatus == PlayerStatusRunning {
		return
	}
	a.logger().Error("playback failed", "error", err)
	a.recordPlayerError(mediaKey, err)
}

// recordPlayerError keeps the raw error for the log and web status, and a
// short reason shown only on mediaKey's detail page.
func (a *App) recordPlayerError(mediaKey string, err error) {
	a.playerStatus = PlayerStatusError
	a.playerError = err.Error()
	a.playerErrorKey = mediaKey
	a.playerErrorMsg = playbackErrorReason(err)
}

func (a *App) clearPlayerError() {
	a.playerError, a.playerErrorKey, a.playerErrorMsg = "", "", ""
}

// playerErrorFor is the failure reason to show for mediaKey, if its last
// playback attempt failed.
func (a *App) playerErrorFor(mediaKey string) string {
	if a.playerStatus != PlayerStatusError || mediaKey == "" || mediaKey != a.playerErrorKey {
		return ""
	}
	return a.playerErrorMsg
}

var (
	errMPVNotFound        = errors.New("mpv not found: install mpv 0.41+ with Lua, or set mpv_path in the config")
	errVideoURL           = errors.New("video URL")
	errLauncherTeardown   = errors.New("managed launcher teardown is unconfirmed; private runtime retained")
	errLauncherBlocked    = errors.New("previous launcher teardown is unconfirmed; playback is blocked")
	errPlayerCommandEmpty = errors.New("player command is empty")
)

// playbackErrorReason turns a playback error into a short status line. Raw
// errors can carry private URLs and paths, so they stay in the log.
func playbackErrorReason(err error) string {
	var exitErr *exec.ExitError
	switch {
	case errors.Is(err, errMPVNotFound):
		return "mpv isn't installed"
	case errors.Is(err, errLauncherTeardown), errors.Is(err, errLauncherBlocked):
		return "The previous player didn't close · restart pépé"
	case errors.Is(err, errVideoURL):
		return "This file isn't available"
	case errors.As(err, &exitErr) && exitErr.ExitCode() == 2:
		// mpv's exit status when no file could be played.
		return "mpv couldn't open this file"
	}
	return "Playback failed"
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
		if source.mediaKey != "" || source.video != nil || len(source.subtitles) != 0 || len(source.before) != 0 || len(source.after) != 0 {
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
			return playerCommand{}, fmt.Errorf("%w: %w", errVideoURL, err)
		}
		token = a.client.getToken()
		subtitleURLs = a.externalSubtitleURLs(source.subtitles)
		startPos = a.progress.position(source.mediaKey)
	}
	before, after := a.playlistEntries(source.before), a.playlistEntries(source.after)

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
		before:     before,
		after:      after,
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
	command.scripted = len(interfaceArgs) > 0
	// Keep the media URL last for wrappers that add platform options before it.
	last := len(command.args) - 1
	command.args = append(append(command.args[:last:last], interfaceArgs...), command.args[last])
	if a.playerPolicy.managedLauncher {
		command.args = append([]string{command.args[0], "--managed-runtime=" + runtimeDir}, command.args[1:]...)
	}
	return command, nil
}

// playlistEntries resolves queued episodes; one without a video URL is left out.
func (a *App) playlistEntries(sources []playbackSource) []playlistEntry {
	entries := make([]playlistEntry, 0, len(sources))
	for _, source := range sources {
		if source.mediaKey == "" || source.video == nil {
			continue
		}
		videoURL, err := a.client.getDownloadURL(source.video)
		if err != nil {
			a.logger().Warn("episode left out of the playlist", "title", source.title, "error", err)
			continue
		}
		entries = append(entries, playlistEntry{
			mediaKey:     source.mediaKey,
			title:        source.title,
			url:          videoURL,
			subtitleURLs: a.externalSubtitleURLs(source.subtitles),
			start:        a.progress.position(source.mediaKey),
		})
	}
	return entries
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
			a.setPlayerError(command.mediaKey, errLauncherBlocked)
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
		a.setPlayerError(command.mediaKey, errPlayerCommandEmpty)
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
			a.setPlayerError(command.mediaKey, errMPVNotFound)
			return false
		}
		a.setPlayerError(command.mediaKey, err)
		return false
	}

	started := time.Now()
	a.logger().Info("player started", "player", command.args[0], "mode", command.mode, "script", command.scripted,
		"title", command.title, "media", command.args[len(command.args)-1], "playlist", 1+len(command.before)+len(command.after))
	done := make(chan playerResult, 1)
	stop := make(chan struct{})
	policy := a.playerPolicy.withDefaults()
	tracker := a.commandTracker(command)
	startIPC(command.sockPath, tracker, policy.connectTimeout)
	a.playerTracker = tracker
	a.playerDone = done
	a.playerStop = stop
	a.playerStatus = PlayerStatusRunning
	a.playerTitle = command.title
	a.playerSocket = command.sockPath
	a.playerSession++
	a.playerFromWeb = command.fromWeb
	a.playerScripted = command.scripted
	a.playerStopping = false
	a.clearPlayerError()
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
		// An ordinary mpv terminated by our Stop request is not a playback failure:
		// mpv handles SIGTERM and exits with status 4 ("quit due to a signal").
		// Managed launcher failures and forced termination must remain visible.
		var exitErr *exec.ExitError
		if stopped && !policy.managedLauncher && errors.As(err, &exitErr) {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && (status.Signal() == syscall.SIGTERM || status.Exited() && status.ExitStatus() == 4) {
				err = nil
			}
		}
		tracker.finishIPC()
		result := playerResult{err: err, tracker: tracker}
		if policy.managedLauncher && !launcherComplete(command.runtimeDir) {
			result.retainedRuntime = command.runtimeDir
			result.err = errors.Join(err, errLauncherTeardown)
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

// applyPlaybackRecords stores entries that ended during playback, so a long
// playlist does not lose them if the player is later killed.
func (a *App) applyPlaybackRecords(tracker *playbackTracker) {
	if tracker == nil {
		return
	}
	for _, record := range tracker.takeRecords() {
		a.progress.recordPlayback(record.mediaKey, record.pos, record.duration, record.eof)
	}
}

// applyPlaybackSnapshot stores the remaining progress once the player exited.
func (a *App) applyPlaybackSnapshot(tracker *playbackTracker) {
	if tracker == nil {
		return
	}
	a.applyPlaybackRecords(tracker)
	if record, ok := tracker.unfinished(); ok {
		a.progress.recordPlayback(record.mediaKey, record.pos, record.duration, record.eof)
	}
}

// commandTracker tracks command's playback, including its queued episodes.
func (a *App) commandTracker(command playerCommand) *playbackTracker {
	tracker := newPlaybackTracker(command.mediaKey, a.invalidate)
	tracker.before, tracker.after = command.before, command.after
	tracker.logger = a.logger()
	return tracker
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

// playbackRecord is where playback of one playlist entry stopped.
type playbackRecord struct {
	mediaKey string
	pos      float64
	duration float64
	eof      bool
}

// playbackTracker stores IPC state for the current playlist entry. Title
// changes and ended entries wake the UI.
type playbackTracker struct {
	mu       sync.RWMutex
	mediaKey string
	entryID  int64   // mpv playlist entry ID; 0 until an event names it
	pos      float64 // resume position: time-pos past minimumResumePosition
	clock    float64 // latest time-pos, for the web remote's progress bar
	paused   bool
	duration float64
	eof      bool
	ended    bool             // the current entry's record is in records
	records  []playbackRecord // ended entries the event loop has not applied
	keys     map[int64]string // playlist entry ID → media key
	title    string
	// playlistPos (1-based) and playlistCount follow mpv's playlist.
	playlistPos, playlistCount int
	notify                     func()
	logger                     *slog.Logger
	// before and after are queued around the launched file once IPC connects.
	before, after []playlistEntry
	done          chan struct{}
	stop          chan struct{}
	stopOnce      sync.Once
}

func newPlaybackTracker(mediaKey string, notify func()) *playbackTracker {
	return &playbackTracker{
		mediaKey: mediaKey,
		keys:     map[int64]string{},
		notify:   notify,
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

func (t *playbackTracker) updateClock(pos float64) {
	t.mu.Lock()
	t.clock = pos
	t.mu.Unlock()
}

func (t *playbackTracker) updatePaused(paused bool) {
	t.mu.Lock()
	t.paused = paused
	t.mu.Unlock()
}

// progress is the playing entry's latest position, its duration (0 when
// unknown) and whether it is paused.
func (t *playbackTracker) progress() (pos, duration float64, paused bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.clock, t.duration, t.paused
}

func (t *playbackTracker) updateDuration(duration float64) {
	t.mu.Lock()
	t.duration = duration
	t.mu.Unlock()
}

func (t *playbackTracker) updateTitle(title string) {
	title = strings.TrimSpace(title)
	t.mu.Lock()
	changed := t.title != title
	t.title = title
	t.mu.Unlock()
	if changed && t.notify != nil {
		t.notify()
	}
}

func (t *playbackTracker) resolvedTitle() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.title
}

// updatePlaylist stores mpv's playlist-pos (0-based) or playlist-count.
func (t *playbackTracker) updatePlaylist(name string, value int) {
	t.mu.Lock()
	old := [2]int{t.playlistPos, t.playlistCount}
	if name == "playlist-pos" {
		t.playlistPos = value + 1
	} else {
		t.playlistCount = value
	}
	changed := old != [2]int{t.playlistPos, t.playlistCount}
	t.mu.Unlock()
	if changed && t.notify != nil {
		t.notify()
	}
}

// playlist is the playing entry's 1-based position and the entry count, or
// zeros until mpv reports them.
func (t *playbackTracker) playlist() (pos, count int) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.playlistPos, t.playlistCount
}

// updatePlayerTitle follows mpv's title for the playing entry: the
// force-media-title pépé sets on the launched and each queued file, or the
// yt-dlp title for web playback.
func (a *App) updatePlayerTitle() {
	if a.playerStatus != PlayerStatusRunning || a.playerTracker == nil || a.playerStopping {
		return
	}
	title := a.playerTracker.resolvedTitle()
	if !a.playerFromWeb {
		if title != "" {
			a.playerTitle = title
		}
		return
	}
	a.playerTitle = title
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

// bindEntry maps a queued entry's playlist ID, from mpv's loadfile reply.
func (t *playbackTracker) bindEntry(id int64, mediaKey string) {
	t.mu.Lock()
	t.keys[id] = mediaKey
	t.mu.Unlock()
}

// claimEntry names the launched entry: the first playlist ID seen that no
// loadfile reply claimed. The caller holds t.mu.
func (t *playbackTracker) claimEntry(id int64) {
	if t.entryID != 0 || id == 0 {
		return
	}
	if _, queued := t.keys[id]; !queued {
		t.entryID = id
		t.keys[id] = t.mediaKey
	}
}

// startEntry follows mpv's start-file event.
func (t *playbackTracker) startEntry(id int64) {
	t.mu.Lock()
	t.claimEntry(id)
	if id == t.entryID && !t.ended {
		t.mu.Unlock()
		return // the launched file, already current
	}
	// Unknown entries (added by the user) are tracked without a key.
	t.mediaKey, t.entryID = t.keys[id], id
	// The duration is kept: mpv sends no change when the next file has the
	// same length, and a different length arrives as soon as it loads.
	t.pos, t.clock, t.eof, t.ended = 0, 0, false, false
	t.mu.Unlock()
	if t.notify != nil {
		t.notify() // the player title follows the entry
	}
}

// endEntry records where the current entry stopped, for its end-file event.
func (t *playbackTracker) endEntry(id int64, eof bool) {
	t.mu.Lock()
	t.claimEntry(id)
	if t.ended || id != 0 && id != t.entryID {
		t.mu.Unlock()
		return
	}
	t.eof = t.eof || eof
	t.ended = true
	t.records = append(t.records, playbackRecord{mediaKey: t.mediaKey, pos: t.pos, duration: t.duration, eof: t.eof})
	t.mu.Unlock()
	if t.notify != nil {
		t.notify()
	}
}

// takeRecords returns and forgets the entries that ended.
func (t *playbackTracker) takeRecords() []playbackRecord {
	t.mu.Lock()
	defer t.mu.Unlock()
	records := t.records
	t.records = nil
	return records
}

// unfinished is the current entry when no end-file recorded it, as when mpv
// is killed.
func (t *playbackTracker) unfinished() (playbackRecord, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return playbackRecord{mediaKey: t.mediaKey, pos: t.pos, duration: t.duration, eof: t.eof}, !t.ended
}

func (t *playbackTracker) wait(timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-t.done:
	case <-timer.C:
	}
}

// startIPC launches ipcLoop in a goroutine.
func startIPC(sockPath string, tracker *playbackTracker, connectTimeout time.Duration) {
	go ipcLoop(sockPath, tracker, connectTimeout)
}

// mpvEvent is the JSON event or reply structure sent by mpv over IPC.
type mpvEvent struct {
	Event           string `json:"event"`
	Name            string `json:"name"`
	Data            any    `json:"data"`
	Reason          string `json:"reason"`
	PlaylistEntryID int64  `json:"playlist_entry_id"`
	RequestID       int64  `json:"request_id"`
	Error           string `json:"error"`
}

// mpvPathList escapes a -set value of an mpv path list option such as
// sub-files, whose separator is ':' on Unix.
func mpvPathList(paths []string) string {
	escaped := make([]string, len(paths))
	escaper := strings.NewReplacer(`\`, `\\`, `:`, `\:`)
	for i, path := range paths {
		escaped[i] = escaper.Replace(path)
	}
	return strings.Join(escaped, ":")
}

// loadfileCommand queues an entry with file-local options, which mpv
// restores when the entry ends (mpv 0.38+ argument order).
func loadfileCommand(entry playlistEntry, flags string, index int, requestID int64) ([]byte, error) {
	start := "none"
	if entry.start > minimumResumePosition {
		start = fmt.Sprintf("%d", int(entry.start))
	}
	return json.Marshal(map[string]any{
		"command": map[string]any{
			"name":  "loadfile",
			"url":   entry.url,
			"flags": flags,
			"index": index,
			"options": map[string]string{
				"start":             start,
				"title":             entry.title,
				"force-media-title": entry.title,
				"sub-files":         mpvPathList(entry.subtitleURLs),
			},
		},
		"request_id": requestID,
	})
}

// queuePlaylist inserts the earlier entries before the launched file and
// appends the later ones. It returns the pending request IDs.
func (t *playbackTracker) queuePlaylist(conn net.Conn) map[int64]string {
	pending := map[int64]string{}
	var requestID int64
	send := func(entry playlistEntry, flags string, index int) {
		requestID++
		line, err := loadfileCommand(entry, flags, index, requestID)
		if err != nil {
			return
		}
		pending[requestID] = entry.mediaKey
		_, _ = conn.Write(append(line, '\n'))
	}
	for i, entry := range t.before {
		send(entry, "insert-at", i)
	}
	for _, entry := range t.after {
		send(entry, "append", -1)
	}
	return pending
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
	// Observe the title sources directly to avoid exposing media-title's
	// URL/filename fallback. Current yt-dlp hooks set the stream metadata;
	// older ones (and user configs) set force-media-title, which wins.
	_, _ = fmt.Fprintf(conn, "{\"command\": [\"observe_property\", 3, \"force-media-title\"]}\n")
	_, _ = fmt.Fprintf(conn, "{\"command\": [\"observe_property\", 4, \"metadata/by-key/title\"]}\n")
	_, _ = fmt.Fprintf(conn, "{\"command\": [\"observe_property\", 5, \"duration\"]}\n")
	_, _ = fmt.Fprintf(conn, "{\"command\": [\"observe_property\", 6, \"playlist-pos\"]}\n")
	_, _ = fmt.Fprintf(conn, "{\"command\": [\"observe_property\", 7, \"playlist-count\"]}\n")
	_, _ = fmt.Fprintf(conn, "{\"command\": [\"observe_property\", 8, \"pause\"]}\n")
	pending := tracker.queuePlaylist(conn)
	titles := map[string]string{}

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Bytes()
		var ev mpvEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		switch ev.Event {
		case "":
			mediaKey, ok := pending[ev.RequestID]
			if !ok {
				continue
			}
			delete(pending, ev.RequestID)
			data, _ := ev.Data.(map[string]any)
			id, _ := data["playlist_entry_id"].(float64)
			if ev.Error != "success" || id <= 0 {
				if tracker.logger != nil {
					tracker.logger.Warn("mpv did not queue an episode", "media", mediaKey, "error", ev.Error)
				}
				continue
			}
			tracker.bindEntry(int64(id), mediaKey)
			continue
		case "start-file":
			tracker.startEntry(ev.PlaylistEntryID)
			continue
		case "end-file":
			// A redirect replaces the entry with its expansion (playlists).
			if ev.Reason != "redirect" {
				tracker.endEntry(ev.PlaylistEntryID, ev.Reason == "eof")
			}
			continue
		case "property-change":
		default:
			continue
		}
		switch ev.Name {
		case "force-media-title", "metadata/by-key/title":
			if title, ok := ev.Data.(string); ok {
				titles[ev.Name] = strings.TrimSpace(title)
			} else if ev.Data == nil {
				titles[ev.Name] = ""
			} else {
				continue
			}
			title := titles["force-media-title"]
			if title == "" {
				title = titles["metadata/by-key/title"]
			}
			tracker.updateTitle(title)
		case "time-pos":
			if pos, ok := ev.Data.(float64); ok && pos >= 0 {
				tracker.updateClock(pos)
				if pos > minimumResumePosition {
					tracker.updatePos(pos)
				}
			}
		case "pause":
			if paused, ok := ev.Data.(bool); ok {
				tracker.updatePaused(paused)
			}
		case "duration":
			if duration, ok := ev.Data.(float64); ok && duration > 0 {
				tracker.updateDuration(duration)
			}
		case "playlist-pos", "playlist-count":
			if value, ok := ev.Data.(float64); ok {
				tracker.updatePlaylist(ev.Name, int(value))
			}
		case "eof-reached":
			// With keep-open, mpv stays on the ended last entry.
			// A late true after start-file must not mark the next entry:
			// its position is 0 until time-pos passes minimumResumePosition.
			if reached, ok := ev.Data.(bool); ok && reached {
				if _, pos, _ := tracker.snapshot(); pos > 0 {
					tracker.markEOF()
				}
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
