package app

import (
	"image"
	"math"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
)

// ── Motion ────────────────────────────────────────────────────────────────────
//
// Motion follows Material 3 (curves, durations) and tvOS (springs for focus).
// Logical state always changes at once; only its drawing catches up, so motion
// never delays D-pad input. Values are computed from the frame time and a
// redraw is requested only while something moves.

// easeStandard is Material 3's standard curve, for on-screen changes.
var easeStandard = cubicBezier(0.2, 0, 0, 1)

// cubicBezier returns the CSS-style timing function with control points
// (x1, y1) and (x2, y2), mapping progress in [0, 1] to eased progress.
func cubicBezier(x1, y1, x2, y2 float64) func(float32) float32 {
	bezier := func(t, p1, p2 float64) float64 {
		u := 1 - t
		return 3*u*u*t*p1 + 3*u*t*t*p2 + t*t*t
	}
	slope := func(t, p1, p2 float64) float64 {
		u := 1 - t
		return 3*u*u*p1 + 6*u*t*(p2-p1) + 3*t*t*(1-p2)
	}
	return func(x float32) float32 {
		if x <= 0 {
			return 0
		}
		if x >= 1 {
			return 1
		}
		// Solve bezier(t) = x: Newton steps, then bisection if the slope
		// is too flat for them to converge.
		target, t := float64(x), float64(x)
		for range 8 {
			d := slope(t, x1, x2)
			if math.Abs(d) < 1e-6 {
				break
			}
			t -= (bezier(t, x1, x2) - target) / d
		}
		if t < 0 || t > 1 || math.Abs(bezier(t, x1, x2)-target) > 1e-4 {
			lo, hi := 0.0, 1.0
			for range 30 {
				t = (lo + hi) / 2
				if bezier(t, x1, x2) < target {
					lo = t
				} else {
					hi = t
				}
			}
		}
		return float32(bezier(t, y1, y2))
	}
}

// easedProgress is how far an eased transition of duration d started at
// start has progressed at now, in [0, 1]; a zero start is finished.
func easedProgress(now, start time.Time, d time.Duration, ease func(float32) float32) float32 {
	if start.IsZero() || d <= 0 {
		return 1
	}
	elapsed := now.Sub(start)
	if elapsed >= d {
		return 1
	}
	return ease(max(0, float32(elapsed)/float32(d)))
}

// spring is a critically damped spring that pulls an offset to zero, the
// tvOS-style motion for focus: kicking it while it moves keeps its velocity,
// so a held key gives one continuous glide instead of restarting each press.
type spring struct {
	pos, vel float32 // offset from rest and its velocity, per second
	last     time.Time
}

// springOmega sets how fast springs settle: ω = 2π/response, and a 0.3 s
// response settles a one-row move in about 300 ms, Material 3's medium
// duration.
const springOmega = 2 * math.Pi / 0.3

// settled reports whether the spring is at rest.
func (s *spring) settled() bool { return s.pos == 0 && s.vel == 0 }

// kick displaces the spring by delta, clamped to ±limit (when positive) so a
// held key never leaves the drawing far behind.
func (s *spring) kick(now time.Time, delta, limit float32) {
	s.step(now)
	s.pos += delta
	if limit > 0 {
		s.pos = min(max(s.pos, -limit), limit)
	}
	s.last = now
}

// reset puts the spring at rest at once.
func (s *spring) reset() { *s = spring{} }

// step advances the spring to now with the exact solution of the critically
// damped oscillator, stable for any frame interval.
func (s *spring) step(now time.Time) float32 {
	if s.settled() {
		s.last = now
		return 0
	}
	dt := float32(now.Sub(s.last).Seconds())
	s.last = now
	if dt <= 0 {
		return s.pos
	}
	const w = springOmega
	decay := float32(math.Exp(float64(-w * dt)))
	b := s.vel + w*s.pos
	s.pos, s.vel = (s.pos+b*dt)*decay, (s.vel-w*b*dt)*decay
	if abs32(s.pos) < springRestPos && abs32(s.vel) < springRestVel {
		s.pos, s.vel = 0, 0
	}
	return s.pos
}

// The spring rests below half a pixel, moving slower than it can show.
// Unitless springs use a 0..100 scale so the same thresholds apply.
const (
	springRestPos = 0.5
	springRestVel = 10
)

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// animating requests the next frame and marks motion as running, so the
// headless driver waits for it before a screenshot (Loading).
func (a *App) animating(gtx C) {
	a.inMotion = true
	gtx.Execute(op.InvalidateCmd{})
}

