package driver

import (
	"bytes"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/font/gofont"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/text"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

func TestHeadlessUI(t *testing.T) {
	var button widget.Clickable
	clicked := false
	d, err := New(Config{Width: 320, Height: 200, PxPerDp: 1, PxPerSp: 1}, func(gtx layout.Context) layout.Dimensions {
		if button.Clicked(gtx) {
			clicked = true
		}
		return button.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			semantic.Button.Add(gtx.Ops)
			semantic.LabelOp("Apply").Add(gtx.Ops)
			return layout.Dimensions{Size: gtx.Constraints.Max}
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	if len(d.Tree()) == 0 {
		t.Fatal("initial semantic tree is empty")
	}
	if err := d.Click(ClickTarget{Label: "Apply", Occurrence: 1}); err != nil {
		t.Fatal(err)
	}
	if !clicked {
		t.Fatal("semantic click did not reach widget")
	}
}

func TestSemanticTreeAndClickTargets(t *testing.T) {
	var buttons [3]widget.Clickable
	var clicks [3]int
	d, err := New(Config{Width: 240, Height: 160, PxPerDp: 1, PxPerSp: 1}, func(gtx layout.Context) layout.Dimensions {
		var rows []layout.FlexChild
		for i := range buttons {
			rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if buttons[i].Clicked(gtx) {
					clicks[i]++
				}
				return buttons[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					semantic.Button.Add(gtx.Ops)
					label := "Twin"
					if i == 2 {
						label = "Unique"
					}
					semantic.LabelOp(label).Add(gtx.Ops)
					if i < 2 {
						stack := clip.Rect(image.Rect(0, 0, 120, 30)).Push(gtx.Ops)
						semantic.LabelOp(label).Add(gtx.Ops)
						stack.Pop()
					}
					return layout.Dimensions{Size: image.Pt(160, 40)}
				})
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	tree := d.Tree()
	if len(tree) != 1 {
		t.Fatalf("tree has %d top-level nodes, want one root", len(tree))
	}
	seen := make(map[uint64]bool)
	var check func(Node, uint64)
	check = func(node Node, parent uint64) {
		if seen[node.ID] || node.ParentID != parent {
			t.Fatalf("repeated node or incorrect parent: %+v, want parent %d", node, parent)
		}
		seen[node.ID] = true
		for _, child := range node.Children {
			check(child, node.ID)
		}
	}
	check(tree[0], 0)
	flat := d.router.AppendSemantics(nil)
	if len(seen) != len(flat) {
		t.Fatalf("root subtree has %d unique IDs, Gio returned %d nodes", len(seen), len(flat))
	}
	for _, node := range flat {
		if !seen[uint64(node.ID)] {
			t.Fatalf("root subtree lost semantic node %d", node.ID)
		}
	}
	var matches []Node
	findLabel(tree, "Twin", &matches)
	if len(matches) != 4 || matches[0].Class != "button" || matches[1].ParentID != matches[0].ID || matches[2].Class != "button" || matches[3].ParentID != matches[2].ID || matches[0].ID == matches[2].ID {
		t.Fatalf("duplicate-label traversal = %+v, want two distinct buttons and their nested labels", matches)
	}
	if err := d.Click(ClickTarget{Label: "Twin"}); err == nil || !strings.Contains(err.Error(), "matched 4 nodes") {
		t.Fatalf("ambiguous label error = %v, want four matches", err)
	}
	if err := d.Click(ClickTarget{Label: "Twin", Occurrence: 5}); err == nil || !strings.Contains(err.Error(), "out of range (1-4)") {
		t.Fatalf("out-of-range occurrence error = %v", err)
	}

	for index := range 2 {
		// IDs may change after a frame: resolve each from the current tree.
		matches = nil
		findLabel(d.Tree(), "Twin", &matches)
		id := matches[index*2].ID
		if err := d.Click(ClickTarget{ID: &id}); err != nil {
			t.Fatal(err)
		}
	}
	if clicks != [3]int{1, 1, 0} {
		t.Fatalf("button ID clicks = %v, want [1 1 0]", clicks)
	}
	for _, tc := range []struct{ occurrence, button int }{{1, 0}, {2, 0}, {3, 1}, {4, 1}} {
		want := clicks
		want[tc.button]++
		if err := d.Click(ClickTarget{Label: "Twin", Occurrence: tc.occurrence}); err != nil {
			t.Fatal(err)
		}
		if clicks != want {
			t.Fatalf("occurrence %d clicks = %v, want %v", tc.occurrence, clicks, want)
		}
	}
	if err := d.Click(ClickTarget{Label: "Unique"}); err != nil || clicks != [3]int{3, 3, 1} {
		t.Fatalf("unique label click = %v, counts %v", err, clicks)
	}
}

func TestClickTargetsRespectVisibleBounds(t *testing.T) {
	var partial, hidden, offscreen widget.Clickable
	clicks := 0
	d, err := New(Config{Width: 100, Height: 100, PxPerDp: 1, PxPerSp: 1}, func(gtx layout.Context) layout.Dimensions {
		if partial.Clicked(gtx) {
			clicks++
		}
		viewport := clip.Rect(image.Rect(0, 0, 100, 100)).Push(gtx.Ops)
		semantic.LabelOp("Viewport").Add(gtx.Ops)
		offset := op.Offset(image.Pt(80, 10)).Push(gtx.Ops)
		partial.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			semantic.Button.Add(gtx.Ops)
			semantic.LabelOp("Partial").Add(gtx.Ops)
			child := clip.Rect(image.Rect(0, 0, 1000, 1000)).Push(gtx.Ops)
			semantic.LabelOp("Oversized child").Add(gtx.Ops)
			child.Pop()
			return layout.Dimensions{Size: image.Pt(60, 30)}
		})
		offset.Pop()
		viewport.Pop()

		parent := clip.Rect(image.Rect(0, 50, 40, 90)).Push(gtx.Ops)
		semantic.LabelOp("Clipping parent").Add(gtx.Ops)
		offset = op.Offset(image.Pt(60, 60)).Push(gtx.Ops)
		hidden.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			semantic.Button.Add(gtx.Ops)
			semantic.LabelOp("Clipped").Add(gtx.Ops)
			return layout.Dimensions{Size: image.Pt(20, 20)}
		})
		offset.Pop()
		parent.Pop()

		offset = op.Offset(image.Pt(120, 10)).Push(gtx.Ops)
		offscreen.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			semantic.Button.Add(gtx.Ops)
			semantic.LabelOp("Offscreen").Add(gtx.Ops)
			return layout.Dimensions{Size: image.Pt(20, 20)}
		})
		offset.Pop()

		disabled := clip.Rect(image.Rect(0, 0, 20, 20)).Push(gtx.Ops)
		semantic.Button.Add(gtx.Ops)
		semantic.LabelOp("Disabled").Add(gtx.Ops)
		semantic.EnabledOp(false).Add(gtx.Ops)
		disabled.Pop()

		return layout.Dimensions{Size: image.Pt(100, 100)}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	idFor := func(label string) uint64 {
		t.Helper()
		var matches []Node
		findLabel(d.Tree(), label, &matches)
		if len(matches) != 1 {
			t.Fatalf("semantic label %q has %d matches: %+v", label, len(matches), d.Tree())
		}
		return matches[0].ID
	}
	for _, label := range []string{"Partial", "Oversized child"} {
		id := idFor(label)
		if err := d.Click(ClickTarget{ID: &id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Click(ClickTarget{Label: "Oversized child"}); err != nil {
		t.Fatal(err)
	}
	if err := d.Click(ClickTarget{Coordinates: true, X: 90, Y: 25}); err != nil {
		t.Fatal(err)
	}
	if clicks != 4 {
		t.Fatalf("visible ID, label and coordinate clicks = %d, want 4", clicks)
	}
	for _, label := range []string{"Clipped", "Offscreen", "Disabled"} {
		id := idFor(label)
		frame := d.FrameNumber()
		err := d.Click(ClickTarget{ID: &id})
		want := "visible"
		if label == "Disabled" {
			want = "disabled"
		}
		if err == nil || !strings.Contains(err.Error(), want) || d.FrameNumber() != frame {
			t.Fatalf("click %q = %v; want %s error and no frame", label, err, want)
		}
	}
	disabledID := idFor("Disabled")
	if err := d.Click(ClickTarget{ID: &disabledID, Coordinates: true, X: 90, Y: 25}); err != nil || clicks != 5 {
		t.Fatalf("explicit coordinates did not take precedence: err=%v clicks=%d", err, clicks)
	}
	missingID := ^uint64(0)
	for _, tc := range []struct {
		target ClickTarget
		want   string
	}{
		{ClickTarget{}, "requires id"},
		{ClickTarget{ID: &missingID}, "does not exist"},
		{ClickTarget{Label: "Missing"}, "does not exist"},
		{ClickTarget{Label: "Oversized child", Occurrence: -1}, "out of range"},
		{ClickTarget{Coordinates: true, X: 100, Y: 25}, "outside viewport"},
	} {
		frame := d.FrameNumber()
		if err := d.Click(tc.target); err == nil || !strings.Contains(err.Error(), tc.want) || d.FrameNumber() != frame {
			t.Fatalf("invalid click %+v = %v, want %s error and no frame", tc.target, err, tc.want)
		}
	}
}

func TestClickTargetsThroughNonSemanticClips(t *testing.T) {
	for _, tc := range []struct {
		name string
		clip image.Rectangle
	}{
		{"original center", image.Rect(85, 0, 100, 100)},
		{"clipped center", image.Rect(75, 0, 85, 100)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var button widget.Clickable
			clicks := 0
			d, err := New(Config{Width: 100, Height: 100, PxPerDp: 1, PxPerSp: 1}, func(gtx layout.Context) layout.Dimensions {
				if button.Clicked(gtx) {
					clicks++
				}
				outer := clip.Rect(image.Rect(0, 0, 100, 100)).Push(gtx.Ops)
				semantic.LabelOp("Outer").Add(gtx.Ops)
				inner := clip.Rect(tc.clip).Push(gtx.Ops)
				offset := op.Offset(image.Pt(60, 20)).Push(gtx.Ops)
				button.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					semantic.Button.Add(gtx.Ops)
					semantic.LabelOp("Button").Add(gtx.Ops)
					return layout.Dimensions{Size: image.Pt(60, 20)}
				})
				offset.Pop()
				inner.Pop()
				outer.Pop()
				return layout.Dimensions{Size: gtx.Constraints.Max}
			})
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()

			for _, byID := range []bool{false, true} {
				var matches []Node
				findLabel(d.Tree(), "Button", &matches)
				if len(matches) != 1 || matches[0].Bounds != (Bounds{MinX: 60, MinY: 20, MaxX: 120, MaxY: 40}) {
					t.Fatalf("button semantics = %+v, want unclipped bounds", matches)
				}
				target := ClickTarget{Label: "Button"}
				if byID {
					target = ClickTarget{ID: &matches[0].ID}
				}
				before := clicks
				if err := d.Click(target); err != nil || clicks != before+1 {
					t.Fatalf("click = %v, clicks %d -> %d; want widget activation", err, before, clicks)
				}
			}
		})
	}
}

func TestClickRejectsTargetCoveredByAnotherControl(t *testing.T) {
	var below, popup widget.Clickable
	belowClicks, popupClicks := 0, 0
	popupWidth := 100
	d, err := New(Config{Width: 100, Height: 100, PxPerDp: 1, PxPerSp: 1}, func(gtx layout.Context) layout.Dimensions {
		if below.Clicked(gtx) {
			belowClicks++
		}
		if popup.Clicked(gtx) {
			popupClicks++
		}
		below.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			semantic.Button.Add(gtx.Ops)
			semantic.LabelOp("Play").Add(gtx.Ops)
			return layout.Dimensions{Size: image.Pt(100, 100)}
		})
		popup.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			semantic.Button.Add(gtx.Ops)
			semantic.LabelOp("Subtitles").Add(gtx.Ops)
			return layout.Dimensions{Size: image.Pt(popupWidth, 100)}
		})
		return layout.Dimensions{Size: gtx.Constraints.Max}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	err = d.Click(ClickTarget{Label: "Play"})
	if err == nil || !strings.Contains(err.Error(), `covered by`) || !strings.Contains(err.Error(), `"Subtitles"`) {
		t.Fatalf("click covered target = %v, want covered error", err)
	}
	if belowClicks != 0 || popupClicks != 0 {
		t.Fatalf("clicks below=%d popup=%d, want none", belowClicks, popupClicks)
	}

	// Once the popup only covers the left half, the target's center is free.
	popupWidth = 40
	if err := d.Refresh(); err != nil {
		t.Fatal(err)
	}
	if err := d.Click(ClickTarget{Label: "Play"}); err != nil || belowClicks != 1 || popupClicks != 0 {
		t.Fatalf("click = %v, clicks below=%d popup=%d; want only the target", err, belowClicks, popupClicks)
	}
}

