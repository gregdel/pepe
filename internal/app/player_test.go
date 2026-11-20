package app

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gioui.org/op/paint"
	polochon "github.com/odwrtw/polochon/lib"
	"github.com/odwrtw/polochon/lib/papi"
)

func TestCatalogPlaybackURLFailures(t *testing.T) {
	for _, kind := range []string{"movie", "episode"} {
		for _, failure := range []string{"video", "subtitle"} {
			t.Run(kind+"/"+failure, func(t *testing.T) {
				client, err := NewAPIClient("https://catalog.example", "fixture-token", nil)
				if err != nil {
					t.Fatal(err)
				}
				a := New(client, nil, &Config{MPVPath: filepath.Join(t.TempDir(), "must-not-launch")}, nil, nil)
				defer a.Close()
				movie := testMovie("movie-one", "Fixture Movie", 2026)
				episode := headlessEpisode(1, 2, "Fixture Episode")
				episode.ShowImdbID = "show-one"
				source := playbackSource{mediaKey: movieMediaKey(movie.ImdbID), title: "Fixture Movie", video: movie}
				if kind == "episode" {
					source = playbackSource{mediaKey: episodeMediaKey(episode.ShowImdbID, 1, 2), title: "Fixture Episode", video: episode}
				}
				if failure == "video" {
					wantErr := papi.ErrMissingMovieID
					movie.ImdbID = ""
					if kind == "episode" {
						episode.ShowImdbID = ""
						wantErr = papi.ErrMissingShowEpisodeInformations
					}
					command, err := a.buildPlayerCommand(source)
					if !errors.Is(err, wantErr) || !strings.HasPrefix(err.Error(), "video URL: ") ||
						len(command.args) != 0 || command.runtimeDir != "" {
						t.Fatalf("invalid video command = %+v, error = %v", command, err)
					}
					a.launchPlayback(source)
					if a.playerStatus != PlayerStatusError || !strings.HasPrefix(a.playerError, "video URL: ") ||
						a.playerDone != nil || a.playerTracker != nil || a.playerStop != nil {
						t.Fatalf("invalid video launch state = %s: %s", a.playerStatus, a.playerError)
					}
					return
				}

				bad := &papi.Subtitle{Subtitle: &polochon.Subtitle{Lang: polochon.EN}}
				if _, err := client.getDownloadURL(bad); !errors.Is(err, papi.ErrMissingSubtitleVideo) {
					t.Fatalf("fixture subtitle URL error = %v", err)
				}
				good := &papi.Subtitle{Subtitle: &polochon.Subtitle{
					Video: source.video.(polochon.Video), Lang: polochon.EN,
				}}
				source.subtitles = []*papi.Subtitle{
					nil, {Subtitle: &polochon.Subtitle{Embedded: true}}, bad, good, bad,
				}
				videoURL, err := client.getDownloadURL(source.video)
				if err != nil {
					t.Fatal(err)
				}
				subtitleURL, err := client.getDownloadURL(good)
				if err != nil {
					t.Fatal(err)
				}
				a.resumePos[source.mediaKey] = 47
				command, err := a.buildPlayerCommand(source)
				if err != nil {
					t.Fatalf("subtitle URL failure prevented playback command: %v", err)
				}
				defer func() { _ = os.RemoveAll(command.runtimeDir) }()
				var subtitleArgs []string
				for _, arg := range command.args {
					if strings.HasPrefix(arg, "--sub-file=") {
						subtitleArgs = append(subtitleArgs, arg)
					}
				}
				if command.mediaKey != source.mediaKey || command.args[len(command.args)-1] != videoURL ||
					len(subtitleArgs) != 1 || subtitleArgs[0] != "--sub-file="+subtitleURL ||
					!strings.Contains(strings.Join(command.args, "\n"), "--start=47\n") {
					t.Fatalf("command after subtitle URL failure = %+v", command)
				}
			})
		}
	}
}

