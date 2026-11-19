// Package driver provides a sequential, headless Gio UI driver.
package driver

import (
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
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
	"golang.org/x/image/draw"
)

func init() {
	if os.Getenv("EGL_PLATFORM") == "" {
		_ = os.Setenv("EGL_PLATFORM", "surfaceless")
	}
	if os.Getenv("LIBGL_ALWAYS_SOFTWARE") == "" {
		_ = os.Setenv("LIBGL_ALWAYS_SOFTWARE", "1")
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
	// hasNextFrame and nextFrame hold the earliest redraw the UI asked for,
	// like app.Window does: reading router.WakeupTime consumes it, so it is
	// kept here until a frame is rendered.
	hasNextFrame bool
	nextFrame    time.Time
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

// ErrRender matches a failure to render a frame. Input methods queue their
// input before rendering, so a Click, Key or Type error matching ErrRender
// means the input was delivered although its frame was not rendered.
var ErrRender = errors.New("render frame")

func (d *Driver) Frame() error {
	if d.closed || d.window == nil {
		return errors.New("UI driver is closed")
	}
	pending, at := d.hasNextFrame, d.nextFrame
	d.hasNextFrame = false
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
		d.hasNextFrame, d.nextFrame = pending, at
		return fmt.Errorf("%w: %w", ErrRender, err)
	}
	d.finishFrame()
	return nil
}

// Refresh updates asynchronous state and honors scheduled redraws before an
// observation. Bound the wait because loading UIs may invalidate indefinitely.
// Normal Gio windows schedule these frames themselves; headless windows do not.
func (d *Driver) Refresh() error {
	deadline := time.Now().Add(250 * time.Millisecond)
	if err := d.Frame(); err != nil {
		return err
	}
	for {
		at, pending := d.nextFrame, d.hasNextFrame
		if !pending {
			return nil
		}
		now := time.Now()
		if !now.Before(deadline) {
			return nil
		}
		if at.After(deadline) {
			return nil
		}
		delay := max(16*time.Millisecond, at.Sub(now))
		time.Sleep(min(delay, time.Until(deadline)))
		if err := d.Frame(); err != nil {
			return err
		}
	}
}

// Settle renders the extra frames an input action asks for immediately, such
// as a redraw requested while handling the event, so state that changed during
// the action's frame is reflected in the semantic tree. Unlike Refresh it never
// sleeps for a scheduled wakeup, and it stops after a few frames because a
// running animation keeps requesting immediate redraws.
func (d *Driver) Settle() error {
	for range settleFrames {
		if !d.hasNextFrame || d.nextFrame.After(time.Now()) {
			return nil
		}
		if err := d.Frame(); err != nil {
			return err
		}
	}
	return nil
}

// finishFrame completes a rendered frame and records the redraw it asks for.
func (d *Driver) finishFrame() {
	d.router.Frame(&d.ops)
	d.frameNumber++
	if at, ok := d.router.WakeupTime(); ok {
		if !d.hasNextFrame || at.Before(d.nextFrame) {
			d.hasNextFrame = true
			d.nextFrame = at
		}
	}
}

// settleFrames bounds Settle.
const settleFrames = 3

func (d *Driver) FrameNumber() uint64 { return d.frameNumber }

func (d *Driver) Size() image.Point { return d.size }

// Tree returns the semantic root and its descendants as a one-element slice.
// It returns nil when the driver is closed.
func (d *Driver) Tree() []Node {
	if d.closed {
		return nil
	}
	return normalizeTree(d.router.AppendSemantics(nil))
}

// ClickTarget selects a node by ID or label, or by explicit coordinates.
// Occurrence is a 1-based depth-first label match; a button and its nested
// label can both match, so use the button's ID when the label is ambiguous.
// Semantic targets prefer a hit-tested center within the viewport and semantic
// ancestor bounds. A click fails when another control covers the clipped
// center and the original center is not the target either. If neither center
// can be confirmed otherwise, they fall back to the clipped center; use
// coordinates for other clip layouts.
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
	if target.ID == nil && target.Label == "" {
		return f32.Point{}, errors.New("click target requires id, label, or coordinates")
	}
	tree := d.Tree()
	var id uint64
	if target.ID != nil {
		id = *target.ID
	} else {
		var matches []Node
		findLabel(tree, target.Label, &matches)
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
		id = matches[occurrence-1].ID
	}
	node, visible, ok := findNode(tree, id, image.Rectangle{Max: d.size})
	if !ok {
		return f32.Point{}, fmt.Errorf("semantic node %d does not exist", id)
	}
	if node.Disabled {
		return f32.Point{}, fmt.Errorf("semantic node %s is disabled", describeNode(node))
	}
	if node.Bounds.MinX >= node.Bounds.MaxX || node.Bounds.MinY >= node.Bounds.MaxY {
		return f32.Point{}, fmt.Errorf("semantic node %d has empty bounds", node.ID)
	}
	if visible.Empty() {
		return f32.Point{}, fmt.Errorf("semantic node %d has no visible bounds", node.ID)
	}
	// Try the visible center first, then the full center when clipping moved
	// it; the visible center is always inside visible.
	clippedCenter := visible.Min.Add(visible.Size().Div(2))
	points := []image.Point{clippedCenter}
	full := image.Rect(node.Bounds.MinX, node.Bounds.MinY, node.Bounds.MaxX, node.Bounds.MaxY)
	if center := full.Min.Add(full.Size().Div(2)); center != clippedCenter && center.In(visible) {
		points = append(points, center)
	}
	var cover *Node
	for i, point := range points {
		position := f32.Pt(float32(point.X), float32(point.Y))
		hit, ok := d.router.SemanticAt(position)
		if !ok {
			continue
		}
		// A button's nested label can be the first semantic hit. Accept its
		// descendants, but not an unrelated control or a clipping ancestor.
		if _, _, ok := findNode([]Node{node}, uint64(hit), visible); ok {
			return position, nil
		}
		// Only the clipped center matters for cover: it is what the fallback
		// below presses.
		if i == 0 {
			cover = coveringControl(tree, node, uint64(hit))
		}
	}
	// The fallback below would press whatever covers the clipped center, and
	// report success; fail instead when that is another control.
	if cover != nil {
		return f32.Point{}, fmt.Errorf("semantic node %d is covered by %s", node.ID, describeNode(*cover))
	}
	// SemanticAt is not a complete pointer-target query: for example, an
	// editor's decorative hint is a semantic sibling above its input handler.
	// Preserve the existing best-effort center when neither hit is conclusive.
	return f32.Pt(float32(clippedCenter.X), float32(clippedCenter.Y)), nil
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
	pending, at := d.hasNextFrame, d.nextFrame
	d.hasNextFrame = false
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
		d.hasNextFrame, d.nextFrame = pending, at
		return fmt.Errorf("%w: %w", ErrRender, err)
	}
	d.finishFrame()
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

