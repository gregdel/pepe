// Package driver provides a sequential, headless Gio UI driver.
package driver

import (
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"time"

	"gioui.org/f32"
	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

func init() {
	if os.Getenv("EGL_PLATFORM") == "" {
		os.Setenv("EGL_PLATFORM", "surfaceless")
	}
	if os.Getenv("LIBGL_ALWAYS_SOFTWARE") == "" {
		os.Setenv("LIBGL_ALWAYS_SOFTWARE", "1")
	}
}

type LayoutFunc func(layout.Context) layout.Dimensions

type Config struct {
	Width   int
	Height  int
	PxPerDp float32
	PxPerSp float32
}

type Driver struct {
	window      *headless.Window
	router      input.Router
	ops         op.Ops
	size        image.Point
	metric      unit.Metric
	layout      LayoutFunc
	frameNumber uint64
	closed      bool
}

func New(cfg Config, layoutFunc LayoutFunc) (*Driver, error) {
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, fmt.Errorf("viewport dimensions must be positive, got %dx%d", cfg.Width, cfg.Height)
	}
	if cfg.PxPerDp <= 0 || cfg.PxPerSp <= 0 {
		return nil, fmt.Errorf("pixel scales must be positive, got dp=%v sp=%v", cfg.PxPerDp, cfg.PxPerSp)
	}
	if layoutFunc == nil {
		return nil, errors.New("layout function is required")
	}
	window, err := headless.NewWindow(cfg.Width, cfg.Height)
	if err != nil {
		return nil, fmt.Errorf("create headless window: %w", err)
	}
	d := &Driver{
		window: window,
		size:   image.Pt(cfg.Width, cfg.Height),
		metric: unit.Metric{PxPerDp: cfg.PxPerDp, PxPerSp: cfg.PxPerSp},
		layout: layoutFunc,
	}
	if err := d.Frame(); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

func (d *Driver) Close() {
	if d.closed {
		return
	}
	d.closed = true
	if d.window != nil {
		d.window.Release()
		d.window = nil
	}
}

func (d *Driver) Frame() error {
	if d.closed || d.window == nil {
		return errors.New("UI driver is closed")
	}
	d.ops.Reset()
	gtx := layout.Context{
		Ops:         &d.ops,
		Source:      d.router.Source(),
		Now:         time.Now(),
		Metric:      d.metric,
		Constraints: layout.Exact(d.size),
	}
	d.layout(gtx)
	if err := d.window.Frame(&d.ops); err != nil {
		return fmt.Errorf("render frame: %w", err)
	}
	d.router.Frame(&d.ops)
	d.frameNumber++
	return nil
}

func (d *Driver) FrameNumber() uint64 { return d.frameNumber }

func (d *Driver) Size() image.Point { return d.size }

func (d *Driver) Tree() []Node {
	if d.closed {
		return nil
	}
	return normalizeTree(d.router.AppendSemantics(nil))
}

type ClickTarget struct {
	ID          *uint64
	Label       string
	Occurrence  int
	X, Y        float32
	Coordinates bool
	Button      string
}

func (d *Driver) Click(target ClickTarget) error {
	if d.closed {
		return errors.New("UI driver is closed")
	}
	position, err := d.clickPosition(target)
	if err != nil {
		return err
	}
	button, err := clickButton(target.Button)
	if err != nil {
		return err
	}
	d.router.Queue(
		pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, PointerID: 1, Position: position, Buttons: 0},
		pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, PointerID: 1, Position: position, Buttons: button},
		pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, PointerID: 1, Position: position, Buttons: 0},
	)
	return d.Frame()
}

func (d *Driver) clickPosition(target ClickTarget) (f32.Point, error) {
	if target.Coordinates {
		if target.X < 0 || target.Y < 0 || target.X >= float32(d.size.X) || target.Y >= float32(d.size.Y) {
			return f32.Point{}, fmt.Errorf("coordinates (%v, %v) are outside viewport %dx%d", target.X, target.Y, d.size.X, d.size.Y)
		}
		return f32.Pt(target.X, target.Y), nil
	}
	var matches []Node
	if target.ID != nil {
		if node, ok := findNode(d.Tree(), *target.ID); ok {
			matches = []Node{node}
		} else {
			return f32.Point{}, fmt.Errorf("semantic node %d does not exist", *target.ID)
		}
	} else if target.Label != "" {
		findLabel(d.Tree(), target.Label, &matches)
		if len(matches) == 0 {
			return f32.Point{}, fmt.Errorf("semantic label %q does not exist", target.Label)
		}
		if target.Occurrence == 0 && len(matches) != 1 {
			return f32.Point{}, fmt.Errorf("semantic label %q matched %d nodes; specify occurrence", target.Label, len(matches))
		}
		occurrence := target.Occurrence
		if occurrence == 0 {
			occurrence = 1
		}
		if occurrence < 1 || occurrence > len(matches) {
			return f32.Point{}, fmt.Errorf("semantic label %q occurrence %d is out of range (1-%d)", target.Label, occurrence, len(matches))
		}
		matches = matches[occurrence-1 : occurrence]
	} else {
		return f32.Point{}, errors.New("click target requires id, label, or coordinates")
	}
	node := matches[0]
	if node.Disabled {
		name := node.Label
		if name == "" {
			name = fmt.Sprintf("%d", node.ID)
		}
		return f32.Point{}, fmt.Errorf("semantic node %q is disabled", name)
	}
	if node.Bounds.MinX >= node.Bounds.MaxX || node.Bounds.MinY >= node.Bounds.MaxY {
		return f32.Point{}, fmt.Errorf("semantic node %d has empty bounds", node.ID)
	}
	return f32.Pt(float32((node.Bounds.MinX+node.Bounds.MaxX)/2), float32((node.Bounds.MinY+node.Bounds.MaxY)/2)), nil
}

