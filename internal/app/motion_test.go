package app

import (
	"fmt"
	"image"
	"log/slog"
	"testing"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

func TestCubicBezierEasing(t *testing.T) {
	for name, ease := range map[string]func(float32) float32{
		"standard": easeStandard,
		"linear":   cubicBezier(0, 0, 1, 1),
	} {
		if ease(0) != 0 || ease(1) != 1 || ease(-1) != 0 || ease(2) != 1 {
			t.Errorf("%s endpoints = %v %v, want clamped 0 and 1", name, ease(0), ease(1))
		}
		prev := float32(0)
		for i := 1; i <= 100; i++ {
			v := ease(float32(i) / 100)
			if v < prev-1e-4 {
				t.Fatalf("%s is not monotonic at %d%%: %v < %v", name, i, v, prev)
			}
			prev = v
		}
	}
	if v := cubicBezier(0, 0, 1, 1)(0.3); v < 0.299 || v > 0.301 {
		t.Errorf("linear bezier(0.3) = %v", v)
	}
	// Standard decelerates: most of the change happens early, gently.
	if v := easeStandard(0.5); v < 0.8 || v > 0.95 {
		t.Errorf("standard(0.5) = %v, want a decelerating curve", v)
	}
	if v := easeStandard(0.1); v > 0.3 {
		t.Errorf("standard(0.1) = %v, want a gentle start, not a pop", v)
	}
}

func TestSpringSettlesWithoutOvershoot(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	var s spring
	s.kick(start, 100, 0)
	now := start
	for s.step(now) != 0 {
		if s.pos < 0 {
			t.Fatalf("spring overshot to %v at %v", s.pos, now.Sub(start))
		}
		now = now.Add(16 * time.Millisecond)
		if now.Sub(start) > time.Second {
			t.Fatalf("spring did not settle within a second: %v", s.pos)
		}
	}
	if d := now.Sub(start); d < 200*time.Millisecond || d > 450*time.Millisecond {
		t.Errorf("one-row spring settled in %v, want about 300 ms", d)
	}
	if !s.settled() {
		t.Error("spring at zero is not settled")
	}

	// Frame-rate independence: one big step lands where small steps do.
	var a, b spring
	a.kick(start, 100, 0)
	b.kick(start, 100, 0)
	for i := 1; i <= 6; i++ {
		a.step(start.Add(time.Duration(i) * 10 * time.Millisecond))
	}
	b.step(start.Add(60 * time.Millisecond))
	if d := a.pos - b.pos; d > 0.01 || d < -0.01 {
		t.Errorf("60 ms in steps = %v, in one step = %v", a.pos, b.pos)
	}
}

func TestSpringKickKeepsVelocityAndLimit(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	var s spring
	s.kick(start, 100, 150)
	s.step(start.Add(50 * time.Millisecond))
	vel := s.vel
	s.kick(start.Add(50*time.Millisecond), 100, 150)
	if s.vel != vel {
		t.Errorf("kick changed velocity %v to %v; a held key must glide on", vel, s.vel)
	}
	if s.pos != 150 {
		t.Errorf("kick past the limit = %v, want 150", s.pos)
	}
}

// motionListApp is a catalog with one terminal row per letter, so the movie
// section lists 26 tall rows.
func motionListApp(t *testing.T) *App {
	t.Helper()
	var movies []*listingItem
	for i := range 26 {
		movies = append(movies, newMovieListingItem(testMovie(fmt.Sprintf("tt%03d", i), fmt.Sprintf("%c movie", 'A'+i), 2000+i)))
	}
	snapshot := buildLibrarySnapshot(radixListingBuilder{}, movies, nil)
	client := &APIClient{movieItems: movies, snapshot: snapshot, log: slog.Default()}
	a := New(client, nil, &Config{MPVPath: "mpv"}, nil, nil)
	a.browse.nav.resetToSection(SectionMovies, a.sectionRoot(SectionMovies))
	if n := a.browse.nav.getChildrenCount(); n != 26 {
		t.Fatalf("fixture has %d rows, want 26", n)
	}
	return a
}

// layoutNavFrame lays out the nav list alone at now and reports whether it
// asked for another frame.
func layoutNavFrame(a *App, now time.Time) bool {
	var ops op.Ops
	a.inMotion = false
	a.layoutNavList(C{
		Ops:         &ops,
		Now:         now,
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(image.Pt(600, 500)),
	})
	return a.inMotion
}

// settleNav renders frames until the list stops moving.
func settleNav(t *testing.T, a *App, now time.Time) time.Time {
	t.Helper()
	for range 100 {
		if !layoutNavFrame(a, now) {
			return now
		}
		now = now.Add(16 * time.Millisecond)
	}
	t.Fatal("nav list never settled")
	return now
}

func TestNavListScrollGlides(t *testing.T) {
	a := motionListApp(t)
	now := time.Unix(1_700_000_000, 0)
	now = settleNav(t, a, now)
	a.browse.nav.cursor = 12
	now = settleNav(t, a, now.Add(time.Second)) // a jump this far snaps
	m := &a.browse.navMotion
	if a.leftList.Position.Offset != 0 {
		t.Fatalf("resting offset = %d, want 0", a.leftList.Position.Offset)
	}
	// Every row has the same height, so the drawn scroll position is
	// First*row + Offset whichever way Gio normalises a negative offset.
	row := m.heights[12]
	top := func() int { return a.leftList.Position.First*row + a.leftList.Position.Offset }
	startTop := top()

	a.navigateDown()
	if !layoutNavFrame(a, now) {
		t.Fatal("Down in the middle of the list did not animate")
	}
	if m.target*row != startTop+row {
		t.Fatalf("resting target %d, want one row on from top %d", m.target, startTop)
	}
	if got := top(); got != startTop {
		t.Fatalf("first frame top = %d, want %d (content still where it was)", got, startTop)
	}
	// The cursor stays centred, so the highlight stays put on screen while
	// the rows slide under it.
	if h := m.highlightOffset(); h != -row {
		t.Errorf("highlight offset in the row = %d, want -%d (fixed on screen)", h, row)
	}
	prev := startTop
	for i := 1; ; i++ {
		moving := layoutNavFrame(a, now.Add(time.Duration(i)*16*time.Millisecond))
		got := top()
		if got < prev || got > startTop+row {
			t.Fatalf("frame %d top = %d after %d, want a monotonic glide to %d", i, got, prev, startTop+row)
		}
		prev = got
		if !moving {
			if got != startTop+row || i*16 > 450 {
				t.Fatalf("settled at top %d after %d ms, want %d", got, i*16, startTop+row)
			}
			break
		}
	}
	if l := m.lifted(a.browse.nav.cursor); l != 1 {
		t.Errorf("settled focused row lift = %v, want 1", l)
	}
	if l := m.lifted(12); l != 0 {
		t.Errorf("previous row lift = %v, want 0", l)
	}
}

func TestNavListHighlightGlidesAtTop(t *testing.T) {
	a := motionListApp(t)
	now := settleNav(t, a, time.Unix(1_700_000_000, 0))
	m := &a.browse.navMotion
	a.navigateDown()
	layoutNavFrame(a, now)
	if a.leftList.Position.First != 0 || a.leftList.Position.Offset != 0 {
		t.Fatalf("near the top the list scrolled: %+v", a.leftList.Position)
	}
	row := m.prevHeights[0]
	if h := m.highlightOffset(); h != -row {
		t.Fatalf("highlight starts at %d, want -%d (the previous row)", h, row)
	}
	if l := m.lifted(1); l != 0 {
		t.Errorf("newly focused row starts lifted: %v", l)
	}
	if l := m.lifted(0); l != 1 {
		t.Errorf("previous row drops at once: %v", l)
	}
	settleNav(t, a, now.Add(16*time.Millisecond))
	if h := m.highlightOffset(); h != 0 {
		t.Errorf("settled highlight offset = %d", h)
	}
}

func TestNavListHeldKeyLagIsBounded(t *testing.T) {
	a := motionListApp(t)
	now := time.Unix(1_700_000_000, 0)
	now = settleNav(t, a, now)
	a.browse.nav.cursor = 6
	now = settleNav(t, a, now.Add(time.Second))
	m := &a.browse.navMotion
	for i := range 10 {
		a.navigateDown()
		layoutNavFrame(a, now.Add(time.Duration(i)*time.Millisecond))
		if limit := 1.5 * float32(m.prevHeights[a.browse.nav.cursor]); m.scroll.pos > limit {
			t.Fatalf("press %d: scroll lag %v exceeds %v", i, m.scroll.pos, limit)
		}
	}
}

func TestNavListSnaps(t *testing.T) {
	for name, move := range map[string]func(a *App){
		"wrap":     func(a *App) { a.navigateUp() },
		"drill in": func(a *App) { a.navigateLeft(); a.navigateForward() },
		"listing":  func(a *App) { a.applyListingMode(ListingModeDateAdded) },
	} {
		t.Run(name, func(t *testing.T) {
			a := motionListApp(t)
			now := settleNav(t, a, time.Unix(1_700_000_000, 0))
			move(a)
			if layoutNavFrame(a, now) {
				t.Errorf("%s animated: %+v", name, a.leftList.Position)
			}
		})
	}
}

// plainFocusList lays out a focus list of n 50 px rows in a 200 px viewport
// at now, scrolling only to keep the cursor visible, and reports whether it
// asked for another frame.
func plainFocusList(a *App, list *layout.List, m *listMotion, n, cursor int, now time.Time) bool {
	var ops op.Ops
	a.inMotion = false
	a.focusList(C{
		Ops:         &ops,
		Now:         now,
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(image.Pt(300, 200)),
	}, list, m, "plain", n, cursor, true, focusStyle{},
		func(gtx C, i int) D { return D{Size: image.Pt(300, 50)} })
	return a.inMotion
}

func settlePlain(t *testing.T, a *App, list *layout.List, m *listMotion, n, cursor int, now time.Time) time.Time {
	t.Helper()
	for range 100 {
		if !plainFocusList(a, list, m, n, cursor, now) {
			return now
		}
		now = now.Add(16 * time.Millisecond)
	}
	t.Fatal("list never settled")
	return now
}

func TestFocusListKeepsCursorVisible(t *testing.T) {
	a := &App{}
	list := &layout.List{Axis: layout.Vertical}
	var m listMotion
	now := settlePlain(t, a, list, &m, 20, 0, time.Unix(1_700_000_000, 0))
	now = settlePlain(t, a, list, &m, 20, 0, now) // rows measured

	// Inside the view only the highlight moves.
	plainFocusList(a, list, &m, 20, 1, now)
	if list.Position.First != 0 || list.Position.Offset != 0 || m.highlightOffset() != -50 {
		t.Fatalf("inside the view: %+v, highlight %d", list.Position, m.highlightOffset())
	}
	now = settlePlain(t, a, list, &m, 20, 1, now.Add(16*time.Millisecond))
	for c := 2; c <= 3; c++ {
		now = settlePlain(t, a, list, &m, 20, c, now)
	}

	// Past the edge the list glides one row, the highlight staying put.
	plainFocusList(a, list, &m, 20, 4, now)
	if top := list.Position.First*50 + list.Position.Offset; top != 0 {
		t.Fatalf("first frame past the edge shows top %d, want 0", top)
	}
	if h := m.highlightOffset(); h != -50 {
		t.Fatalf("highlight offset %d, want -50 (fixed at the edge)", h)
	}
	settlePlain(t, a, list, &m, 20, 4, now.Add(16*time.Millisecond))
	if list.Position.First != 1 || list.Position.Offset != 0 {
		t.Fatalf("settled at %+v, want row 1 first", list.Position)
	}
}

func TestFocusListFollowsTheWheel(t *testing.T) {
	a := &App{}
	list := &layout.List{Axis: layout.Vertical}
	var m listMotion
	now := settlePlain(t, a, list, &m, 20, 0, time.Unix(1_700_000_000, 0))
	now = settlePlain(t, a, list, &m, 20, 0, now)

	// The wheel scrolls part of a row: the list stays there.
	list.Position.Offset = 20
	now = settlePlain(t, a, list, &m, 20, 1, now)
	if list.Position.First != 0 || list.Position.Offset != 20 {
		t.Fatalf("a cursor move inside the view undid the wheel: %+v", list.Position)
	}
	// Up to the partly hidden row: the list glides from where it is.
	plainFocusList(a, list, &m, 20, 0, now)
	if top := list.Position.First*50 + list.Position.Offset; top != 20 {
		t.Fatalf("reveal started at top %d, want 20 (where the wheel left it)", top)
	}
	settlePlain(t, a, list, &m, 20, 0, now.Add(16*time.Millisecond))
	if list.Position.First != 0 || list.Position.Offset != 0 {
		t.Fatalf("reveal settled at %+v", list.Position)
	}
}

func TestModeRailFocusGlides(t *testing.T) {
	a := motionListApp(t)
	a.browse.focus = browseFocusModeRail
	rail := func(now time.Time) bool {
		var ops op.Ops
		a.inMotion = false
		a.layoutModeRail(C{
			Ops:         &ops,
			Now:         now,
			Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Constraints: layout.Exact(image.Pt(240, 600)),
		})
		return a.inMotion
	}
	now := time.Unix(1_700_000_000, 0)
	for rail(now) {
		now = now.Add(16 * time.Millisecond)
	}
	rail(now)
	a.navigateDown()
	if !rail(now) {
		t.Fatal("moving on the rail did not animate")
	}
	m := &a.browse.railMotion
	if h := m.highlightOffset(); h >= 0 {
		t.Fatalf("rail focus starts at %d, want above (the previous entry)", h)
	}
	for i := 1; rail(now.Add(time.Duration(i) * 16 * time.Millisecond)); i++ {
		if i > 40 {
			t.Fatal("rail motion did not settle")
		}
	}
	if h := m.highlightOffset(); h != 0 {
		t.Fatalf("settled rail focus offset %d", h)
	}
}

func TestLiftStaysInsideTheRow(t *testing.T) {
	gtx := C{Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}}
	if s := liftScale(gtx, 200, 1); s != 1+focusLift {
		t.Errorf("narrow row lift = %v, want %v", s, 1+focusLift)
	}
	for _, width := range []int{600, 733, 1800} {
		s := liftScale(gtx, width, 1)
		if grow := (s - 1) * float32(width) / 2; grow > float32(focusLiftReach)+0.01 {
			t.Errorf("%d px row grows %v px per side, past the fill inset", width, grow)
		}
	}
	if s := liftScale(gtx, 733, 0); s != 1 {
		t.Errorf("flat row scale = %v", s)
	}
}
