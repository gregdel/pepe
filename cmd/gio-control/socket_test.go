package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/widget"
	"github.com/gregdel/pepe/internal/driver"
)

func newButtonDriver(t *testing.T, label string, clicks *int) *driver.Driver {
	t.Helper()
	var button widget.Clickable
	drv, err := driver.New(driver.Config{Width: 200, Height: 100, PxPerDp: 1, PxPerSp: 1}, func(gtx layout.Context) layout.Dimensions {
		if button.Clicked(gtx) {
			*clicks++
		}
		return button.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			semantic.Button.Add(gtx.Ops)
			semantic.LabelOp(label).Add(gtx.Ops)
			return layout.Dimensions{Size: image.Pt(100, 50)}
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(drv.Close)
	return drv
}

// startSocketServer runs serveSocket in the background and returns once the
// socket accepts connections, plus a channel that yields its return value.
func startSocketServer(t *testing.T, ctx context.Context, socketPath string, drv *driver.Driver) <-chan error {
	t.Helper()
	return startServer(t, ctx, socketPath, newServer(drv, t.TempDir()))
}

func startServer(t *testing.T, ctx context.Context, socketPath string, srv *server) <-chan error {
	t.Helper()
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- serveSocket(ctx, socketPath, srv, func() error {
			close(ready)
			return nil
		})
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("serveSocket() returned before ready: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("socket server did not become ready")
	}
	return done
}

func waitServer(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveSocket() = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("socket server did not stop")
	}
}

const testCallTimeout = 10 * time.Second

func shortTempDir(t *testing.T) string {
	t.Helper()
	// Unix socket paths are limited to ~108 bytes; t.TempDir can exceed it.
	dir, err := os.MkdirTemp("", "gioctl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func shortSocketPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(shortTempDir(t), "s.sock")
}

func TestSocketServerSharesStateAcrossConnections(t *testing.T) {
	t.Setenv("GIO_CONTROL_SCREENSHOT_DIR", t.TempDir())
	clicks := 0
	drv := newButtonDriver(t, "Apply", &clicks)
	socketPath := shortSocketPath(t)
	done := startSocketServer(t, context.Background(), socketPath, drv)

	if pid, err := os.ReadFile(pidPath(socketPath)); err != nil || strings.TrimSpace(string(pid)) == "" {
		t.Fatalf("pid file = %q, %v", pid, err)
	}
	if _, err := sendCall(socketPath, testCallTimeout, callRequest{Method: "click", Params: clickParams{Label: "Apply"}}); err != nil {
		t.Fatal(err)
	}
	if clicks != 1 {
		t.Fatalf("clicks = %d, want 1", clicks)
	}
	var paths []string
	for range 2 {
		result, err := sendCall(socketPath, testCallTimeout, callRequest{Method: "screenshot"})
		if err != nil {
			t.Fatal(err)
		}
		var shot struct {
			AbsolutePath string `json:"absolutePath"`
		}
		if err := json.Unmarshal(result, &shot); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(shot.AbsolutePath); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, filepath.Base(shot.AbsolutePath))
	}
	if paths[0] != "frame-000001.png" || paths[1] != "frame-000002.png" {
		t.Fatalf("screenshot names = %v, want increasing frame numbers across connections", paths)
	}
	if viewport, err := fetchViewport(socketPath, testCallTimeout); err != nil || viewport != image.Rect(0, 0, 200, 100) {
		t.Fatalf("fetchViewport() = %v, %v; want 200x100", viewport, err)
	}
	if _, err := sendCall(socketPath, testCallTimeout, callRequest{Method: "nope"}); err == nil || !strings.Contains(err.Error(), "unknown method") {
		t.Fatalf("unknown method error = %v", err)
	}

	if _, err := sendCall(socketPath, testCallTimeout, callRequest{Method: "shutdown"}); err != nil {
		t.Fatal(err)
	}
	waitServer(t, done)
	for _, path := range []string{socketPath, pidPath(socketPath)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists after shutdown: %v", path, err)
		}
	}
}

func TestSocketServerStopsOnContextCancel(t *testing.T) {
	clicks := 0
	drv := newButtonDriver(t, "Apply", &clicks)
	socketPath := shortSocketPath(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := startSocketServer(t, ctx, socketPath, drv)
	// An idle client connection must not keep the server alive.
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	cancel()
	waitServer(t, done)
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("socket still exists after cancel: %v", err)
	}
}