func TestPlayerRuntimeKeepsTokenOutOfArgumentsAndCleansUp(t *testing.T) {
	dir := t.TempDir()
	argvPath := filepath.Join(dir, "argv")
	configCopy := filepath.Join(dir, "config-copy")
	envCopy := filepath.Join(dir, "env-copy")
	scriptPath := filepath.Join(dir, "fake-mpv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$PEPE_TEST_ARGV\"\nenv > \"$PEPE_TEST_ENV\"\nfor arg do\n  case \"$arg\" in\n    --include=*) cat \"${arg#--include=}\" > \"$PEPE_TEST_CONFIG\" ;;\n  esac\ndone\nsleep 0.15\nexit \"$PEPE_TEST_EXIT\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PEPE_TEST_ARGV", argvPath)
	t.Setenv("PEPE_TEST_CONFIG", configCopy)
	t.Setenv("PEPE_TEST_ENV", envCopy)
	t.Setenv("PEPE_TEST_EXIT", "0")

	secret := `tok"en\here`
	t.Setenv("POLOCHON_TOKEN", secret)
	runtimeDir, configPath, sockPath, err := createPlayerRuntime(secret)
	if err != nil {
		t.Fatalf("createPlayerRuntime: %v", err)
	}
	defer func() { _ = os.RemoveAll(runtimeDir) }()
	if info, err := os.Stat(runtimeDir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("runtime directory mode = %v, err=%v; want 0700", info, err)
	}
	if info, err := os.Stat(configPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config file mode = %v, err=%v; want 0600", info, err)
	}
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	wantConfig := `http-header-fields="X-Auth-Token: tok\"en\\here"` + "\n"
	if string(config) != wantConfig {
		t.Fatalf("mpv config = %q, want %q", config, wantConfig)
	}
	args := buildMPVArgs(scriptPath, "Movie", "https://video", []string{"https://subtitle"}, PlayerModeGUI, 0, sockPath, false, configPath)
	if strings.Contains(strings.Join(args, "\x00"), secret) {
		t.Fatal("mpv arguments contain the authentication token")
	}

	invalidated := make(chan struct{}, 2)
	a := &App{playerStatus: PlayerStatusIdle, resumePos: make(map[string]float64), images: newAsyncCache[paint.ImageOp](1, 0), invalidate: func() { invalidated <- struct{}{} }}
	if !a.startPlayer(playerCommand{mediaKey: "movie:tt123", sockPath: sockPath, runtimeDir: runtimeDir, args: args, mode: PlayerModeGUI}) {
		t.Fatalf("startPlayer failed: %s", a.playerError)
	}
	done := a.playerDone
	if len(invalidated) != 1 {
		t.Fatal("starting playback did not request a new frame")
	}
	if a.playerStatus != PlayerStatusRunning {
		t.Fatalf("player status = %q, want Running", a.playerStatus)
	}
	waitForCache(t, func() bool { return len(done) == 1 })
	argv, err := os.ReadFile(argvPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(argv), secret) || !strings.Contains(string(argv), "--include="+configPath) {
		t.Fatalf("fake mpv argv is not redacted/include based: %q", argv)
	}
	copiedConfig, err := os.ReadFile(configCopy)
	if err != nil || string(copiedConfig) != wantConfig {
		t.Fatalf("mpv config copy = %q, err=%v", copiedConfig, err)
	}
	childEnv, err := os.ReadFile(envCopy)
	if err != nil || strings.Contains(string(childEnv), "POLOCHON_TOKEN=") || strings.Contains(string(childEnv), secret) {
		t.Fatalf("mpv environment leaked token: %q, err=%v", childEnv, err)
	}
	if _, err := os.Stat(runtimeDir); !os.IsNotExist(err) {
		t.Fatalf("per-launch runtime directory remains after mpv exit: %v", err)
	}
	a.drainChannels()
	if a.playerStatus != PlayerStatusIdle || a.playerDone != nil {
		t.Fatalf("successful completion state = (%q, %v)", a.playerStatus, a.playerDone)
	}
	select {
	case <-done:
		t.Fatal("launch produced more than one completion")
	default:
	}
}

