package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

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

func Serve(ctx context.Context, r io.Reader, w io.Writer, drv *driver.Driver, repoRoot string) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	out := bufio.NewWriter(w)
	defer out.Flush()
	shotNumber := uint64(0)

	write := func(resp response) error {
		if err := json.NewEncoder(out).Encode(resp); err != nil {
			return err
		}
		return out.Flush()
	}

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			if write(response{Error: fmt.Sprintf("invalid JSON request: %v", err)}) != nil {
				return err
			}
			continue
		}
		result, shutdown, err := dispatch(req, drv, repoRoot, &shotNumber)
		if err != nil {
			if err := write(response{ID: req.ID, Error: err.Error()}); err != nil {
				return err
			}
			continue
		}
		if err := write(response{ID: req.ID, Result: result}); err != nil {
			return err
		}
		if shutdown {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read request: %w", err)
	}
	return nil
}

func dispatch(req request, drv *driver.Driver, repoRoot string, shotNumber *uint64) (any, bool, error) {
	params := req.Params
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	switch req.Method {
	case "tree":
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
		(*shotNumber)++
		screenshotDir := os.Getenv("GIO_CONTROL_SCREENSHOT_DIR")
		if screenshotDir == "" {
			screenshotDir = filepath.Join(".pi", "tmp", "screenshots")
		}
		if !filepath.IsAbs(screenshotDir) {
			screenshotDir = filepath.Join(repoRoot, screenshotDir)
		}
		path := filepath.Join(screenshotDir, fmt.Sprintf("frame-%06d.png", *shotNumber))
		if err := drv.Screenshot(path); err != nil {
			return nil, false, err
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			rel = path
		}
		size := drv.Size()
		return map[string]any{"path": rel, "width": size.X, "height": size.Y}, false, nil
	case "shutdown":
		return map[string]any{"ok": true}, true, nil
	default:
		return nil, false, fmt.Errorf("unknown method %q", req.Method)
	}
}