// frameTime is the frame's clock: gtx.Now, or the wall clock when a caller
// lays out without one.
func frameTime(gtx C) time.Time {
	if gtx.Now.IsZero() {
		return time.Now()
	}
	return gtx.Now
}

// ── List focus motion ─────────────────────────────────────────────────────────

// focusLift is how much the focused row grows, like a focused Android TV
// card or tvOS item. A wide row grows less, by at most focusLiftReach on
// each side: the focus fill's inset, so a lifted row stays inside its list.
const (
	focusLift      = 0.03
	focusLiftReach = dp8
)

// liftScale is the scale of a row of the given width lifted by amount.
func liftScale(gtx C, width int, amount float32) float32 {
	if amount == 0 || width <= 0 {
		return 1
	}
	lift := min(focusLift, 2*float32(gtx.Dp(focusLiftReach))/float32(width))
	return 1 + lift*amount
}

// listMotion animates a keyboard-driven layout.List whose cursor the caller
// keeps scrolled into place: the list glides to its new position, the
// highlight glides between rows where the list cannot scroll, and the focused
// row lifts. Where the list scrolls under a centred cursor the highlight stays
// put, as on Android TV and tvOS.
//
// The caller picks the list's resting First (target) each frame; begin turns
// it into a Position, measuring the move with the row heights laid out in the
// previous frame. A move it cannot measure (a new list, a resize, rows not
// laid out yet) snaps.
type listMotion struct {
	scroll    spring // remaining scroll, in pixels: content still shows this far back
	highlight spring // remaining highlight move on screen, in pixels
	lift      spring // focused row lift, -100 (flat) .. 0 (lifted)
	unlift    spring // previously focused row lift, 100 (lifted) .. 0 (flat)

	liftIndex, unliftIndex int

	key            any
	target, cursor int
	topRel         int             // resting top of the view, relative to row target
	valid          bool            // key, target, cursor and topRel describe the last frame
	rest           layout.Position // last position at rest, see captureRest
	restFull       int             // rows wholly visible at rest
	reveal         bool            // visibleTarget needs the list to move
	size           image.Point     // constraints of the last frame, see focusList

	heights, prevHeights map[int]int
}

// begin positions list for this frame. key identifies the list's contents,
// cursor is the focused row (or -1), target the row the caller wants at the
// top, n the row count and viewport the list's main-axis size. It reports
// whether the list is still moving; the caller then requests the next frame.
func (m *listMotion) begin(gtx C, list *layout.List, key any, cursor, target, n, viewport int) bool {
	now := frameTime(gtx)
	m.prevHeights, m.heights = m.heights, m.prevHeights
	if m.heights == nil {
		m.heights = make(map[int]int)
	}
	clear(m.heights)

	topRel, known := m.restingTop(target, n, viewport)
	moved := !m.valid || key != m.key || target != m.target || m.reveal // the list must move
	m.reveal = false
	if !moved {
		// It rests where it is, perhaps where the wheel left it.
		topRel, known = m.topRel, true
	}
	switch {
	case !moved && cursor == m.cursor:
	case m.valid && key == m.key && known && cursor >= 0 && m.cursor >= 0 && cursor != m.cursor:
		oldTop, ok0 := m.topRel, true
		scroll, ok1 := m.dist(m.target, target)
		oldSel, ok2 := m.dist(m.target, m.cursor)
		newSel, ok3 := m.dist(target, cursor)
		if !ok0 || !ok1 || !ok2 || !ok3 {
			m.snap()
			break
		}
		limit := float32(viewport) / 4
		if h, ok := m.prevHeights[cursor]; ok {
			limit = 1.5 * float32(h)
		}
		scroll += topRel - oldTop
		m.scroll.kick(now, float32(scroll), limit)
		m.highlight.kick(now, float32((oldSel-oldTop)-(newSel-topRel)), limit)
		// The previous row settles from its current lift, keeping its
		// velocity, while the new one lifts from flat.
		lifted := 100 + m.lift.step(now)
		m.unlift = spring{pos: lifted, vel: m.lift.vel, last: now}
		m.unliftIndex = m.liftIndex
		m.lift = spring{pos: -100, last: now}
		m.liftIndex = cursor
	default:
		m.snap()
		m.liftIndex = cursor
	}
	m.key, m.target, m.cursor, m.topRel, m.valid = key, target, cursor, topRel, true

	scrolling := !m.scroll.settled()
	offset := m.scroll.step(now)
	m.highlight.step(now)
	m.lift.step(now)
	m.unlift.step(now)
	// Leave a list that need not move alone, so the wheel can scroll it.
	if moved || scrolling {
		list.Position.First = target
		list.Position.Offset = topRel - int(math.Round(float64(offset)))
	}
	return !m.scroll.settled() || !m.highlight.settled() || !m.lift.settled() || !m.unlift.settled()
}

