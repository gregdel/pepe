package app

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestLogRedaction(t *testing.T) {
	redact := newRedactor("s3cr3t/t0ken+x", "abc")
	for _, tc := range []struct{ in, want string }{
		{"token s3cr3t/t0ken+x in text", "token … in text"},
		{"encoded s3cr3t%2Ft0ken%2Bx", "encoded …"},
		{"short secrets are ignored: abc", "short secrets are ignored: abc"},
		{"open https://user:pw@cdn.example/v.mp4?sig=1&exp=2#t=3 now", "open https://cdn.example/v.mp4?… now"},
		{"https://www.youtube.com/watch?v=jNQXAC9IVRw", "https://www.youtube.com/watch?…"},
		{"plain https://arte.tv/fr/videos/123/ stays", "plain https://arte.tv/fr/videos/123/ stays"},
		{`http-header-fields="X-Auth-Token: anything"`, `http-header-fields="X-Auth-Token: …"`},
		{"Authorization: Bearer abc.def, next", "Authorization: …, next"},
		{"cookie=a=b", "cookie=…"},
		{"no secrets here", "no secrets here"},
	} {
		if got := redact(tc.in); got != tc.want {
			t.Errorf("redact(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestLogBufferRingAndSince(t *testing.T) {
	b := NewLogBuffer()
	for i := range logBufferSize + 10 {
		b.add(slog.LevelInfo, "pepe", fmt.Sprintf("line %d", i))
	}
	entries, next, truncated := b.since(0)
	if len(entries) != logBufferSize || next != logBufferSize+10 || !truncated {
		t.Fatalf("entries = %d, next = %d, truncated = %t", len(entries), next, truncated)
	}
	if entries[0].Seq != 11 || entries[0].Message != "line 10" || entries[len(entries)-1].Seq != next {
		t.Fatalf("ring order = %+v … %+v", entries[0], entries[len(entries)-1])
	}
	if entries, _, truncated := b.since(next - 2); len(entries) != 2 || truncated {
		t.Fatalf("incremental read = %d entries, truncated = %t", len(entries), truncated)
	}
	if entries, _, _ := b.since(next); len(entries) != 0 {
		t.Fatal("read past the newest entry returned entries")
	}
	b.add(slog.LevelError, "mpv", strings.Repeat("é", logMessageMaxSize))
	entries, _, _ = b.since(next)
	if got := entries[0].Message; len(got) > logMessageMaxSize+len("…") || !strings.HasSuffix(got, "…") || !strings.HasPrefix(got, "é") {
		t.Fatalf("long message was not capped on a rune boundary: %d bytes", len(got))
	}
}

func TestLogBufferHandler(t *testing.T) {
	b := NewLogBuffer("polochon-secret")
	var file bytes.Buffer
	woken := 0
	b.setNotify(func() { woken++ })
	logger := slog.New(b.Handler(slog.NewTextHandler(&file, nil))).With("app", "pepe")
	logger.Debug("hidden debug")
	logger.Info("player started", "url", "https://cdn.example/v.mp4?token=polochon-secret", "mode", "gui")
	logger.With("source", "mpv").Warn("[ytdl_hook] ERROR: failed")
	logger.WithGroup("player").Error("exited", "error", errors.New("Authorization: polochon-secret"))
	entries, _, _ := b.since(0)
	want := []string{
		"INFO pepe: player started url=https://cdn.example/v.mp4?… mode=gui",
		"WARN mpv: [ytdl_hook] ERROR: failed",
		"ERROR pepe: exited player.error=Authorization: …",
	}
	if len(entries) != len(want) {
		t.Fatalf("entries = %+v", entries)
	}
	for i, entry := range entries {
		if got := entry.String()[len("2006-01-02 15:04:05 "):]; got != want[i] {
			t.Errorf("entry %d = %q, want %q", i, got, want[i])
		}
	}
	if strings.Contains(file.String(), "polochon-secret") {
		t.Fatalf("file log leaked a secret: %s", file.String())
	}
	if !strings.Contains(file.String(), "url=https://cdn.example/v.mp4?…") {
		t.Fatalf("file log is not redacted the same way: %s", file.String())
	}
	if woken != 1 || !b.consumeChange() || b.consumeChange() {
		t.Fatalf("notifications = %d before the UI consumed the change", woken)
	}
}

func TestLogBufferRedactsGroupedValues(t *testing.T) {
	b := NewLogBuffer("polochon-secret")
	var file bytes.Buffer
	logger := slog.New(b.Handler(slog.NewTextHandler(&file, nil)))
	logger.Info("request", slog.Group("http", "url", "https://cdn.example/v.mp4?token=polochon-secret",
		slog.Group("auth", "header", "Authorization: polochon-secret")))
	entries, _, _ := b.since(0)
	if len(entries) != 1 || strings.Contains(entries[0].Message, "polochon-secret") {
		t.Fatalf("buffer leaked a grouped secret: %+v", entries)
	}
	if strings.Contains(file.String(), "polochon-secret") || !strings.Contains(file.String(), "http.url=https://cdn.example/v.mp4?…") {
		t.Fatalf("file log is not redacted: %s", file.String())
	}
}
