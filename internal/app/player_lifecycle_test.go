package app

import (
	"bufio"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPlayerPolicyDefaultsAndOverrides(t *testing.T) {
	for _, config := range []*Config{nil, {}, {MPVStartupTimeout: 3 * time.Second}, {MPVStopTimeout: 2 * time.Second}} {
		policy := playerPolicyFromConfig(config)
		if policy.managedLauncher || policy.connectTimeout != 3*time.Second || policy.stopTimeout != 2*time.Second {
			t.Fatalf("desktop policy changed: %+v", policy)
		}
	}
	config := &Config{MPVManagedLauncher: true, MPVStartupTimeout: 45 * time.Second, MPVStopTimeout: 30 * time.Second}
	a := New(nil, nil, config, nil, nil)
	if !a.playerPolicy.managedLauncher || a.playerPolicy.connectTimeout != config.MPVStartupTimeout || a.playerPolicy.stopTimeout != config.MPVStopTimeout {
		t.Fatalf("launcher policy not copied: %+v", a.playerPolicy)
	}
	policy := (App{}).playerPolicy.withDefaults()
	if policy != playerPolicyFromConfig(nil) {
		t.Fatalf("zero-valued app policy differs: %+v", policy)
	}
}

func TestPlayerManagedArgumentsAreOptIn(t *testing.T) {
	for _, managed := range []bool{false, true} {
		t.Run(fmt.Sprint(managed), func(t *testing.T) {
			client, err := NewAPIClient("http://localhost", "private-token", nil)
			if err != nil {
				t.Fatal(err)
			}
			a := New(client, nil, &Config{MPVPath: "launcher", MPVManagedLauncher: managed, PlayerFullscreen: true}, nil, nil)
			a.progress.entries["movie:first"] = progressEntry{Pos: 91}
			source := playbackSource{mediaKey: "movie:first", title: "A 'quoted' $ title", video: testMovie("first", "First", 2020)}
			command, err := a.buildPlayerCommand(source)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.RemoveAll(command.runtimeDir) }()
			want := buildMPVArgs("launcher", source.title, command.args[len(command.args)-1], nil,
				command.mode, 91, command.sockPath, true, filepath.Join(command.runtimeDir, "auth.conf"))
			last := len(want) - 1
			media := want[last]
			var colors []string
			for _, c := range osdColors {
				colors = append(colors, "--script-opts-append=pepe-color_"+c.name+"="+assColor(c.color))
			}
			want = append(want[:last:last],
				"--script="+filepath.Join(command.runtimeDir, "pepe.lua"),
				"--script-opts-append=pepe-controls=keyboard",
				"--script-opts-append=pepe-help=yes", "--script-opts-append=pepe-panel=yes",
				"--script-opts-append=pepe-web=no")
			want = append(want, colors...)
			want = append(want,
				"--osd-fonts-dir="+filepath.Join(command.runtimeDir, "fonts"),
				"--osd-font=Roboto", "--script-opts-append=console-background_alpha=0", media)
			if managed {
				want = append([]string{want[0], "--managed-runtime=" + command.runtimeDir}, want[1:]...)
			}
			if !reflect.DeepEqual(command.args, want) {
				t.Fatalf("arguments = %#v, want %#v", command.args, want)
			}
			if strings.Contains(strings.Join(command.args, "\x00"), "private-token") {
				t.Fatal("authentication token entered arguments")
			}
		})
	}
}

