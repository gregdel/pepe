package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"
)

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