// ScreenshotOptions selects part of the frame and shrinks it, so a screenshot
// costs less to store and read. The zero value is the full frame at full size.
type ScreenshotOptions struct {
	// Crop is the viewport area to keep; it is clipped to the viewport. The
	// zero Crop keeps the whole frame; any other empty Crop is an error.
	Crop image.Rectangle
	// Scale shrinks the (cropped) image; 0 and 1 keep its size, and other
	// values must be in (0, 1).
	Scale float64
}

func (d *Driver) Screenshot(path string) error {
	_, err := d.saveScreenshot(path, os.O_TRUNC, ScreenshotOptions{})
	return err
}

// CreateScreenshot is like Screenshot but never replaces an existing name,
// including a symlink; it then returns an error matching os.ErrExist. It
// returns the written image size, which opts can make differ from Size.
func (d *Driver) CreateScreenshot(path string, opts ScreenshotOptions) (image.Point, error) {
	return d.saveScreenshot(path, os.O_EXCL, opts)
}

// saveScreenshot renders a frame, then writes it as a PNG to path, opened with
// os.O_WRONLY|os.O_CREATE|mode. Rendering first leaves an existing file
// untouched when it fails; a failed write removes the file again.
func (d *Driver) saveScreenshot(path string, mode int, opts ScreenshotOptions) (image.Point, error) {
	if path == "" {
		return image.Point{}, errors.New("screenshot path is required")
	}
	img, err := d.Capture(opts)
	if err != nil {
		return image.Point{}, err
	}
	if err := savePNG(path, mode, img); err != nil {
		return image.Point{}, err
	}
	return img.Bounds().Size(), nil
}

