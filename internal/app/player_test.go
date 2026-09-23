package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gioui.org/op/paint"
)

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

	a := &App{playerStatus: PlayerStatusIdle, resumePos: make(map[string]float64), images: newAsyncCache[paint.ImageOp](1, 0)}
	if !a.startPlayer(playerCommand{mediaKey: "movie:tt123", sockPath: sockPath, runtimeDir: runtimeDir, args: args, mode: PlayerModeGUI}) {
		t.Fatalf("startPlayer failed: %s", a.playerError)
	}
	done := a.playerDone
	if a.playerStatus != PlayerStatusRunning {
		t.Fatalf("player status = %q, want Running", a.playerStatus)
	}
	// Repeated Play is ignored before command construction and cannot replace
	// the active launch's completion channel or status.
	a.launchPlayback(playbackSource{})
	if a.playerDone != done || a.playerStatus != PlayerStatusRunning {
		t.Fatal("repeated Play changed active player state")
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