func (d *Driver) Type(text string) error {
	if d.closed {
		return errors.New("UI driver is closed")
	}
	if _, ok := d.router.TextInputHint(); !ok {
		return errors.New("cannot type: no focused text editor")
	}
	state := d.router.EditorState()
	d.router.Queue(key.EditEvent{Range: state.Selection.Range, Text: text})
	return d.Frame()
}

func (d *Driver) Key(name string, modifiers []string) error {
	if d.closed {
		return errors.New("UI driver is closed")
	}
	keyName, err := keyName(name)
	if err != nil {
		return err
	}
	mods, err := keyModifiers(modifiers)
	if err != nil {
		return err
	}
	press := key.Event{Name: keyName, Modifiers: mods, State: key.Press}
	if keyName == key.NameTab {
		d.router.Queue(input.SystemEvent{Event: press})
		if err := d.renderWithFallbackFocus(keyName, mods); err != nil {
			return err
		}
		return nil
	}
	d.router.Queue(press, key.Event{Name: keyName, Modifiers: mods, State: key.Release})
	return d.Frame()
}

func (d *Driver) renderWithFallbackFocus(name key.Name, mods key.Modifiers) error {
	// Explicitly named SystemEvents are delivered to handlers that ask for the
	// key. If the application did not consume Tab, mirror app.Window's focus
	// fallback before completing the frame.
	d.ops.Reset()
	gtx := layout.Context{Ops: &d.ops, Source: d.router.Source(), Now: time.Now(), Metric: d.metric, Constraints: layout.Exact(d.size)}
	d.layout(gtx)
	if _, handled := d.router.Event(key.Filter{Name: name, Optional: mods}); !handled {
		if mods&key.ModShift != 0 {
			d.router.MoveFocus(key.FocusBackward)
		} else {
			d.router.MoveFocus(key.FocusForward)
		}
	}
	if err := d.window.Frame(&d.ops); err != nil {
		return fmt.Errorf("render frame: %w", err)
	}
	d.router.Frame(&d.ops)
	d.frameNumber++
	return nil
}

func (d *Driver) Resize(width, height int) error {
	if d.closed {
		return errors.New("UI driver is closed")
	}
	if width <= 0 || height <= 0 {
		return fmt.Errorf("viewport dimensions must be positive, got %dx%d", width, height)
	}
	// Gio's EGL headless backend shares EGL_DEFAULT_DISPLAY between windows;
	// releasing the old window after creating the new one would terminate that
	// shared display and invalidate the replacement. Release first, then create
	// the replacement. A creation failure leaves the driver closed rather than
	// retaining an invalid rendering window.
	old := d.window
	old.Release()
	d.window = nil
	newWindow, err := headless.NewWindow(width, height)
	if err != nil {
		d.closed = true
		return fmt.Errorf("create resized headless window: %w", err)
	}
	d.window = newWindow
	d.size = image.Pt(width, height)
	return d.Frame()
}

func (d *Driver) Screenshot(path string) error {
	if d.closed || d.window == nil {
		return errors.New("UI driver is closed")
	}
	if path == "" {
		return errors.New("screenshot path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create screenshot directory: %w", err)
	}
	img := image.NewRGBA(image.Rectangle{Max: d.size})
	if err := d.window.Screenshot(img); err != nil {
		return fmt.Errorf("capture screenshot: %w", err)
	}
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create screenshot: %w", err)
	}
	if err := png.Encode(file, img); err != nil {
		_ = file.Close()
		return fmt.Errorf("encode screenshot: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close screenshot: %w", err)
	}
	return nil
}

func clickButton(name string) (pointer.Buttons, error) {
	switch name {
	case "", "primary":
		return pointer.ButtonPrimary, nil
	case "secondary":
		return pointer.ButtonSecondary, nil
	case "tertiary":
		return pointer.ButtonTertiary, nil
	default:
		return 0, fmt.Errorf("unknown pointer button %q", name)
	}
}

func keyModifiers(names []string) (key.Modifiers, error) {
	var modifiers key.Modifiers
	for _, name := range names {
		switch name {
		case "Ctrl":
			modifiers |= key.ModCtrl
		case "Shift":
			modifiers |= key.ModShift
		case "Alt":
			modifiers |= key.ModAlt
		case "Super":
			modifiers |= key.ModSuper
		default:
			return 0, fmt.Errorf("unknown key modifier %q", name)
		}
	}
	return modifiers, nil
}

func keyName(name string) (key.Name, error) {
	names := map[string]key.Name{
		"Return": key.NameReturn, "Enter": key.NameEnter, "Escape": key.NameEscape,
		"Tab": key.NameTab, "Space": key.NameSpace, "Backspace": key.NameDeleteBackward,
		"Delete": key.NameDeleteForward, "LeftArrow": key.NameLeftArrow, "RightArrow": key.NameRightArrow,
		"UpArrow": key.NameUpArrow, "DownArrow": key.NameDownArrow, "Home": key.NameHome,
		"End": key.NameEnd, "PageUp": key.NamePageUp, "PageDown": key.NamePageDown,
		"F1": key.NameF1, "F2": key.NameF2, "F3": key.NameF3, "F4": key.NameF4,
		"F5": key.NameF5, "F6": key.NameF6, "F7": key.NameF7, "F8": key.NameF8,
		"F9": key.NameF9, "F10": key.NameF10, "F11": key.NameF11, "F12": key.NameF12,
	}
	if value, ok := names[name]; ok {
		return value, nil
	}
	return "", fmt.Errorf("unknown key name %q", name)
}