func TestSocketServerRefusesLiveSocketAndReplacesStaleOne(t *testing.T) {
	clicks := 0
	drv := newButtonDriver(t, "Apply", &clicks)
	socketPath := shortSocketPath(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := startSocketServer(t, ctx, socketPath, drv)
	err := serveSocket(context.Background(), socketPath, newServer(drv, t.TempDir()), nil)
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second server error = %v, want already running", err)
	}
	cancel()
	waitServer(t, done)

	// A socket file nobody listens on is stale and gets replaced.
	stale, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = stale.Close()
	if _, err := os.Stat(socketPath); err != nil {
		t.Fatalf("stale socket missing: %v", err)
	}
	done = startSocketServer(t, context.Background(), socketPath, drv)
	if _, err := sendCall(socketPath, testCallTimeout, callRequest{Method: "shutdown"}); err != nil {
		t.Fatal(err)
	}
	waitServer(t, done)

	if err := os.WriteFile(socketPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := serveSocket(context.Background(), socketPath, newServer(drv, t.TempDir()), nil); err == nil || !strings.Contains(err.Error(), "not a socket") {
		t.Fatalf("regular file error = %v, want not a socket", err)
	}
}

func TestSendCallReportsMissingServer(t *testing.T) {
	_, err := sendCall(filepath.Join(shortTempDir(t), "missing.sock"), testCallTimeout, callRequest{Method: "tree"})
	if err == nil || !strings.Contains(err.Error(), "scripts/gio start") {
		t.Fatalf("missing server error = %v", err)
	}
}

func TestSocketServerDropsIdleConnection(t *testing.T) {
	clicks := 0
	drv := newButtonDriver(t, "Apply", &clicks)
	socketPath := shortSocketPath(t)
	srv := newServer(drv, t.TempDir())
	srv.idleTimeout = 200 * time.Millisecond
	done := startServer(t, context.Background(), socketPath, srv)

	idle, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idle.Close() }()
	if _, err := sendCall(socketPath, 5*time.Second, callRequest{Method: "click", Params: clickParams{Label: "Apply"}}); err != nil {
		t.Fatalf("call queued behind an idle connection: %v", err)
	}
	if clicks != 1 {
		t.Fatalf("clicks = %d, want 1", clicks)
	}
	if _, err := sendCall(socketPath, testCallTimeout, callRequest{Method: "shutdown"}); err != nil {
		t.Fatal(err)
	}
	waitServer(t, done)
}

func TestSendCallTimesOutWithoutResponse(t *testing.T) {
	socketPath := shortSocketPath(t)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			defer func() { _ = conn.Close() }()
			_, _ = io.Copy(io.Discard, conn)
		}
	}()
	start := time.Now()
	_, err = sendCall(socketPath, 200*time.Millisecond, callRequest{Method: "tree"})
	if err == nil || !strings.Contains(err.Error(), "no response from gio-control within 200ms") {
		t.Fatalf("sendCall() error = %v, want timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("sendCall() took %s", elapsed)
	}
}

func TestSendCallTimesOutAfterPartialResponse(t *testing.T) {
	socketPath := shortSocketPath(t)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			defer func() { _ = conn.Close() }()
			_, _ = io.WriteString(conn, `{"id":1,"result":[{"id":`)
			_, _ = io.Copy(io.Discard, conn)
		}
	}()
	_, err = sendCall(socketPath, 200*time.Millisecond, callRequest{Method: "tree"})
	if err == nil || !strings.Contains(err.Error(), "no response from gio-control within 200ms") {
		t.Fatalf("sendCall() error = %v, want timeout", err)
	}
}

