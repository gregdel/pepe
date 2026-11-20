package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	logBufferSize     = 500
	logMessageMaxSize = 1024
)

// logEntry is one redacted log line. Seq increases monotonically so readers
// can request only newer entries.
type logEntry struct {
	Seq     uint64    `json:"seq"`
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Source  string    `json:"source"`
	Message string    `json:"message"`
}

func (e logEntry) String() string {
	return e.Time.Format("2006-01-02 15:04:05") + " " + e.Level + " " + e.Source + ": " + e.Message
}

// LogBuffer keeps the latest log entries in memory for the TV and web UIs.
// Both are reachable without authentication, so entries are redacted before
// they are stored.
type LogBuffer struct {
	mu      sync.RWMutex
	entries []logEntry // ring of at most logBufferSize entries
	start   int
	next    uint64
	redact  func(string) string
	changed atomic.Bool
	notify  atomic.Pointer[func()]
}

// NewLogBuffer returns an empty buffer that removes the given secrets, URL
// credentials, query strings and authentication header values from entries.
func NewLogBuffer(secrets ...string) *LogBuffer {
	return &LogBuffer{redact: newRedactor(secrets...), next: 1}
}

// add stores an already redacted message.
func (b *LogBuffer) add(level slog.Level, source, message string) {
	message = strings.TrimSpace(message)
	if len(message) > logMessageMaxSize {
		message = strings.ToValidUTF8(message[:logMessageMaxSize], "") + "…"
	}
	entry := logEntry{Time: time.Now(), Level: level.String(), Source: source, Message: message}
	b.mu.Lock()
	entry.Seq = b.next
	b.next++
	if len(b.entries) < logBufferSize {
		b.entries = append(b.entries, entry)
	} else {
		b.entries[b.start] = entry
		b.start = (b.start + 1) % logBufferSize
	}
	b.mu.Unlock()
	// Wake the UI once until it consumes the change, so a chatty player
	// cannot request more than one redraw per frame.
	if !b.changed.Swap(true) {
		if notify := b.notify.Load(); notify != nil {
			(*notify)()
		}
	}
}

// since returns entries newer than seq and whether older unseen entries were
// already dropped from the ring.
func (b *LogBuffer) since(seq uint64) ([]logEntry, uint64, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var entries []logEntry
	for i := range b.entries {
		entry := b.entries[(b.start+i)%len(b.entries)]
		if entry.Seq > seq {
			entries = append(entries, entry)
		}
	}
	truncated := len(b.entries) > 0 && b.entries[b.start].Seq > seq+1
	return entries, b.next - 1, truncated
}

func (b *LogBuffer) setNotify(notify func()) {
	if notify == nil {
		b.notify.Store(nil)
		return
	}
	b.notify.Store(&notify)
}

// consumeChange reports whether entries were added since the last call.
func (b *LogBuffer) consumeChange() bool {
	return b.changed.Swap(false)
}

// redactValue redacts a value, including every value nested in groups.
func (b *LogBuffer) redactValue(value slog.Value) slog.Value {
	value = value.Resolve()
	if value.Kind() != slog.KindGroup {
		return slog.StringValue(b.redact(value.String()))
	}
	attrs := value.Group()
	redacted := make([]slog.Attr, len(attrs))
	for i, attr := range attrs {
		redacted[i] = slog.Attr{Key: attr.Key, Value: b.redactValue(attr.Value)}
	}
	return slog.GroupValue(redacted...)
}

// Handler returns a slog handler that stores Info and higher records in the
// buffer and forwards the same redacted records to next, when not nil.
func (b *LogBuffer) Handler(next slog.Handler) slog.Handler {
	return &logBufferHandler{buffer: b, next: next}
}

type groupedAttr struct {
	attr  slog.Attr
	group string
}

type logBufferHandler struct {
	buffer *LogBuffer
	next   slog.Handler
	attrs  []groupedAttr // with the group that applied when they were added
	group  string
}

func (h *logBufferHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= slog.LevelInfo || h.next != nil && h.next.Enabled(ctx, level)
}

// Handle redacts the message and every attribute value once, then stores and
// forwards that redacted record. A "source" attribute names the log source
// (default "pepe"); the logger's "app" attribute is implied.
func (h *logBufferHandler) Handle(ctx context.Context, record slog.Record) error {
	redacted := slog.NewRecord(record.Time, record.Level, h.buffer.redact(record.Message), record.PC)
	source := "pepe"
	var text strings.Builder
	text.WriteString(redacted.Message)
	add := func(attr slog.Attr, group string) {
		attr.Value = attr.Value.Resolve()
		if attr.Equal(slog.Attr{}) {
			return
		}
		attr.Value = h.buffer.redactValue(attr.Value)
		if group != "" {
			attr.Key = group + "." + attr.Key
		}
		redacted.AddAttrs(attr)
		switch attr.Key {
		case "source":
			source = attr.Value.String()
		case "app":
		default:
			fmt.Fprintf(&text, " %s=%s", attr.Key, attr.Value)
		}
	}
	for _, attr := range h.attrs {
		add(attr.attr, attr.group)
	}
	record.Attrs(func(attr slog.Attr) bool {
		add(attr, h.group)
		return true
	})
	if record.Level >= slog.LevelInfo {
		h.buffer.add(record.Level, source, text.String())
	}
	if h.next != nil && h.next.Enabled(ctx, record.Level) {
		return h.next.Handle(ctx, redacted)
	}
	return nil
}

func (h *logBufferHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.attrs = clone.attrs[:len(clone.attrs):len(clone.attrs)]
	for _, attr := range attrs {
		clone.attrs = append(clone.attrs, groupedAttr{attr, h.group})
	}
	return &clone
}

func (h *logBufferHandler) WithGroup(name string) slog.Handler {
	clone := *h
	if clone.group != "" {
		name = clone.group + "." + name
	}
	clone.group = name
	return &clone
}

var (
	logURLPattern    = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s"'<>]+`)
	logHeaderPattern = regexp.MustCompile(`(?i)\b(authorization|proxy-authorization|cookie|set-cookie|x-auth-token|x-api-key|api-key)(\s*[:=]\s*)("[^"]*"|[^\r\n,"]+)`)
)

// newRedactor removes secrets rather than masking them: logs are shown
// without authentication. URL queries and fragments are dropped entirely
// because signed links and tokens hide there under arbitrary names.
func newRedactor(secrets ...string) func(string) string {
	var pairs []string
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if len(secret) < 4 {
			continue // never rewrite ordinary short words
		}
		for _, form := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret)} {
			pairs = append(pairs, form, "…")
		}
	}
	replacer := strings.NewReplacer(pairs...)
	return func(text string) string {
		if len(pairs) > 0 {
			text = replacer.Replace(text)
		}
		text = logHeaderPattern.ReplaceAllString(text, "$1$2…")
		return logURLPattern.ReplaceAllStringFunc(text, redactURL)
	}
}

func redactURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		if i := strings.IndexAny(raw, "?#"); i >= 0 {
			return raw[:i] + "?…"
		}
		return raw
	}
	hidden := parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != ""
	parsed.User, parsed.RawQuery, parsed.ForceQuery, parsed.Fragment, parsed.RawFragment = nil, "", false, "", ""
	if hidden {
		return parsed.String() + "?…"
	}
	return parsed.String()
}