// end checks, after the list's layout, that it shows where begin put it. A
// wheel or drag scrolls the list itself: its position becomes the resting
// one, so the wheel keeps working and the next move glides from what the list
// shows.
func (m *listMotion) end(list *layout.List) {
	d, ok := 0, true
	if first := list.Position.First; first != m.target {
		d, ok = m.distIn(m.heights, m.target, first)
	}
	want := m.topRel - int(math.Round(float64(m.scroll.pos)))
	if got := d + list.Position.Offset; ok && got >= want-1 && got <= want+1 {
		return
	}
	m.scroll.reset()
	m.target, m.topRel = list.Position.First, list.Position.Offset
}

// The resting view: a list in motion shows partial rows that change its
// position and count, so targets are computed from the last view at rest.
func (m *listMotion) captureRest(list *layout.List) {
	if !m.scroll.settled() {
		return
	}
	pos := list.Position
	m.rest = pos
	m.restFull = pos.Count
	if pos.Offset > 0 {
		m.restFull--
	}
	if pos.OffsetLast < 0 {
		m.restFull--
	}
}

// centreTarget keeps the cursor centred, like scrolloff=999.
func (m *listMotion) centreTarget(list *layout.List, cursor int) int {
	m.captureRest(list)
	if m.rest.Count == 0 {
		return m.rest.First
	}
	return max(0, cursor-m.rest.Count/2)
}

// visibleTarget scrolls only as far as needed to show the whole cursor row.
func (m *listMotion) visibleTarget(list *layout.List, cursor int) int {
	m.captureRest(list)
	start := m.rest.First
	if m.rest.Offset > 0 {
		start++ // partially scrolled out
	}
	switch {
	case m.restFull <= 0:
		return m.rest.First
	case cursor < start:
		m.reveal = true // even when its row is already first, partly scrolled out
		return cursor
	case cursor >= start+m.restFull:
		m.reveal = true
		return cursor - m.restFull + 1
	case m.valid:
		// Keep the requested target: Gio may have normalised the resting
		// position (at the end of the list), and that is not a move.
		return m.target
	}
	return m.rest.First
}

// snap puts every spring at rest, the focused row fully lifted.
func (m *listMotion) snap() {
	m.scroll.reset()
	m.highlight.reset()
	m.lift.reset()
	m.unlift.reset()
	m.unliftIndex = -1
}

// record notes a laid-out row's main-axis size for the next frame.
func (m *listMotion) record(i, size int) {
	if m.heights != nil {
		m.heights[i] = size
	}
}

// highlightOffset is where the focused row's highlight is drawn relative to
// the row: the row itself still moves with the scroll.
func (m *listMotion) highlightOffset() int {
	return int(math.Round(float64(m.highlight.pos - m.scroll.pos)))
}

// lifted is how far row i is lifted, from 0 (flat) to 1.
func (m *listMotion) lifted(i int) float32 {
	switch i {
	case m.liftIndex:
		return (100 + m.lift.pos) / 100
	case m.unliftIndex:
		return m.unlift.pos / 100
	}
	return 0
}

// dist is the content distance from the top of row a to the top of row b,
// known when every row between was laid out last frame.
func (m *listMotion) dist(a, b int) (int, bool) {
	return m.distIn(m.prevHeights, a, b)
}

// distIn is dist with the given row sizes.
func (m *listMotion) distIn(heights map[int]int, a, b int) (int, bool) {
	sign := 1
	if b < a {
		a, b, sign = b, a, -1
	}
	d := 0
	for i := a; i < b; i++ {
		h, ok := heights[i]
		if !ok {
			return 0, false
		}
		d += h
	}
	return sign * d, true
}

// restingTop is where layout.List rests with target first: at target's top,
// or earlier when the rows from target on do not fill the viewport, since the
// list never scrolls past its end.
func (m *listMotion) restingTop(target, n, viewport int) (int, bool) {
	below := 0
	for i := target; i < n && below < viewport; i++ {
		h, ok := m.prevHeights[i]
		if !ok {
			return 0, false
		}
		below += h
	}
	if below >= viewport {
		return 0, true
	}
	need := viewport - below
	top := 0
	for i := target - 1; i >= 0 && need > 0; i-- {
		h, ok := m.prevHeights[i]
		if !ok {
			return 0, false
		}
		step := min(h, need)
		top -= step
		need -= step
	}
	return top, true
}