func TestProbe(t *testing.T) {
	socketPath := shortSocketPath(t)
	if code := runProbe([]string{"--socket", socketPath}, io.Discard); code != 1 {
		t.Fatalf("runProbe(no server) = %d, want 1", code)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	// Nothing accepts: a busy server still completes connections in the backlog.
	if code := runProbe([]string{"--socket", socketPath}, io.Discard); code != 0 {
		t.Fatalf("runProbe(live socket) = %d, want 0", code)
	}
	_ = listener.Close()
	if code := runProbe([]string{"--socket", socketPath, "extra"}, io.Discard); code != 2 {
		t.Fatalf("runProbe(extra argument) = %d, want 2", code)
	}
}

func TestSocketServerKeepsReplacedSocketAndPidFile(t *testing.T) {
	clicks := 0
	drv := newButtonDriver(t, "Apply", &clicks)
	socketPath := shortSocketPath(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := startSocketServer(t, ctx, socketPath, drv)

	// Simulate a restart that bound a new server before this one exited.
	if err := os.Remove(socketPath); err != nil {
		t.Fatal(err)
	}
	replacement, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = replacement.Close() }()
	if err := os.WriteFile(pidPath(socketPath), []byte("999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cancel()
	waitServer(t, done)

	if _, err := os.Stat(socketPath); err != nil {
		t.Fatalf("replacement socket removed: %v", err)
	}
	if pid, err := os.ReadFile(pidPath(socketPath)); err != nil || string(pid) != "999999\n" {
		t.Fatalf("replacement pid file = %q, %v", pid, err)
	}
}

func TestScreenshotSkipsExistingFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GIO_CONTROL_SCREENSHOT_DIR", dir)
	earlier := filepath.Join(dir, "frame-000001.png")
	if err := os.WriteFile(earlier, []byte("earlier run"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A dangling symlink is an existing name too; never write through it.
	if err := os.Symlink(filepath.Join(dir, "missing.png"), filepath.Join(dir, "frame-000002.png")); err != nil {
		t.Fatal(err)
	}
	clicks := 0
	drv := newButtonDriver(t, "Apply", &clicks)
	var shotNumber uint64
	result, _, err := dispatch(request{Method: "screenshot"}, drv, t.TempDir(), &shotNumber)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.(map[string]any)["absolutePath"]; got != filepath.Join(dir, "frame-000003.png") {
		t.Fatalf("screenshot path = %v, want frame-000003.png", got)
	}
	if _, err := os.Lstat(filepath.Join(dir, "missing.png")); !os.IsNotExist(err) {
		t.Fatalf("screenshot written through symlink: %v", err)
	}
	if data, err := os.ReadFile(earlier); err != nil || string(data) != "earlier run" {
		t.Fatalf("earlier screenshot overwritten: %q, %v", data, err)
	}
}

func TestScreenshotReportsUnwritableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	t.Setenv("GIO_CONTROL_SCREENSHOT_DIR", dir)
	clicks := 0
	drv := newButtonDriver(t, "Apply", &clicks)
	var shotNumber uint64
	_, _, err := dispatch(request{Method: "screenshot"}, drv, t.TempDir(), &shotNumber)
	if err == nil || !strings.Contains(err.Error(), "create screenshot") {
		t.Fatalf("screenshot error = %v, want create failure", err)
	}
	if shotNumber != 1 {
		t.Fatalf("shot number = %d, want 1 (no retry on a non-existence error)", shotNumber)
	}
}

func TestSocketServerCreatesParentDirectory(t *testing.T) {
	clicks := 0
	drv := newButtonDriver(t, "Apply", &clicks)
	socketPath := filepath.Join(shortTempDir(t), "a", "b", "s.sock")
	done := startSocketServer(t, context.Background(), socketPath, drv)
	if _, err := sendCall(socketPath, testCallTimeout, callRequest{Method: "shutdown"}); err != nil {
		t.Fatal(err)
	}
	waitServer(t, done)
}

func TestSocketPathValidation(t *testing.T) {
	long := "/" + strings.Repeat("x", maxSocketPathLen)
	if err := serveSocket(context.Background(), long, nil, nil); err == nil || !strings.Contains(err.Error(), "Unix socket limit") {
		t.Fatalf("serveSocket(long path) error = %v", err)
	}
	if _, err := sendCall(long, testCallTimeout, callRequest{Method: "tree"}); err == nil || !strings.Contains(err.Error(), "Unix socket limit") {
		t.Fatalf("sendCall(long path) error = %v", err)
	}
	if err := serveSocket(context.Background(), "", nil, nil); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("serveSocket(empty path) error = %v", err)
	}
}

func TestScreenshotContinuesAfterHighestFrame(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GIO_CONTROL_SCREENSHOT_DIR", dir)
	for _, name := range []string{"frame-000002.png", "frame-000041.png", "frame-x.png", "other-000099.png"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	clicks := 0
	drv := newButtonDriver(t, "Apply", &clicks)
	var shotNumber uint64
	for _, want := range []string{"frame-000042.png", "frame-000043.png"} {
		result, _, err := dispatch(request{Method: "screenshot"}, drv, t.TempDir(), &shotNumber)
		if err != nil {
			t.Fatal(err)
		}
		if got := result.(map[string]any)["absolutePath"]; got != filepath.Join(dir, want) {
			t.Fatalf("screenshot path = %v, want %s", got, want)
		}
	}
}

func TestInputActionsReportSelection(t *testing.T) {
	var button widget.Clickable
	drv, err := driver.New(driver.Config{Width: 200, Height: 100, PxPerDp: 1, PxPerSp: 1}, func(gtx layout.Context) layout.Dimensions {
		return button.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			semantic.Button.Add(gtx.Ops)
			semantic.LabelOp("Play").Add(gtx.Ops)
			size := image.Pt(100, 50)
			defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
			semantic.SelectedOp(true).Add(gtx.Ops)
			return layout.Dimensions{Size: size}
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(drv.Close)
	var shotNumber uint64
	for _, req := range []request{
		{Method: "key", Params: json.RawMessage(`{"name":"DownArrow"}`)},
		{Method: "click", Params: json.RawMessage(`{"label":"Play"}`)},
	} {
		result, _, err := dispatch(req, drv, t.TempDir(), &shotNumber)
		if err != nil {
			t.Fatalf("%s: %v", req.Method, err)
		}
		selected, ok := result.(map[string]any)["selected"].([]driver.Selection)
		if !ok || len(selected) != 1 || selected[0].Label != "Play" {
			t.Fatalf("%s selected = %#v, want the Play button", req.Method, result)
		}
	}
}

func TestScreenshotCropReportsOutputSize(t *testing.T) {
	t.Setenv("GIO_CONTROL_SCREENSHOT_DIR", t.TempDir())
	clicks := 0
	drv := newButtonDriver(t, "Apply", &clicks)
	var shotNumber uint64
	params := json.RawMessage(`{"crop":{"x":10,"y":10,"width":100,"height":60},"scale":0.5}`)
	result, _, err := dispatch(request{Method: "screenshot", Params: params}, drv, t.TempDir(), &shotNumber)
	if err != nil {
		t.Fatal(err)
	}
	got := result.(map[string]any)
	if got["width"] != 50 || got["height"] != 30 {
		t.Fatalf("screenshot size = %vx%v, want 50x30", got["width"], got["height"])
	}
	for _, bad := range []string{
		`{"crop":{"x":0,"y":0,"width":0,"height":10}}`,
		`{"crop":{"x":9223372036854775807,"y":0,"width":10,"height":10}}`,
		`{"crop":{"x":0,"y":9223372036854775807,"width":10,"height":10}}`,
	} {
		if _, _, err := dispatch(request{Method: "screenshot", Params: json.RawMessage(bad)}, drv, t.TempDir(), &shotNumber); err == nil {
			t.Fatalf("screenshot with crop %s succeeded", bad)
		}
	}
}

func TestActionResultTreatsRenderFailureAsApplied(t *testing.T) {
	clicks := 0
	drv := newButtonDriver(t, "Apply", &clicks)
	result, _, err := actionResult(drv, fmt.Errorf("%w: lost context", driver.ErrRender))
	if err != nil {
		t.Fatalf("actionResult(render error) error = %v, want a warning", err)
	}
	got := result
	if got["ok"] != true || !strings.Contains(fmt.Sprint(got["warning"]), "lost context") || got["selected"] != nil {
		t.Fatalf("actionResult(render error) = %#v", got)
	}
	other := errors.New("no such key")
	if _, _, err := actionResult(drv, other); !errors.Is(err, other) {
		t.Fatalf("actionResult(input error) error = %v, want %v", err, other)
	}
}

func TestWriteScreenshotSkipsTakenNames(t *testing.T) {
	clicks := 0
	drv := newButtonDriver(t, "Apply", &clicks)
	dir := t.TempDir()
	// Names taken after the server picked its first number.
	for _, name := range []string{"frame-000002.png", "frame-000003.png"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	shotNumber := uint64(1)
	path, size, err := writeScreenshot(drv, dir, &shotNumber, driver.ScreenshotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "frame-000004.png" || size != image.Pt(200, 100) {
		t.Fatalf("writeScreenshot() = %s %v, want frame-000004.png 200x100", path, size)
	}
	for _, name := range []string{"frame-000002.png", "frame-000003.png"} {
		if data, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(data) != "keep" {
			t.Fatalf("%s was replaced: %q, %v", name, data, err)
		}
	}
}
