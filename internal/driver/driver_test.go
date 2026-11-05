package driver

import (
	"image"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/font/gofont"
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

func TestSemanticEditorClickFocusesEditor(t *testing.T) {
	var editor widget.Editor
	editor.SingleLine = true
	theme := material.NewTheme()
	theme.Shaper = text.NewShaper(text.NoSystemFonts(), text.WithCollection(gofont.Collection()))
	d, err := New(Config{Width: 240, Height: 40, PxPerDp: 1, PxPerSp: 1}, func(gtx layout.Context) layout.Dimensions {
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
	if err := d.Click(ClickTarget{ID: &ids[0]}); err != nil {
		t.Fatal(err)
	}
	if !d.router.Source().Focused(&editor) {
		t.Fatal("semantic click did not focus the editor")
	}
	d.router.Queue(key.EditEvent{Text: "search text"})
	if err := d.Frame(); err != nil {
		t.Fatal(err)
	}
	if got := editor.Text(); got != "search text" {
		t.Fatalf("editor text = %q, want search text", got)
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