func lifecycleCommand(t *testing.T, a *App, body string) playerCommand {
	t.Helper()
	script := filepath.Join(t.TempDir(), "launcher")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nruntime=${1#--managed-runtime=}\n"+body), 0700); err != nil {
		t.Fatal(err)
	}
	client, err := NewAPIClient("http://localhost", "private-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	a.client = client
	command, err := a.buildPlayerCommand(playbackSource{mediaKey: "movie:first", title: "First", video: testMovie("first", "First", 2020)})
	if err != nil {
		t.Fatal(err)
	}
	command.args[0] = script
	t.Cleanup(func() { _ = os.RemoveAll(command.runtimeDir) })
	return command
}

func startLifecyclePlayer(t *testing.T, a *App, command playerCommand) {
	t.Helper()
	if !a.startPlayer(command) {
		t.Fatal(a.playerError)
	}
	stop := a.playerStop
	t.Cleanup(func() {
		select {
		case <-stop:
		default:
			close(stop)
		}
	})
}

func lifecycleResult(t *testing.T, a *App) playerResult {
	t.Helper()
	select {
	case result := <-a.playerDone:
		result.tracker.wait(time.Second)
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("launcher did not finish")
		return playerResult{}
	}
}

const confirmRuntime = "printf \"stopped-and-restored\\n\" > \"$runtime/launcher-complete.tmp\"\nmv \"$runtime/launcher-complete.tmp\" \"$runtime/launcher-complete\"\n"

func TestPlayerManagedCompletion(t *testing.T) {
	for _, test := range []struct {
		name string
		ack  string
		exit int
	}{
		{"success", "stopped-and-restored\n", 0},
		{"confirmed error", "stopped-and-restored\n", 7},
		{"missing", "", 0},
		{"missing with error", "", 7},
		{"malformed", "wrong\n", 0},
		{"missing newline", "stopped-and-restored", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := New(nil, nil, &Config{MPVManagedLauncher: true}, nil, nil)
			body := fmt.Sprintf("exit %d\n", test.exit)
			if test.ack != "" {
				body = fmt.Sprintf("printf '%%s' '%s' > \"$runtime/launcher-complete.tmp\"\nmv \"$runtime/launcher-complete.tmp\" \"$runtime/launcher-complete\"\n", test.ack) + body
			}
			command := lifecycleCommand(t, a, body)
			startLifecyclePlayer(t, a, command)
			result := lifecycleResult(t, a)
			confirmed := test.ack == "stopped-and-restored\n"
			if confirmed {
				if result.retainedRuntime != "" {
					t.Fatal("confirmed runtime retained")
				}
				if _, err := os.Stat(command.runtimeDir); !os.IsNotExist(err) {
					t.Fatal("confirmed runtime not cleaned")
				}
			} else {
				if result.retainedRuntime != command.runtimeDir || result.err == nil || !strings.Contains(result.err.Error(), "unconfirmed") {
					t.Fatalf("unconfirmed completion = %+v", result)
				}
				if _, err := os.Stat(filepath.Join(command.runtimeDir, "auth.conf")); err != nil {
					t.Fatal("private runtime deleted before teardown")
				}
			}
			var exitErr *exec.ExitError
			if test.exit != 0 && (!errors.As(result.err, &exitErr) || exitErr.ExitCode() != test.exit) {
				t.Fatalf("exit status lost: %v", result.err)
			}
			if confirmed && test.exit == 0 && result.err != nil {
				t.Fatal(result.err)
			}
			a.playerDone <- result
			a.drainChannels()
			wantStatus := PlayerStatusIdle
			if !confirmed || test.exit != 0 {
				wantStatus = PlayerStatusError
			}
			if a.playerStatus != wantStatus {
				t.Fatalf("completion status = %s, want %s", a.playerStatus, wantStatus)
			}
		})
	}
}

func TestPlayerManagedStartFailureCleansRuntime(t *testing.T) {
	a := New(nil, nil, &Config{MPVManagedLauncher: true}, nil, nil)
	command := lifecycleCommand(t, a, "exit 0\n")
	if err := os.Remove(command.args[0]); err != nil {
		t.Fatal(err)
	}
	if a.startPlayer(command) || a.playerStatus != PlayerStatusError || a.playerDone != nil || a.playerCleanupDir != "" {
		t.Fatal("failed start was treated as a submitted managed launch")
	}
	if _, err := os.Stat(command.runtimeDir); !os.IsNotExist(err) {
		t.Fatal("failed start retained private runtime")
	}
}

