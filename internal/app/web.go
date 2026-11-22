package app

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed web/index.html
var webHTML []byte

//go:embed web/remote.css
var webCSS []byte

//go:embed web/remote.js
var webJS []byte

//go:embed web/logs.html
var webLogsHTML []byte

//go:embed web/logs.js
var webLogsJS []byte

const webQueueSize = 32

type webCommand struct {
	action  inputAction
	url     string
	control playerControl
	session uint64
}

type webStatus struct {
	State          PlayerStatus `json:"state"`
	Source         string       `json:"source"`
	Title          string       `json:"title"`
	Stopping       bool         `json:"stopping"`
	Error          string       `json:"error"`
	CommandError   string       `json:"command_error"`
	QueuedCommands int          `json:"queued_commands"`
	session        uint64       // transport-only identity; never exposed to the browser
}

// webUI owns only transport state. Handlers never access mutable App state.
type webUI struct {
	commands   chan webCommand
	logs       *LogBuffer // guarded by mu; concurrency-safe, entries are already redacted
	invalidate func()
	server     *http.Server
	listener   net.Listener
	serveDone  chan struct{}
	closeOnce  sync.Once
	mu         sync.RWMutex
	closing    bool
	status     webStatus
}

func (web *webUI) logBuffer() *LogBuffer {
	web.mu.RLock()
	defer web.mu.RUnlock()
	return web.logs
}

func (web *webUI) setLogs(logs *LogBuffer) {
	web.mu.Lock()
	web.logs = logs
	web.mu.Unlock()
}

func (a *App) startWebUI(addr string) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil
	}
	if a.webUI != nil {
		return fmt.Errorf("web UI is already started")
	}
	if err := (&Config{WebUIAddr: addr}).ValidateWebUI(); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen for web UI: %w", err)
	}
	web := &webUI{
		commands: make(chan webCommand, webQueueSize), invalidate: a.invalidate,
		logs:     a.logs,
		listener: listener, serveDone: make(chan struct{}),
	}
	web.server = &http.Server{
		Handler: web.handler(), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second,
		MaxHeaderBytes: 16 << 10,
	}
	a.webUI = web
	a.publishWebStatus()
	logger := a.logger()
	logger.Warn("web remote enabled without authentication; use only on trusted networks", "address", listener.Addr().String())
	go func() {
		defer close(web.serveDone)
		if err := web.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("web UI server failed", "error", err)
		}
	}()
	return nil
}

func (web *webUI) close() {
	web.closeOnce.Do(func() {
		web.mu.Lock()
		web.closing = true
		web.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := web.server.Shutdown(ctx); err != nil {
			_ = web.server.Close()
		}
		<-web.serveDone
	})
}

func (web *webUI) enqueue(command webCommand) bool {
	web.mu.RLock()
	if web.closing {
		web.mu.RUnlock()
		return false
	}
	if command.control != "" {
		command.session = web.status.session
	}
	select {
	case web.commands <- command:
		web.mu.RUnlock()
		if web.invalidate != nil {
			web.invalidate()
		}
		return true
	default:
		web.mu.RUnlock()
		return false
	}
}

// themedHTML fills a page's theme-color with colorBg.
func themedHTML(page []byte) []byte {
	return bytes.ReplaceAll(page, []byte("@COLOR_BG@"), []byte(cssColor(colorBg)))
}

