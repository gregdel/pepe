package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"gioui.org/app"
)

// ipcEvent is sent from the IPC goroutine to the Gio event loop.
type ipcEvent struct {
	mediaKey string
	pos      float64 // -1 means eof (clear resume position)
}

// startIPC launches ipcLoop in a goroutine and returns a buffered channel (cap 32).
func startIPC(sockPath, mediaKey string, w *app.Window) chan ipcEvent {
	ch := make(chan ipcEvent, 32)
	go ipcLoop(sockPath, mediaKey, ch, w)
	return ch
}

// mpvEvent is the JSON event structure sent by mpv over IPC.
type mpvEvent struct {
	Event string      `json:"event"`
	Name  string      `json:"name"`
	Data  any `json:"data"`
}

// ipcLoop connects to the mpv IPC socket and processes events.
func ipcLoop(sockPath, mediaKey string, ch chan ipcEvent, w *app.Window) {
	var conn net.Conn
	for range 30 {
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
			if pos, ok := ev.Data.(float64); ok && pos > 30.0 {
				ch <- ipcEvent{mediaKey, pos}
				w.Invalidate()
			}
		case "eof-reached":
			if reached, ok := ev.Data.(bool); ok && reached {
				ch <- ipcEvent{mediaKey, -1}
				w.Invalidate()
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
