package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func webRequest(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestWebPageControlsAndCopy(t *testing.T) {
	web := &webUI{commands: make(chan webCommand, webQueueSize)}
	response := webRequest(web.handler(), "GET", "/", "")
	page := response.Body.String()
	for _, removed := range []string{
		`data-action="quit"`, `data-action="refresh"`, "Quit Pépé", "No authentication. Use only on a trusted network.",
		">Pause / Resume<", ">−10 seconds<", ">+10 seconds<",
		"Right opens details. OK plays from details.",
		"Replaces current playback. Website support depends on your existing mpv setup.",
		"HTTP or HTTPS video URL",
	} {
		if strings.Contains(page, removed) {
			t.Errorf("page still contains %q", removed)
		}
	}
	for _, retained := range []string{
		`aria-label="Video URL"`, `id="url-form"`, `data-action="right"`, `data-command="stop"`,
		`id="now-playing"`, `class="playback-bar"`, `aria-label="Playback controls"`,
		`aria-label="Seek back 10 seconds"`, `aria-label="Pause or resume"`,
		`aria-label="Seek forward 10 seconds"`, `aria-label="Stop playback"`,
	} {
		if !strings.Contains(page, retained) {
			t.Errorf("page is missing %q", retained)
		}
	}
	if got := strings.Count(page, `aria-hidden="true" focusable="false"`); got != 4 {
		t.Errorf("decorative playback SVG icons = %d, want 4", got)
	}
}

func TestWebHTTPValidationAndAssets(t *testing.T) {
	web := &webUI{commands: make(chan webCommand, webQueueSize)}
	handler := web.handler()
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/", "", 200},
		{"GET", "/remote.css", "", 200},
		{"GET", "/remote.js", "", 200},
		{"GET", "/roboto.ttf", "", 200},
		{"GET", "/symbols.ttf", "", 200},
		{"GET", "/symbols2.ttf", "", 200},
		{"GET", "/api/status", "", 200},
		{"GET", "/main.go", "", 404},
		{"GET", "/api/play", "", 405},
		{"POST", "/api/action", `{"action":"right"}`, 202},
		{"POST", "/api/action", `{"action":"run"}`, 400},
		{"POST", "/api/action", `{"action":"right","extra":1}`, 400},
		{"POST", "/api/action", `{"action":"right"} {}`, 400},
		{"POST", "/api/action", `{`, 400},
		{"POST", "/api/play", `{"url":"https://media.example/video"}`, 202},
		{"POST", "/api/play", `{"url":"file:///private"}`, 400},
		{"POST", "/api/play", `{"url":"https://media.example/%zz?secret=private"}`, 400},
		{"POST", "/api/player", `{"command":"pause"}`, 202},
		{"POST", "/api/player", `{"command":"stop"}`, 202},
		{"POST", "/api/player", `{"command":"run"}`, 400},
		{"POST", "/api/player", `{"command":["run","sh"]}`, 400},
		{"POST", "/api/play", `{"url":"https://media/` + strings.Repeat("x", 17<<10) + `"}`, 413},
	} {
		t.Run(tc.method+tc.path+fmt.Sprint(tc.status)+fmt.Sprint(len(tc.body)), func(t *testing.T) {
			response := webRequest(handler, tc.method, tc.path, tc.body)
			if response.Code != tc.status {
				t.Fatalf("response = %d %s, want %d", response.Code, response.Body.String(), tc.status)
			}
			if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Frame-Options") != "DENY" || response.Header().Get("Content-Security-Policy") == "" {
				t.Fatal("missing security headers")
			}
			if strings.Contains(response.Body.String(), "secret=private") {
				t.Fatal("URL leaked in error response")
			}
		})
	}
	request := httptest.NewRequest("POST", "/api/play", strings.NewReader(`{"url":"http://media/video"}`))
	request.Header.Set("Content-Type", "text/plain")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatal("simple form-compatible content type accepted")
	}
}