func TestPlayerUnconfirmedTeardownBlocksReplacementUntilRecovery(t *testing.T) {
	a := New(nil, nil, &Config{MPVManagedLauncher: true}, nil, nil)
	command := lifecycleCommand(t, a, "exit 0\n")
	startLifecyclePlayer(t, a, command)
	result := lifecycleResult(t, a)
	a.playerDone <- result
	a.pendingPlayback = &playbackSource{title: "replacement"}
	a.drainChannels()
	if a.playerCleanupDir != command.runtimeDir || a.pendingPlayback != nil || a.playerStatus != PlayerStatusError {
		t.Fatal("unconfirmed teardown did not block queued replacement")
	}

	next := lifecycleCommand(t, a, confirmRuntime)
	if a.startPlayer(next) {
		t.Fatal("new launcher started before previous teardown")
	}
	if _, err := os.Stat(next.runtimeDir); !os.IsNotExist(err) {
		t.Fatal("rejected launch leaked its runtime")
	}
	ack := filepath.Join(command.runtimeDir, "launcher-complete")
	if err := os.WriteFile(ack+".tmp", []byte("stopped-and-restored\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(ack+".tmp", ack); err != nil {
		t.Fatal(err)
	}
	next = lifecycleCommand(t, a, confirmRuntime)
	startLifecyclePlayer(t, a, next)
	if a.playerCleanupDir != "" {
		t.Fatal("confirmed recovery did not clear block")
	}
	if _, err := os.Stat(command.runtimeDir); !os.IsNotExist(err) {
		t.Fatal("recovered runtime not cleaned")
	}
	a.playerDone <- lifecycleResult(t, a)
	a.drainChannels()
	if a.playerStatus != PlayerStatusIdle {
		t.Fatal(a.playerError)
	}
}

func TestPlayerManagedCancellationWaitsForRecovery(t *testing.T) {
	a := New(nil, nil, &Config{MPVManagedLauncher: true, MPVStopTimeout: 4 * time.Second}, nil, nil)
	ready := filepath.Join(t.TempDir(), "ready")
	t.Setenv("PEPE_TEST_READY", ready)
	command := lifecycleCommand(t, a, "trap 'sleep 2.2; "+confirmRuntime+"exit 7' TERM\n: > \"$PEPE_TEST_READY\"\nwhile :; do sleep 0.05; done\n")
	startLifecyclePlayer(t, a, command)
	waitForCache(t, func() bool { _, err := os.Stat(ready); return err == nil })
	close(a.playerStop)
	a.playerStop = nil
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(command.runtimeDir, "auth.conf")); err != nil {
		t.Fatal("private runtime removed during recovery")
	}
	result := lifecycleResult(t, a)
	var exitErr *exec.ExitError
	if result.retainedRuntime != "" || !errors.As(result.err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("cancellation completion = %+v", result)
	}
	if _, err := os.Stat(command.runtimeDir); !os.IsNotExist(err) {
		t.Fatal("confirmed cancellation runtime not cleaned")
	}
}

func TestPlayerManagedForcedTerminationRetainsRuntime(t *testing.T) {
	a := New(nil, nil, &Config{MPVManagedLauncher: true, MPVStopTimeout: 50 * time.Millisecond}, nil, nil)
	ready := filepath.Join(t.TempDir(), "ready")
	t.Setenv("PEPE_TEST_READY", ready)
	command := lifecycleCommand(t, a, "trap '' TERM\n: > \"$PEPE_TEST_READY\"\nwhile :; do sleep 0.05; done\n")
	startLifecyclePlayer(t, a, command)
	waitForCache(t, func() bool { _, err := os.Stat(ready); return err == nil })
	close(a.playerStop)
	a.playerStop = nil
	result := lifecycleResult(t, a)
	var exitErr *exec.ExitError
	if result.retainedRuntime != command.runtimeDir || !errors.As(result.err, &exitErr) ||
		exitErr.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatalf("forced termination = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(command.runtimeDir, "auth.conf")); err != nil {
		t.Fatal("forced launcher termination deleted private runtime")
	}
}

func TestPlayerIPCTracksDelayedStartup(t *testing.T) {
	dir, _, socket, err := createPlayerRuntime("secret")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	tracker := newPlaybackTracker("movie:delayed", nil)
	startIPC(socket, tracker, 5*time.Second)
	defer tracker.stopIPC()
	time.Sleep(3200 * time.Millisecond) // Beyond the unchanged desktop timeout.
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		scanner := bufio.NewScanner(conn)
		for range 3 {
			if !scanner.Scan() {
				return
			}
		}
		_, _ = conn.Write([]byte("{\"event\":\"property-change\",\"name\":\"time-pos\",\"data\":125}\n{\"event\":\"property-change\",\"name\":\"eof-reached\",\"data\":true}\n"))
	}()
	select {
	case <-tracker.done:
	case <-time.After(2 * time.Second):
		t.Fatal("delayed IPC connection failed")
	}
	key, pos, eof := tracker.snapshot()
	if key != "movie:delayed" || pos != 125 || !eof {
		t.Fatalf("snapshot = %q, %v, %v", key, pos, eof)
	}
}