func (web *webUI) handler() http.Handler {
	mux := http.NewServeMux()
	for path, asset := range map[string]struct {
		contentType string
		data        []byte
	}{
		"/{$}":        {"text/html; charset=utf-8", themedHTML(webHTML)},
		"/remote.css": {"text/css; charset=utf-8", append([]byte(themeCSS()), webCSS...)},
		"/remote.js":  {"text/javascript; charset=utf-8", webJS},
		"/logs":       {"text/html; charset=utf-8", themedHTML(webLogsHTML)},
		"/logs.js":    {"text/javascript; charset=utf-8", webLogsJS},
		"/roboto.ttf": {"font/ttf", embeddedRoboto},
	} {
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", asset.contentType)
			_, _ = w.Write(asset.data)
		})
	}
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		web.mu.RLock()
		status := web.status
		web.mu.RUnlock()
		status.QueuedCommands = len(web.commands)
		writeWebJSON(w, http.StatusOK, status)
	})
	mux.HandleFunc("GET /api/logs", func(w http.ResponseWriter, r *http.Request) {
		var after uint64
		if value := r.URL.Query().Get("after"); value != "" {
			var err error
			if after, err = strconv.ParseUint(value, 10, 64); err != nil {
				writeWebError(w, http.StatusBadRequest, "invalid log position")
				return
			}
		}
		var response struct {
			Entries   []logEntry `json:"entries"`
			Next      uint64     `json:"next"`
			Truncated bool       `json:"truncated"`
		}
		if logs := web.logBuffer(); logs != nil {
			response.Entries, response.Next, response.Truncated = logs.since(after)
		}
		if response.Entries == nil {
			response.Entries = []logEntry{}
		}
		writeWebJSON(w, http.StatusOK, response)
	})
	mux.HandleFunc("POST /api/action", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Action string `json:"action"`
		}
		if !decodeWebJSON(w, r, &request) {
			return
		}
		action, ok := webActions[request.Action]
		if !ok {
			writeWebError(w, http.StatusBadRequest, "unknown navigation action")
			return
		}
		web.accept(w, webCommand{action: action})
	})
	mux.HandleFunc("POST /api/play", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			URL string `json:"url"`
		}
		if !decodeWebJSON(w, r, &request) {
			return
		}
		videoURL, err := validatePlaybackURL(request.URL)
		if err != nil {
			writeWebError(w, http.StatusBadRequest, err.Error())
			return
		}
		web.accept(w, webCommand{url: videoURL})
	})
	mux.HandleFunc("POST /api/player", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Command playerControl `json:"command"`
		}
		if !decodeWebJSON(w, r, &request) {
			return
		}
		if !request.Command.valid() {
			writeWebError(w, http.StatusBadRequest, "unknown playback command")
			return
		}
		web.accept(w, webCommand{control: request.Command})
	})
	protected := http.NewCrossOriginProtection().Handler(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; font-src 'self'; connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		protected.ServeHTTP(w, r)
	})
}

var webActions = map[string]inputAction{
	"up": actionUp, "down": actionDown, "left": actionLeft, "right": actionRight,
	"ok": actionActivate, "back": actionBack, "home": actionHome,
	"section": actionToggleSection, "refresh": actionRefresh, "subtitles": actionSubtitles,
	"watched": actionToggleWatched, "quit": actionQuit,
}

func decodeWebJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeWebError(w, http.StatusUnsupportedMediaType, "application/json is required")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	err = decoder.Decode(destination)
	if err == nil {
		var extra any
		if trailing := decoder.Decode(&extra); trailing != io.EOF {
			err = trailing
			if err == nil {
				err = fmt.Errorf("multiple JSON values")
			}
		}
	}
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeWebError(w, http.StatusRequestEntityTooLarge, "request body is too large")
		} else {
			writeWebError(w, http.StatusBadRequest, "invalid JSON request")
		}
		return false
	}
	return true
}

func (web *webUI) accept(w http.ResponseWriter, command webCommand) {
	if !web.enqueue(command) {
		writeWebError(w, http.StatusServiceUnavailable, "remote is busy or closing; try again")
		return
	}
	writeWebJSON(w, http.StatusAccepted, map[string]bool{"queued": true})
}

func writeWebError(w http.ResponseWriter, status int, message string) {
	writeWebJSON(w, status, map[string]string{"error": message})
}

func writeWebJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// drainWebCommands runs under a.mu, serialized with physical input.
func (a *App) drainWebCommands() {
	if a.webUI == nil {
		return
	}
	for range webQueueSize {
		select {
		case command := <-a.webUI.commands:
			a.webCommandError = ""
			switch {
			case command.url != "":
				if err := a.launchURL(command.url); err != nil {
					a.webCommandError = err.Error()
				}
			case command.control != "":
				if command.session != a.playerSession {
					a.webCommandError = "Playback changed; try again."
				} else {
					a.handlePlayerControl(command.control)
				}
			default:
				if !a.handleRemoteAction(command.action) {
					a.dispatchAction(command.action)
				}
			}
		default:
			return
		}
	}
	if len(a.webUI.commands) > 0 && a.invalidate != nil {
		a.invalidate()
	}
}

func (a *App) publishWebStatus() {
	if a.webUI == nil {
		return
	}
	status := webStatus{State: a.playerStatus, Title: a.playerTitle,
		Stopping: a.playerStopping || a.pendingPlayback != nil, CommandError: a.webCommandError, session: a.playerSession}
	if a.playerStatus == PlayerStatusRunning {
		status.Source = "catalog"
		if a.playerFromWeb {
			status.Source = "web"
		}
	}
	if a.playerError != "" {
		// Player errors can contain private URLs or runtime paths.
		status.Error = "Playback failed; see Logs."
	}
	a.webUI.mu.Lock()
	a.webUI.status = status
	a.webUI.mu.Unlock()
}