func TestSemanticEditorClickFocusesEditor(t *testing.T) {
	var editor widget.Editor
	editor.SingleLine = true
	theme := material.NewTheme()
	theme.Shaper = text.NewShaper(text.NoSystemFonts(), text.WithCollection(gofont.Collection()))
	blur := false
	d, err := New(Config{Width: 240, Height: 40, PxPerDp: 1, PxPerSp: 1}, func(gtx layout.Context) layout.Dimensions {
		if blur {
			blur = false
			gtx.Execute(key.FocusCmd{})
		}
		return material.Editor(theme, &editor, "Search").Layout(gtx)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	var ids []uint64
	var visit func([]Node)
	visit = func(nodes []Node) {
		for _, node := range nodes {
			if node.Class == "editor" {
				ids = append(ids, node.ID)
			}
			visit(node.Children)
		}
	}
	visit(d.Tree())
	if len(ids) != 1 {
		t.Fatalf("editor semantic IDs = %v, want one", ids)
	}
	if err := d.Type("lost"); err == nil {
		t.Fatal("typed into an unfocused editor")
	}
	if err := d.Click(ClickTarget{ID: &ids[0]}); err != nil {
		t.Fatal(err)
	}
	if !d.router.Source().Focused(&editor) {
		t.Fatal("semantic click did not focus the editor")
	}
	// Type twice: the second call must not depend on a changed input hint.
	if err := d.Type("search "); err != nil {
		t.Fatal(err)
	}
	if err := d.Type("text"); err != nil {
		t.Fatal(err)
	}
	if got := editor.Text(); got != "search text" {
		t.Fatalf("editor text = %q, want search text", got)
	}
	blur = true
	if err := d.Frame(); err != nil {
		t.Fatal(err)
	}
	if err := d.Type("lost"); err == nil {
		t.Fatal("typed into an editor that lost focus")
	}
}

func TestScreenshotHonorsScheduledFrames(t *testing.T) {
	var started time.Time
	finished := false
	d, err := New(Config{Width: 320, Height: 200, PxPerDp: 1, PxPerSp: 1}, func(gtx layout.Context) layout.Dimensions {
		if started.IsZero() {
			started = gtx.Now
		}
		finished = gtx.Now.Sub(started) >= 60*time.Millisecond
		if !finished {
			gtx.Execute(op.InvalidateCmd{At: started.Add(60 * time.Millisecond)})
		}
		return layout.Dimensions{Size: gtx.Constraints.Max}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if finished {
		t.Fatal("fixture animation finished in first frame")
	}
	initialFrame := d.FrameNumber()
	if err := d.Screenshot(filepath.Join(t.TempDir(), "animation.png")); err != nil {
		t.Fatal(err)
	}
	if !finished || d.FrameNumber() <= initialFrame {
		t.Fatal("screenshot did not advance scheduled animation without input")
	}
}

func TestRefreshBoundsContinuousInvalidation(t *testing.T) {
	d, err := New(Config{Width: 100, Height: 100, PxPerDp: 1, PxPerSp: 1}, func(gtx layout.Context) layout.Dimensions {
		gtx.Execute(op.InvalidateCmd{})
		return layout.Dimensions{Size: gtx.Constraints.Max}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	started := time.Now()
	if err := d.Refresh(); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("Refresh did not bound a continuously invalidating UI")
	}
}

// TestSettleKeepsScheduledRedraw checks that Settle renders immediate redraws
// only, and that a scheduled one it sees stays pending for the next frame.
func TestSettleKeepsScheduledRedraw(t *testing.T) {
	frames := 0
	immediate := false
	d, err := New(Config{Width: 100, Height: 100, PxPerDp: 1, PxPerSp: 1}, func(gtx layout.Context) layout.Dimensions {
		frames++
		if immediate {
			immediate = false
			gtx.Execute(op.InvalidateCmd{})
		} else {
			gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(time.Hour)})
		}
		return layout.Dimensions{Size: gtx.Constraints.Max}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	immediate = true
	if err := d.Frame(); err != nil {
		t.Fatal(err)
	}
	frames = 0
	if err := d.Settle(); err != nil {
		t.Fatal(err)
	}
	if frames != 1 {
		t.Fatalf("Settle rendered %d frames for one immediate redraw, want 1", frames)
	}
	if err := d.Settle(); err != nil {
		t.Fatal(err)
	}
	if frames != 1 {
		t.Fatalf("Settle rendered a frame for a scheduled redraw")
	}
	if !d.hasNextFrame || time.Until(d.nextFrame) < 30*time.Minute {
		t.Fatalf("scheduled redraw was dropped: pending %v at %v", d.hasNextFrame, d.nextFrame)
	}
}

func TestLetterKeyNames(t *testing.T) {
	for _, name := range []string{"s", "S", "r", "R", "h", "j", "k", "l", "q"} {
		got, err := keyName(name)
		if err != nil {
			t.Fatal(err)
		}
		want := name[0]
		if want >= 'a' && want <= 'z' {
			want -= 'a' - 'A'
		}
		if got != key.Name(string(want)) {
			t.Fatalf("keyName(%q) = %q", name, got)
		}
	}
	for _, name := range []string{"", "Refresh", "Sss"} {
		if _, err := keyName(name); err == nil {
			t.Fatalf("invalid key %q accepted", name)
		}
	}
}

func TestConfigValidation(t *testing.T) {
	if _, err := New(Config{Width: 0, Height: 1, PxPerDp: 1, PxPerSp: 1}, func(layout.Context) layout.Dimensions {
		return layout.Dimensions{}
	}); err == nil {
		t.Fatal("expected invalid width error")
	}
}

func TestSelectedNamesSelections(t *testing.T) {
	tree := []Node{{
		ID: 1, Bounds: Bounds{MaxX: 1000, MaxY: 1000},
		Children: []Node{
			// A selected row inside a button takes the button's label.
			{ID: 2, Class: "button", Label: "Movies", Bounds: Bounds{MaxX: 100, MaxY: 20}, Children: []Node{
				{ID: 3, Selected: true, Bounds: Bounds{MaxX: 100, MaxY: 20}, Children: []Node{{ID: 4, Label: "Movies · 2"}}},
			}},
			// A plain label does not name a later sibling's selection.
			{ID: 5, Label: "Header"},
			// Without a control, descendant labels name it; bounds are clipped.
			{ID: 6, Selected: true, Bounds: Bounds{MinX: 50, MinY: 90, MaxX: 150, MaxY: 120}, Children: []Node{
				{ID: 7, Label: "S01E01"}, {ID: 8, Children: []Node{{ID: 9, Label: "Pilot"}}},
			}},
			{ID: 10, Selected: true, Label: "Own", Bounds: Bounds{MinX: 200, MaxX: 300, MaxY: 10}},
			// A nearer unlabelled control hides an outer control's label.
			{ID: 11, Class: "button", Label: "Card", Children: []Node{
				{ID: 12, Gestures: []string{"click"}, Children: []Node{
					{ID: 13, Selected: true, Children: []Node{{ID: 14, Label: "Row"}}},
				}},
				// So does a selected node that is itself an unlabelled control.
				{ID: 15, Selected: true, Gestures: []string{"click"}, Children: []Node{{ID: 16, Label: "Inner"}}},
			}},
		},
	}}
	got := Selected(tree, image.Rect(0, 0, 100, 100))
	want := []Selection{
		{ID: 3, Label: "Movies", Bounds: Bounds{MaxX: 100, MaxY: 20}},
		{ID: 6, Label: "S01E01 · Pilot", Bounds: Bounds{MinX: 50, MinY: 90, MaxX: 100, MaxY: 100}},
		{ID: 10, Label: "Own"},
		{ID: 13, Label: "Row"},
		{ID: 15, Label: "Inner"},
	}
	if len(got) != len(want) {
		t.Fatalf("Selected() = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Selected()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	if got := Selected(nil, image.Rect(0, 0, 10, 10)); got == nil || len(got) != 0 {
		t.Fatalf("Selected(nil) = %#v, want an empty slice", got)
	}
}

func TestScreenshotCropAndScale(t *testing.T) {
	d, err := New(Config{Width: 200, Height: 100, PxPerDp: 1, PxPerSp: 1}, func(gtx layout.Context) layout.Dimensions {
		return layout.Dimensions{Size: gtx.Constraints.Max}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	dir := t.TempDir()
	for _, tt := range []struct {
		opts ScreenshotOptions
		want image.Point
	}{
		{ScreenshotOptions{}, image.Pt(200, 100)},
		{ScreenshotOptions{Scale: 1}, image.Pt(200, 100)},
		{ScreenshotOptions{Crop: image.Rect(10, 20, 110, 60)}, image.Pt(100, 40)},
		{ScreenshotOptions{Crop: image.Rect(150, 50, 400, 400)}, image.Pt(50, 50)},
		{ScreenshotOptions{Crop: image.Rect(0, 0, 100, 50), Scale: 0.5}, image.Pt(50, 25)},
	} {
		var buf bytes.Buffer
		if err := d.WriteScreenshot(&buf, tt.opts); err != nil {
			t.Fatalf("WriteScreenshot(%+v) = %v", tt.opts, err)
		}
		config, err := png.DecodeConfig(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if got := image.Pt(config.Width, config.Height); got != tt.want {
			t.Fatalf("WriteScreenshot(%+v) size = %v, want %v", tt.opts, got, tt.want)
		}
	}
	for _, opts := range []ScreenshotOptions{
		{Scale: 1.5},
		{Scale: -1},
		{Scale: math.NaN()},
		{Crop: image.Rect(300, 300, 310, 310)},
		{Crop: image.Rectangle{Min: image.Pt(100, 50), Max: image.Pt(50, 20)}},
		{Crop: image.Rect(10, 10, 10, 50)},
	} {
		path := filepath.Join(dir, "bad.png")
		if _, err := d.CreateScreenshot(path, opts); err == nil {
			t.Fatalf("CreateScreenshot(%+v) succeeded", opts)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("CreateScreenshot(%+v) left %s behind: %v", opts, path, err)
		}
	}
}

func TestScreenshotKeepsExistingFileOnFailure(t *testing.T) {
	d, err := New(Config{Width: 20, Height: 20, PxPerDp: 1, PxPerSp: 1}, func(gtx layout.Context) layout.Dimensions {
		return layout.Dimensions{Size: gtx.Constraints.Max}
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(path, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	d.Close()
	if err := d.Screenshot(path); err == nil {
		t.Fatal("Screenshot on a closed driver succeeded")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "keep" {
		t.Fatalf("existing file = %q, %v; want it untouched", data, err)
	}
}

func TestWaitIdleRendersUntilLoaded(t *testing.T) {
	frames, loadedAt := 0, 3
	d, err := New(Config{
		Width: 16, Height: 16, PxPerDp: 1, PxPerSp: 1,
		Loading: func() bool { return frames < loadedAt },
	}, func(gtx layout.Context) layout.Dimensions {
		frames++
		return layout.Dimensions{Size: gtx.Constraints.Max}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	idle, err := d.WaitIdle(time.Second)
	if err != nil || !idle || frames != loadedAt {
		t.Fatalf("WaitIdle() = %v, %v after %d frames, want idle after %d", idle, err, frames, loadedAt)
	}

	loadedAt = math.MaxInt
	start := time.Now()
	idle, err = d.WaitIdle(60 * time.Millisecond)
	if err != nil || idle {
		t.Fatalf("WaitIdle(still loading) = %v, %v, want not idle", idle, err)
	}
	if elapsed := time.Since(start); elapsed < 60*time.Millisecond || elapsed > time.Second {
		t.Fatalf("WaitIdle(60ms) took %v", elapsed)
	}
}

func TestTabMovesFocusOnlyWhenUnhandled(t *testing.T) {
	for _, handled := range []bool{false, true} {
		tag := new(bool)
		tabs, focused := 0, false
		d, err := New(Config{Width: 100, Height: 100, PxPerDp: 1, PxPerSp: 1}, func(gtx layout.Context) layout.Dimensions {
			defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
			event.Op(gtx.Ops, tag)
			filters := []event.Filter{key.FocusFilter{Target: tag}}
			if handled {
				filters = append(filters, key.Filter{Name: key.NameTab})
			}
			for {
				e, ok := gtx.Event(filters...)
				if !ok {
					break
				}
				if _, ok := e.(key.Event); ok {
					tabs++
				}
			}
			focused = gtx.Focused(tag)
			return layout.Dimensions{Size: gtx.Constraints.Max}
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Key("Tab", nil); err != nil {
			t.Fatal(err)
		}
		if err := d.Frame(); err != nil { // observe a focus move made after layout
			t.Fatal(err)
		}
		d.Close()
		if handled && (tabs != 1 || focused) {
			t.Errorf("handled Tab: tabs=%d focused=%v, want 1 and no focus move", tabs, focused)
		}
		if !handled && (tabs != 0 || !focused) {
			t.Errorf("unhandled Tab: tabs=%d focused=%v, want focus moved", tabs, focused)
		}
	}
}