func TestPlayerIPCDeadlineAndCancellation(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(fmt.Sprint(cancel), func(t *testing.T) {
			dir, _, socket, err := createPlayerRuntime("secret")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.RemoveAll(dir) }()
			timeout := 150 * time.Millisecond
			if cancel {
				timeout = 15 * time.Second
			}
			started := time.Now()
			tracker := newPlaybackTracker("movie:missing", nil)
			startIPC(socket, tracker, timeout)
			defer tracker.stopIPC()
			if cancel {
				tracker.stopIPC()
			}
			select {
			case <-tracker.done:
			case <-time.After(time.Second):
				t.Fatal("IPC timeout/cancellation did not stop tracking")
			}
			if !cancel && time.Since(started) < timeout {
				t.Fatal("IPC timeout truncated to polling attempts")
			}
		})
	}
}

// mpv and wrapper output reaches the redacted log, and a background child
// holding the pipe open does not delay the playback result.
func TestPlayerOutputIsLogged(t *testing.T) {
	a := New(nil, nil, nil, nil, nil)
	command := lifecycleCommand(t, a, `echo "Playing: https://cdn.example/v.mp4?sig=private-token"
echo "[ytdl_hook] ERROR: Unsupported URL" >&2
sleep 3 &
exit 3
`)
	a.logs = NewLogBuffer("private-token")
	a.client.log = slog.New(a.logs.Handler(nil))
	started := time.Now()
	startLifecyclePlayer(t, a, command)
	select {
	case <-a.playerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("a background child holding the output pipe delayed the result")
	}
	want := []string{
		"INFO pepe: player started",
		"WARN mpv: Playing: https://cdn.example/v.mp4?…",
		"ERROR mpv: [ytdl_hook] ERROR: Unsupported URL",
		"ERROR pepe: player failed error=exit status 3",
	}
	var text string
	for time.Since(started) < 2*time.Second {
		entries, _, _ := a.logs.since(0)
		var lines []string
		for _, entry := range entries {
			lines = append(lines, entry.String())
		}
		text = strings.Join(lines, "\n")
		missing := false
		for _, line := range want {
			missing = missing || !strings.Contains(text, line)
		}
		if !missing {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, line := range want {
		if !strings.Contains(text, line) {
			t.Errorf("log is missing %q:\n%s", line, text)
		}
	}
	if strings.Contains(text, "private-token") {
		t.Fatalf("log leaked the token:\n%s", text)
	}
}

// mpv handles SIGTERM and exits with status 4; after our Stop request that is
// a clean stop, but the same status without a Stop request remains a failure.
func TestPlayerStopWithMPVExitStatus(t *testing.T) {
	for _, stop := range []bool{true, false} {
		t.Run(fmt.Sprintf("stop=%t", stop), func(t *testing.T) {
			a := New(nil, nil, nil, nil, nil)
			ready := filepath.Join(t.TempDir(), "ready")
			t.Setenv("PEPE_TEST_READY", ready)
			body := "trap 'exit 4' TERM\n: > \"$PEPE_TEST_READY\"\nwhile :; do sleep 0.05; done\n"
			if !stop {
				body = ": > \"$PEPE_TEST_READY\"\nexit 4\n"
			}
			command := lifecycleCommand(t, a, body)
			startLifecyclePlayer(t, a, command)
			waitForCache(t, func() bool { _, err := os.Stat(ready); return err == nil })
			if stop {
				close(a.playerStop)
				a.playerStop = nil
			}
			result := lifecycleResult(t, a)
			if (result.err == nil) != stop {
				t.Fatalf("stop=%t: result error = %v", stop, result.err)
			}
		})
	}
}
