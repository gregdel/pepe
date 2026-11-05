package driver

import (
	"image"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/io/key"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/widget"
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