func TestSwitchPlaybackStopsBeforeStartingNextPlayer(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "launches")
	playerPath := filepath.Join(dir, "fake-mpv")
	script := `#!/bin/sh
case " $* " in
  *--title=Second*) printf 'second\n' >> "$PEPE_TEST_LOG"; exit 0 ;;
esac
trap 'printf "stopped\n" >> "$PEPE_TEST_LOG"; exit 0' TERM
printf 'first\n' >> "$PEPE_TEST_LOG"
while :; do sleep 0.1; done
`
	if err := os.WriteFile(playerPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PEPE_TEST_LOG", logPath)
	client, err := NewAPIClient("http://localhost", "token", nil)
	if err != nil {
		t.Fatal(err)
	}
	a := New(client, nil, &Config{MPVPath: playerPath}, nil, nil)
	first := testMovie("first", "First", 2020)
	second := testMovie("second", "Second", 2021)
	a.launchPlayback(playbackSource{mediaKey: movieMediaKey("first"), title: "First", video: first})
	if a.playerStatus != PlayerStatusRunning {
		t.Fatalf("first launch: %s", a.playerError)
	}
	t.Cleanup(func() {
		if a.playerStop != nil {
			close(a.playerStop)
		}
	})
	waitForCache(t, func() bool {
		data, _ := os.ReadFile(logPath)
		return strings.Contains(string(data), "first\n")
	})
	firstDone := a.playerDone
	a.launchPlayback(playbackSource{mediaKey: movieMediaKey("second"), title: "Second", video: second})
	if a.playerStatus != PlayerStatusRunning || a.playerDone != firstDone || a.pendingPlayback == nil {
		t.Fatal("switch launched a second process before the first exited")
	}
	waitForCache(t, func() bool { return len(firstDone) == 1 })
	data, err := os.ReadFile(logPath)
	if err != nil || string(data) != "first\nstopped\n" {
		t.Fatalf("player order before completion = %q, %v", data, err)
	}
	a.drainChannels()
	if a.playerStatus != PlayerStatusRunning || a.playerTitle != "Second" || a.pendingPlayback != nil {
		t.Fatalf("replacement state = (%q, %q, %v), error %s", a.playerStatus, a.playerTitle, a.pendingPlayback, a.playerError)
	}
	waitForCache(t, func() bool { return len(a.playerDone) == 1 })
	a.drainChannels()
	data, err = os.ReadFile(logPath)
	if err != nil || string(data) != "first\nstopped\nsecond\n" || a.playerStatus != PlayerStatusIdle {
		t.Fatalf("switch result = (%q, %q), %v", data, a.playerStatus, err)
	}
}

func TestMoviePlaybackTitleUsesMetadata(t *testing.T) {
	client := NewEmptyAPIClient(nil)
	for _, tc := range []struct {
		year int
		want string
	}{
		{2024, "Fixture Movie (2024)"},
		{0, "Fixture Movie"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			client.movies["movie-one"] = testMovie("movie-one", "Fixture Movie", tc.year)
			a := &App{client: client, playerStatus: PlayerStatusRunning}
			a.launchMovie(&MediaItem{ID: "movie-one", Title: "Stale listing title"})
			if a.pendingPlayback == nil || a.pendingPlayback.title != tc.want {
				t.Fatalf("movie playback title = %+v, want %q", a.pendingPlayback, tc.want)
			}
		})
	}
}

func TestPlaybackTitleIsPassedLiterallyToMPV(t *testing.T) {
	for _, title := range []string{"", "Movie (2024)", "Show S02E05 - Episode", "${filename} {\\b1} · Title"} {
		args := buildMPVArgs("mpv", title, "https://video", nil, PlayerModeGUI, 0, "/unused/mpv.sock", false, "/unused/auth.conf")
		windowTitle, mediaTitle := "", ""
		for _, arg := range args {
			if strings.HasPrefix(arg, "--title=") {
				windowTitle = strings.TrimPrefix(arg, "--title=")
			}
			if strings.HasPrefix(arg, "--force-media-title=") {
				mediaTitle = strings.TrimPrefix(arg, "--force-media-title=")
			}
		}
		if windowTitle != title || mediaTitle != title {
			t.Fatalf("titles = (%q, %q), want %q", windowTitle, mediaTitle, title)
		}
	}
}

