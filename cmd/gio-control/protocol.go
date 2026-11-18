package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gregdel/pepe/internal/driver"
)

type request struct {
	ID     uint64          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type response struct {
	ID     uint64 `json:"id"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

type clickParams struct {
	ID         *uint64  `json:"id"`
	Label      string   `json:"label"`
	Occurrence int      `json:"occurrence"`
	X          *float32 `json:"x"`
	Y          *float32 `json:"y"`
	Button     string   `json:"button"`
}

type keyParams struct {
	Name      string   `json:"name"`
	Modifiers []string `json:"modifiers"`
}

type resizeParams struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type typeParams struct {
	Text string `json:"text"`
}

// Serve answers newline-delimited requests from r on w until EOF or a
// shutdown request.
func Serve(ctx context.Context, r io.Reader, w io.Writer, drv *driver.Driver, repoRoot string) error {
	_, err := newServer(drv, repoRoot).serve(ctx, r, w)
	return err
}

// server holds protocol state shared by every connection to one driver.
type server struct {
	// mu is held while a request uses the driver, so a signal handler can
	// wait for it before tearing the driver down.
	mu         sync.Mutex
	drv        *driver.Driver
	repoRoot   string
	shotNumber uint64
	// idleTimeout bounds how long a socket connection may stay silent; zero
	// means defaultIdleTimeout. It does not apply to the stdio protocol.
	idleTimeout time.Duration
}

func newServer(drv *driver.Driver, repoRoot string) *server {
	return &server{drv: drv, repoRoot: repoRoot}
}

// serve handles one request stream and reports whether it ended with a
// shutdown request.
func (s *server) serve(ctx context.Context, r io.Reader, w io.Writer) (bool, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	out := bufio.NewWriter(w)

	write := func(resp response) error {
		if err := json.NewEncoder(out).Encode(resp); err != nil {
			return err
		}
		return out.Flush()
	}

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			if writeErr := write(response{Error: fmt.Sprintf("invalid JSON request: %v", err)}); writeErr != nil {
				return false, writeErr
			}
			continue
		}
		s.mu.Lock()
		result, shutdown, err := dispatch(req, s.drv, s.repoRoot, &s.shotNumber)
		s.mu.Unlock()
		if err != nil {
			if err := write(response{ID: req.ID, Error: err.Error()}); err != nil {
				return false, err
			}
			continue
		}
		if err := write(response{ID: req.ID, Result: result}); err != nil {
			return false, err
		}
		if shutdown {
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("read request: %w", err)
	}
	return false, nil
}

func dispatch(req request, drv *driver.Driver, repoRoot string, shotNumber *uint64) (any, bool, error) {
	params := req.Params
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	switch req.Method {
	case "tree":
		if err := drv.Refresh(); err != nil {
			return nil, false, err
		}
		return drv.Tree(), false, nil
	case "click":
		var p clickParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, false, fmt.Errorf("decode click params: %w", err)
		}
		if (p.X == nil) != (p.Y == nil) {
			return nil, false, fmt.Errorf("click coordinates x and y must be supplied together")
		}
		target := driver.ClickTarget{ID: p.ID, Label: p.Label, Occurrence: p.Occurrence, Button: p.Button}
		if p.X != nil {
			target.X, target.Y, target.Coordinates = *p.X, *p.Y, true
		}
		if err := drv.Click(target); err != nil {
			return nil, false, err
		}
		return map[string]any{"ok": true, "frame": drv.FrameNumber()}, false, nil
	case "type":
		var p typeParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, false, fmt.Errorf("decode type params: %w", err)
		}
		if err := drv.Type(p.Text); err != nil {
			return nil, false, err
		}
		return map[string]any{"ok": true, "frame": drv.FrameNumber()}, false, nil
	case "key":
		var p keyParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, false, fmt.Errorf("decode key params: %w", err)
		}
		if err := drv.Key(p.Name, p.Modifiers); err != nil {
			return nil, false, err
		}
		return map[string]any{"ok": true, "frame": drv.FrameNumber()}, false, nil
	case "resize":
		var p resizeParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, false, fmt.Errorf("decode resize params: %w", err)
		}
		if err := drv.Resize(p.Width, p.Height); err != nil {
			return nil, false, err
		}
		return map[string]any{"width": p.Width, "height": p.Height, "frame": drv.FrameNumber()}, false, nil
	case "screenshot":
		screenshotDir := os.Getenv("GIO_CONTROL_SCREENSHOT_DIR")
		if screenshotDir == "" {
			screenshotDir = filepath.Join(".pi", "tmp", "screenshots")
		}
		if !filepath.IsAbs(screenshotDir) {
			screenshotDir = filepath.Join(repoRoot, screenshotDir)
		}
		path, err := writeScreenshot(drv, screenshotDir, shotNumber)
		if err != nil {
			return nil, false, err
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			rel = path
		}
		size := drv.Size()
		return map[string]any{"path": rel, "absolutePath": path, "width": size.X, "height": size.Y}, false, nil
	case "size":
		size := drv.Size()
		return map[string]any{"width": size.X, "height": size.Y, "frame": drv.FrameNumber()}, false, nil
	case "shutdown":
		return map[string]any{"ok": true}, true, nil
	default:
		return nil, false, fmt.Errorf("unknown method %q", req.Method)
	}
}

// writeScreenshot writes the next free frame-NNNNNN.png in dir. Names are
// claimed exclusively, so neither a restart nor another server sharing dir
// overwrites an earlier screenshot. The first screenshot of a process continues
// after the highest existing frame, avoiding a probe per existing file.
func writeScreenshot(drv *driver.Driver, dir string, shotNumber *uint64) (string, error) {
	if *shotNumber == 0 {
		*shotNumber = lastFrameNumber(dir)
	}
	for {
		*shotNumber++
		path := filepath.Join(dir, fmt.Sprintf("frame-%06d.png", *shotNumber))
		err := drv.CreateScreenshot(path)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		return path, nil
	}
}

// lastFrameNumber returns the highest NNNNNN among frame-NNNNNN.png in dir, or
// 0 when there is none or dir cannot be read.
func lastFrameNumber(dir string) uint64 {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var last uint64
	for _, entry := range entries {
		digits, ok := strings.CutPrefix(entry.Name(), "frame-")
		if !ok {
			continue
		}
		if digits, ok = strings.CutSuffix(digits, ".png"); !ok {
			continue
		}
		if n, err := strconv.ParseUint(digits, 10, 64); err == nil {
			last = max(last, n)
		}
	}
	return last
}