// CreatePNG writes img as a PNG to a new file at path, like CreateScreenshot
// does with a frame from Capture. It returns an error matching os.ErrExist
// when the name exists, so a caller can retry another name without capturing
// again.
func CreatePNG(path string, img image.Image) error {
	if path == "" {
		return errors.New("screenshot path is required")
	}
	return savePNG(path, os.O_EXCL, img)
}

// savePNG writes img to path, opened with os.O_WRONLY|os.O_CREATE|mode; a
// failed write removes the file again.
func savePNG(path string, mode int, img image.Image) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create screenshot directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|mode, 0o644)
	if err != nil {
		return fmt.Errorf("create screenshot: %w", err)
	}
	if err := png.Encode(file, img); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("encode screenshot: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close screenshot: %w", err)
	}
	return nil
}

// WriteScreenshot renders the current frame and writes it to w as a PNG.
func (d *Driver) WriteScreenshot(w io.Writer, opts ScreenshotOptions) error {
	img, err := d.Capture(opts)
	if err != nil {
		return err
	}
	if err := png.Encode(w, img); err != nil {
		return fmt.Errorf("encode screenshot: %w", err)
	}
	return nil
}

// Capture renders the current frame, then applies opts.
func (d *Driver) Capture(opts ScreenshotOptions) (image.Image, error) {
	if d.closed || d.window == nil {
		return nil, errors.New("UI driver is closed")
	}
	if !(opts.Scale >= 0 && opts.Scale <= 1) { // also rejects NaN
		return nil, fmt.Errorf("screenshot scale %g is outside (0, 1]", opts.Scale)
	}
	area := image.Rectangle{Max: d.size}
	if opts.Crop != (image.Rectangle{}) {
		if opts.Crop.Empty() {
			return nil, fmt.Errorf("screenshot crop %v has no area", opts.Crop)
		}
		area = opts.Crop.Intersect(area)
		if area.Empty() {
			return nil, fmt.Errorf("screenshot crop %v is outside the %dx%d viewport", opts.Crop, d.size.X, d.size.Y)
		}
	}
	if err := d.Refresh(); err != nil {
		return nil, err
	}
	frame := image.NewRGBA(image.Rectangle{Max: d.size})
	if err := d.window.Screenshot(frame); err != nil {
		return nil, fmt.Errorf("capture screenshot: %w", err)
	}
	img := frame.SubImage(area)
	if opts.Scale == 0 || opts.Scale == 1 {
		return img, nil
	}
	size := image.Pt(
		max(1, int(float64(area.Dx())*opts.Scale+0.5)),
		max(1, int(float64(area.Dy())*opts.Scale+0.5)),
	)
	scaled := image.NewRGBA(image.Rectangle{Max: size})
	draw.ApproxBiLinear.Scale(scaled, scaled.Bounds(), img, area, draw.Src, nil)
	return scaled, nil
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
	// Gio names letter keys in uppercase, regardless of Shift. Accept both
	// spellings so remote shortcuts use the same input path as navigation.
	if len(name) == 1 {
		letter := name[0]
		if letter >= 'a' && letter <= 'z' {
			letter -= 'a' - 'A'
		}
		if letter >= 'A' && letter <= 'Z' {
			return key.Name(string(letter)), nil
		}
	}
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