func TestEpisodePlaybackTitleIncludesShow(t *testing.T) {
	client := NewEmptyAPIClient(nil)
	client.shows["show-one"] = testShow("show-one", "Fixture Show", 2024)
	for _, tc := range []struct {
		showID string
		want   string
	}{
		{"show-one", "Fixture Show S01E02 - Pilot"},
		{"missing", "S01E02 - Pilot"},
	} {
		t.Run(tc.showID, func(t *testing.T) {
			a := &App{client: client, playerStatus: PlayerStatusRunning}
			ep := testEpisode(time.Time{})
			ep.Season, ep.Episode = 1, 2
			a.launchEpisode(&DynamicNode{ShowID: tc.showID, Episode: ep, Label: "S01E02 - Pilot"})
			if a.pendingPlayback == nil || a.pendingPlayback.title != tc.want {
				t.Fatalf("episode playback title = %+v, want %q", a.pendingPlayback, tc.want)
			}
		})
	}
}

func TestSwitchPlaybackUsesLatestSelection(t *testing.T) {
	stop := make(chan struct{})
	invalidations := 0
	a := &App{playerStatus: PlayerStatusRunning, playerStop: stop, invalidate: func() { invalidations++ }}
	a.launchPlayback(playbackSource{title: "Second"})
	a.launchPlayback(playbackSource{title: "Third"})
	if invalidations != 2 {
		t.Fatalf("switch requests produced %d invalidations, want 2", invalidations)
	}
	if a.pendingPlayback == nil || a.pendingPlayback.title != "Third" {
		t.Fatalf("pending playback = %+v", a.pendingPlayback)
	}
	select {
	case <-stop:
	default:
		t.Fatal("player stop not requested")
	}
}

func TestResolveMPVPath(t *testing.T) {
	onPath := func(string) (string, error) { return "/usr/bin/mpv", nil }
	notOnPath := func(string) (string, error) { return "", exec.ErrNotFound }
	only := func(paths ...string) func(string) bool {
		return func(path string) bool { return slices.Contains(paths, path) }
	}
	for _, tc := range []struct {
		name, path, goos string
		lookPath         func(string) (string, error)
		executable       func(string) bool
		want             string
	}{
		{"linux keeps default", "mpv", "linux", notOnPath, only("/opt/homebrew/bin/mpv"), "mpv"},
		{"configured path kept", "/my/mpv", "darwin", notOnPath, only("/opt/homebrew/bin/mpv"), "/my/mpv"},
		{"configured name kept", "mpv-wrapper", "darwin", notOnPath, only("/opt/homebrew/bin/mpv"), "mpv-wrapper"},
		{"PATH wins", "mpv", "darwin", onPath, only("/opt/homebrew/bin/mpv"), "mpv"},
		{"homebrew arm64", "mpv", "darwin", notOnPath, only("/opt/homebrew/bin/mpv", "/usr/local/bin/mpv"), "/opt/homebrew/bin/mpv"},
		{"homebrew intel", "mpv", "darwin", notOnPath, only("/usr/local/bin/mpv", "/Applications/mpv.app/Contents/MacOS/mpv"), "/usr/local/bin/mpv"},
		{"macports", "mpv", "darwin", notOnPath, only("/opt/local/bin/mpv"), "/opt/local/bin/mpv"},
		{"mpv.app", "mpv", "darwin", notOnPath, only("/Applications/mpv.app/Contents/MacOS/mpv"), "/Applications/mpv.app/Contents/MacOS/mpv"},
		{"nothing found", "mpv", "darwin", notOnPath, only(), "mpv"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveMPVPath(tc.path, tc.goos, tc.lookPath, tc.executable); got != tc.want {
				t.Fatalf("resolveMPVPath(%q, %q) = %q, want %q", tc.path, tc.goos, got, tc.want)
			}
		})
	}
}

func TestIsExecutableFile(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "exe")
	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(exe, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plain, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !isExecutableFile(exe) || isExecutableFile(plain) || isExecutableFile(dir) || isExecutableFile(filepath.Join(dir, "missing")) {
		t.Fatal("isExecutableFile misclassified exe, plain file, directory or missing path")
	}
}

func TestPlayerNotFoundOnPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	runtimeDir, _, _, err := createPlayerRuntime("secret")
	if err != nil {
		t.Fatal(err)
	}
	a := &App{resumePos: make(map[string]float64), images: newAsyncCache[paint.ImageOp](1, 0)}
	if a.startPlayer(playerCommand{runtimeDir: runtimeDir, args: []string{"mpv"}}) {
		t.Fatal("startPlayer succeeded without mpv on PATH")
	}
	if a.playerStatus != PlayerStatusError || !strings.HasPrefix(a.playerError, "mpv not found: ") {
		t.Fatalf("missing mpv state = (%q, %q)", a.playerStatus, a.playerError)
	}
}

func TestPlayerLaunchFailureAndNonzeroExit(t *testing.T) {
	runtimeDir, _, _, err := createPlayerRuntime("secret")
	if err != nil {
		t.Fatal(err)
	}
	a := &App{resumePos: make(map[string]float64), images: newAsyncCache[paint.ImageOp](1, 0)}
	if a.startPlayer(playerCommand{runtimeDir: runtimeDir, args: []string{filepath.Join(t.TempDir(), "missing-mpv")}}) {
		t.Fatal("startPlayer succeeded for missing executable")
	}
	if a.playerStatus != PlayerStatusError || a.playerDone != nil {
		t.Fatalf("launch failure state = (%q, %v)", a.playerStatus, a.playerDone)
	}
	if _, err := os.Stat(runtimeDir); !os.IsNotExist(err) {
		t.Fatalf("failed launch left runtime directory: %v", err)
	}
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "fake-mpv")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 7\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	runtimeDir, configPath, sockPath, err := createPlayerRuntime("secret")
	if err != nil {
		t.Fatal(err)
	}
	args := buildMPVArgs(scriptPath, "Movie", "https://video", nil, PlayerModeGUI, 0, sockPath, false, configPath)
	if !a.startPlayer(playerCommand{mediaKey: "episode:show:1:2", sockPath: sockPath, runtimeDir: runtimeDir, args: args, mode: PlayerModeGUI}) {
		t.Fatalf("startPlayer failed: %s", a.playerError)
	}
	waitForCache(t, func() bool { return len(a.playerDone) == 1 })
	a.drainChannels()
	if a.playerStatus != PlayerStatusError || !strings.Contains(a.playerError, "exit status 7") {
		t.Fatalf("nonzero exit state = (%q, %q)", a.playerStatus, a.playerError)
	}
	if _, err := os.Stat(runtimeDir); !os.IsNotExist(err) {
		t.Fatalf("runtime directory remains after error exit: %v", err)
	}
}

func TestPlayerRuntimeRejectsLineBreaksAndUsesUniqueSocketPaths(t *testing.T) {
	for _, token := range []string{"token\nforged", "token\rforged", "token\x00forged"} {
		if _, _, _, err := createPlayerRuntime(token); err == nil || strings.Contains(err.Error(), token) {
			t.Fatalf("invalid token was accepted or leaked in error: %q, %v", token, err)
		}
	}
	firstDir, _, firstSocket, err := createPlayerRuntime("one")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(firstDir) }()
	secondDir, _, secondSocket, err := createPlayerRuntime("two")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(secondDir) }()
	if firstSocket == secondSocket || firstDir == secondDir {
		t.Fatalf("launch runtime paths are not unique: %q and %q", firstSocket, secondSocket)
	}
}

func TestMPVConfigQuoteEscapesHeaderListDelimiter(t *testing.T) {
	if got, want := mpvConfigQuote("X-Auth-Token: token,extra"), `"X-Auth-Token: token\,extra"`; got != want {
		t.Fatalf("quoted config value = %q, want %q", got, want)
	}
}

func TestPlaybackMediaKeys(t *testing.T) {
	if got := movieMediaKey("tt123"); got != "movie:tt123" {
		t.Fatalf("movie media key = %q", got)
	}
	if got := episodeMediaKey("tt456", 2, 3); got != "episode:tt456:2:3" {
		t.Fatalf("episode media key = %q", got)
	}
}

