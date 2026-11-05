package driver

import (
	"image"
	"reflect"
	"testing"

	"gioui.org/io/input"
	"gioui.org/io/semantic"
)

func TestNormalizeTreeUsesRootSubtree(t *testing.T) {
	for _, nodes := range [][]input.SemanticNode{nil, {}} {
		if got := normalizeTree(nodes); got == nil || len(got) != 0 {
			t.Fatalf("empty input produced %#v, want a non-nil empty slice", got)
		}
	}

	label := input.SemanticNode{ID: 3, ParentID: 2, Desc: input.SemanticDesc{Label: "Title", Bounds: image.Rect(5, 6, 30, 20)}}
	button := input.SemanticNode{
		ID: 2, ParentID: 1, Children: []input.SemanticNode{label},
		Desc: input.SemanticDesc{Class: semantic.Button, Label: "Title", Description: "Open", Bounds: image.Rect(0, 0, 40, 30), Selected: true, Disabled: true, Gestures: input.ClickGesture | input.ScrollGesture},
	}
	root := input.SemanticNode{ID: 1, Children: []input.SemanticNode{button}, Desc: input.SemanticDesc{Bounds: image.Rect(0, 0, 100, 100)}}
	// AppendSemantics returns the root and the same descendants in a flat slice.
	got := normalizeTree([]input.SemanticNode{root, button, label})
	want := []Node{{ID: 1, Class: "unknown", Bounds: Bounds{MaxX: 100, MaxY: 100}, Children: []Node{{
		ID: 2, ParentID: 1, Class: "button", Label: "Title", Description: "Open",
		Bounds: Bounds{MaxX: 40, MaxY: 30}, Selected: true, Disabled: true, Gestures: []string{"click", "scroll"},
		Children: []Node{{ID: 3, ParentID: 2, Class: "unknown", Label: "Title", Bounds: Bounds{MinX: 5, MinY: 6, MaxX: 30, MaxY: 20}}},
	}}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized tree = %#v, want %#v", got, want)
	}
}