func TestWebCrossOriginProtection(t *testing.T) {
	web := &webUI{commands: make(chan webCommand, webQueueSize)}
	handler := web.handler()
	for _, tc := range []struct {
		origin, site string
		status       int
	}{
		{"", "", 202}, // Non-browser clients remain unauthenticated.
		{"http://example.com", "same-origin", 202},
		{"http://example.com", "", 202},
		{"https://evil.example", "", 403},
		{"https://evil.example", "cross-site", 403},
		{"http://example.com", "same-site", 403},
	} {
		request := httptest.NewRequest("POST", "/api/action", strings.NewReader(`{"action":"up"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", tc.origin)
		request.Header.Set("Sec-Fetch-Site", tc.site)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != tc.status {
			t.Fatalf("origin %q / site %q: status = %d", tc.origin, tc.site, response.Code)
		}
		if response.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("CORS unexpectedly enabled")
		}
	}
}

func TestWebQueueBoundedConcurrentAndClosing(t *testing.T) {
	var invalidations atomic.Int32
	web := &webUI{commands: make(chan webCommand, webQueueSize), invalidate: func() { invalidations.Add(1) }}
	handler := web.handler()
	var accepted atomic.Int32
	var workers sync.WaitGroup
	for range webQueueSize * 2 {
		workers.Go(func() {
			response := webRequest(handler, "POST", "/api/action", `{"action":"up"}`)
			switch response.Code {
			case http.StatusAccepted:
				accepted.Add(1)
			case http.StatusServiceUnavailable:
			default:
				t.Errorf("unexpected response %d", response.Code)
			}
		})
	}
	workers.Wait()
	if accepted.Load() != webQueueSize || invalidations.Load() != webQueueSize || len(web.commands) != webQueueSize {
		t.Fatal("queue not bounded or accepted commands did not invalidate")
	}
	web.mu.Lock()
	web.closing = true
	web.mu.Unlock()
	<-web.commands
	if response := webRequest(handler, "POST", "/api/action", `{"action":"up"}`); response.Code != http.StatusServiceUnavailable {
		t.Fatal("command accepted after closing")
	}
}

func TestWebListenerOptInBindFailureAndClose(t *testing.T) {
	a := New(nil, nil, &Config{WebUIAddr: "127.0.0.1:0"}, nil, nil)
	defer a.Close()
	if a.webUI != nil {
		t.Fatal("New implicitly started a server")
	}
	if err := a.startWebUI("  "); err != nil || a.webUI != nil {
		t.Fatal("empty address did not disable web UI")
	}
	if err := a.startWebUI("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get("http://" + a.webUI.listener.Addr().String() + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	var status webStatus
	decodeErr := json.NewDecoder(response.Body).Decode(&status)
	_ = response.Body.Close()
	if decodeErr != nil || status.State != PlayerStatusIdle {
		t.Fatalf("initial status = %+v, error %v", status, decodeErr)
	}
	other := New(nil, nil, nil, nil, nil)
	defer other.Close()
	if err := other.startWebUI(a.webUI.listener.Addr().String()); err == nil || other.webUI != nil {
		t.Fatal("bind failure ignored")
	}
	a.Close()
	a.Close()
	select {
	case <-a.webUI.serveDone:
	default:
		t.Fatal("server still running after close")
	}
	if response := webRequest(a.webUI.handler(), "POST", "/api/action", `{"action":"up"}`); response.Code != http.StatusServiceUnavailable {
		t.Fatal("closed server queue accepted a command")
	}
}

func TestHeadlessWebNavigationAndPopupPriority(t *testing.T) {
	f := newHeadlessFixture(t)
	if err := f.app.startWebUI("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.app.Close)
	handler := f.app.webUI.handler()
	remote := func(action string) {
		t.Helper()
		response := webRequest(handler, "POST", "/api/action", `{"action":"`+action+`"}`)
		if response.Code != http.StatusAccepted {
			t.Fatal(response.Body.String())
		}
		if err := f.driver.Frame(); err != nil {
			t.Fatal(err)
		}
	}
	response := webRequest(handler, "POST", "/api/action", `{"action":"right"}`)
	if response.Code != http.StatusAccepted || !f.app.browse.nav.isAtRoot() {
		t.Fatal("handler mutated navigation instead of queuing")
	}
	if err := f.driver.Frame(); err != nil {
		t.Fatal(err)
	}
	if f.app.browse.nav.isAtRoot() {
		t.Fatal("event loop did not dispatch remote navigation")
	}
	remote("home")
	remote("left")
	remote("down")
	if f.app.browse.focus != browseFocusModeRail || f.app.browse.library.mode != ListingModeDateAdded {
		t.Fatal("remote mode rail navigation changed")
	}
	remote("right") // mode rail -> content
	remote("right") // home -> movies
	remote("ok")    // terminal: OK must not open details
	if f.app.detail != nil {
		t.Fatal("remote OK opened a browse terminal")
	}
	remote("right")
	if f.app.detail == nil {
		t.Fatal("remote Right did not open details")
	}
	detail := f.app.detail
	f.app.subPopup = &SubtitlePopup{}
	remote("back")
	if f.app.subPopup != nil || f.app.detail != detail {
		t.Fatal("remote input bypassed popup priority")
	}
	remote("back")
	if f.app.detail != nil {
		t.Fatal("remote Back did not leave details")
	}
	remote("home")
	remote("section")
	remote("right")
	if f.app.detail == nil || f.app.detail.show == nil || f.app.detail.show.focus != focusTabs {
		t.Fatal("remote show navigation did not focus season tabs")
	}
	remote("ok")
	if f.app.detail.show.focus != focusEpisodes {
		t.Fatal("remote OK did not enter episodes")
	}
}

func TestHeadlessWebURLPlaybackAndStop(t *testing.T) {
	f := newHeadlessFixture(t)
	dir := t.TempDir()
	argvPath := filepath.Join(dir, "argv")
	configPath := filepath.Join(dir, "include")
	envPath := filepath.Join(dir, "environment")
	readyPath := filepath.Join(dir, "ready")
	script := filepath.Join(dir, "fake-mpv")
	body := `#!/bin/sh
trap 'exit 0' TERM
printf '%s\n' "$@" > "$PEPE_WEB_ARGV"
env > "$PEPE_WEB_ENV"
for arg do
  case "$arg" in --include=*) cat "${arg#--include=}" > "$PEPE_WEB_INCLUDE" ;; esac
done
: > "$PEPE_WEB_READY"
while :; do sleep 0.05; done
`
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PEPE_WEB_ARGV", argvPath)
	t.Setenv("PEPE_WEB_ENV", envPath)
	t.Setenv("PEPE_WEB_INCLUDE", configPath)
	t.Setenv("PEPE_WEB_READY", readyPath)
	t.Setenv("POLOCHON_TOKEN", "private-test-token")
	f.app.mpvPath = script
	f.app.playerFullscreen = true
	if err := f.app.startWebUI("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		f.app.pendingPlayback = nil
		f.app.requestPlayerStop()
		if done := f.app.playerDone; done != nil {
			<-done
		}
		f.app.Close()
	})
	handler := f.app.webUI.handler()
	videoURL := "https://media.example/video?signature=private-signed-value"
	request, _ := json.Marshal(map[string]string{"url": videoURL})
	response := webRequest(handler, "POST", "/api/play", string(request))
	if response.Code != http.StatusAccepted || f.app.playerStatus != PlayerStatusIdle {
		t.Fatal("HTTP URL request was not queued")
	}
	if err := f.driver.Frame(); err != nil {
		t.Fatal(err)
	}
	if f.app.playerStatus != PlayerStatusRunning || !headlessHasLabel(f.driver.Tree(), "Now playing from Web UI") {
		t.Fatalf("URL did not launch: %s", f.app.playerError)
	}
	waitForCache(t, func() bool { _, err := os.Stat(readyPath); return err == nil })
	args, err := os.ReadFile(argvPath)
	if err != nil || !strings.Contains(string(args), videoURL) || !strings.Contains(string(args), "--fs") {
		t.Fatalf("URL arguments = %s, error %v", args, err)
	}
	include, err := os.ReadFile(configPath)
	if err != nil || len(include) != 0 {
		t.Fatalf("URL include = %q, error %v", include, err)
	}
	environment, err := os.ReadFile(envPath)
	if err != nil || strings.Contains(string(environment), "private-test-token") {
		t.Fatal("URL player inherited token")
	}
	status := webRequest(handler, "GET", "/api/status", "")
	if !strings.Contains(status.Body.String(), `"source":"web"`) || strings.Contains(status.Body.String(), "private-signed-value") || strings.Contains(status.Body.String(), "mpv.sock") {
		t.Fatalf("unsafe or incorrect status: %s", status.Body.String())
	}
	// Stop cancels a queued replacement as well as the active process.
	if response := webRequest(handler, "POST", "/api/play", `{"url":"https://media.example/next"}`); response.Code != 202 {
		t.Fatal(response.Body.String())
	}
	if response := webRequest(handler, "POST", "/api/player", `{"command":"stop"}`); response.Code != 202 {
		t.Fatal(response.Body.String())
	}
	if err := f.driver.Frame(); err != nil {
		t.Fatal(err)
	}
	if !f.app.playerStopping || f.app.pendingPlayback != nil || !headlessHasLabel(f.driver.Tree(), "Stopping playback") {
		t.Fatal("Stop did not cancel replacement")
	}
	waitForCache(t, func() bool { return len(f.app.playerDone) == 1 })
	if err := f.driver.Frame(); err != nil {
		t.Fatal(err)
	}
	if f.app.playerStatus != PlayerStatusIdle || f.app.playerFromWeb || f.app.playerStopping {
		t.Fatalf("stop completion = %s, %s", f.app.playerStatus, f.app.playerError)
	}
	f.app.mpvPath = filepath.Join(dir, "missing-mpv")
	webRequest(handler, "POST", "/api/play", string(request))
	if err := f.driver.Frame(); err != nil {
		t.Fatal(err)
	}
	if f.app.playerStatus != PlayerStatusError {
		t.Fatal("launch failure not reported")
	}
	status = webRequest(handler, "GET", "/api/status", "")
	if !strings.Contains(status.Body.String(), "Playback failed") || strings.Contains(status.Body.String(), "missing-mpv") {
		t.Fatal("launch error missing or leaked runtime details")
	}
}

func TestWebQueuedControlsDoNotTargetReplacement(t *testing.T) {
	a := &App{webUI: &webUI{commands: make(chan webCommand, webQueueSize)}}
	a.playerStatus = PlayerStatusRunning
	a.playerSession = 1
	a.publishWebStatus()
	if !a.webUI.enqueue(webCommand{control: playerControlStop}) {
		t.Fatal("enqueue failed")
	}
	a.playerSession = 2
	a.playerStop = make(chan struct{})
	a.drainWebCommands()
	if a.playerStopping || a.webCommandError != "Playback changed; try again." {
		t.Fatal("stale Stop affected replacement")
	}
}

func TestWebStatusDoesNotExposePlayerErrorDetails(t *testing.T) {
	a := &App{webUI: &webUI{commands: make(chan webCommand, webQueueSize)}}
	a.playerStatus = PlayerStatusError
	a.playerError = "https://private/video?token=secret at /private/mpv.sock"
	a.publishWebStatus()
	response := webRequest(a.webUI.handler(), "GET", "/api/status", "")
	data, err := io.ReadAll(response.Body)
	if err != nil || strings.Contains(string(data), "secret") || strings.Contains(string(data), "/private") {
		t.Fatal("player details leaked")
	}
}