func TestBuildMPVArgsUsesNamedResumeThreshold(t *testing.T) {
	args := buildMPVArgs("mpv", "Movie", "https://video", []string{"https://subtitle"}, PlayerModeGUI, minimumResumePosition+1, "/tmp/mpv.sock", true, "/tmp/auth.conf")
	want := map[string]bool{
		"--start=31":                       false,
		"--fs":                             false,
		"--sub-file=https://subtitle":      false,
		"--input-ipc-server=/tmp/mpv.sock": false,
		"--include=/tmp/auth.conf":         false,
		"https://video":                    false,
	}
	for _, arg := range args {
		if _, ok := want[arg]; ok {
			want[arg] = true
		}
	}
	for arg, found := range want {
		if !found {
			t.Errorf("MPV args missing %q: %#v", arg, args)
		}
	}
}

func TestPlaybackTrackerSnapshot(t *testing.T) {
	tracker := newPlaybackTracker("movie:tt123")
	tracker.updatePos(91.5)

	mediaKey, pos, eof := tracker.snapshot()
	if mediaKey != "movie:tt123" {
		t.Fatalf("mediaKey = %q", mediaKey)
	}
	if pos != 91.5 {
		t.Fatalf("pos = %v", pos)
	}
	if eof {
		t.Fatal("eof = true")
	}
}

func TestApplyPlaybackSnapshotStoresResumePosition(t *testing.T) {
	tracker := newPlaybackTracker("movie:tt123")
	tracker.updatePos(91.5)
	close(tracker.done)

	a := &App{resumePos: make(map[string]float64)}
	a.applyPlaybackSnapshot(tracker)

	if got := a.resumePos["movie:tt123"]; got != 91.5 {
		t.Fatalf("resume position = %v", got)
	}
}

func TestPlayerCompletionAppliesItsLaunchResumeTracker(t *testing.T) {
	key := episodeMediaKey("show", 3, 4)
	tracker := newPlaybackTracker(key)
	tracker.updatePos(180)
	close(tracker.done)
	done := make(chan playerResult, 1)
	done <- playerResult{tracker: tracker}
	a := &App{
		playerStatus: PlayerStatusRunning,
		playerDone:   done,
		resumePos:    make(map[string]float64),
		images:       newAsyncCache[paint.ImageOp](1, 0),
	}
	a.drainChannels()
	if got := a.resumePos[key]; got != 180 {
		t.Fatalf("completed launch resume position = %v, want 180", got)
	}
	if a.playerStatus != PlayerStatusIdle || a.playerDone != nil {
		t.Fatalf("completion state = (%q, %v)", a.playerStatus, a.playerDone)
	}
}

func TestApplyPlaybackSnapshotStoresEpisodeResumePosition(t *testing.T) {
	key := episodeMediaKey("show", 3, 4)
	tracker := newPlaybackTracker(key)
	tracker.updatePos(180)
	close(tracker.done)

	a := &App{resumePos: make(map[string]float64)}
	a.applyPlaybackSnapshot(tracker)
	if got := a.resumePos[key]; got != 180 {
		t.Fatalf("episode resume position = %v, want 180", got)
	}
}

func TestApplyPlaybackSnapshotClearsResumeOnEOF(t *testing.T) {
	tracker := newPlaybackTracker("movie:tt123")
	tracker.updatePos(91.5)
	tracker.markEOF()
	close(tracker.done)

	a := &App{resumePos: map[string]float64{"movie:tt123": 42}}
	a.applyPlaybackSnapshot(tracker)

	if _, ok := a.resumePos["movie:tt123"]; ok {
		t.Fatal("resume position was not cleared")
	}
}

func TestDetectPlayerMode(t *testing.T) {
	emptyEnv := func(string) string { return "" }

	if got := detectPlayerModeFor("darwin", emptyEnv); got != PlayerModeGUI {
		t.Fatalf("macOS player mode = %q, want %q", got, PlayerModeGUI)
	}
	if got := detectPlayerModeFor("linux", emptyEnv); got != PlayerModeKMS {
		t.Fatalf("headless Linux player mode = %q, want %q", got, PlayerModeKMS)
	}
	if got := detectPlayerModeFor("linux", func(name string) string {
		if name == "DISPLAY" {
			return ":0"
		}
		return ""
	}); got != PlayerModeGUI {
		t.Fatalf("desktop Linux player mode = %q, want %q", got, PlayerModeGUI)
	}
}
