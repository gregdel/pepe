package driver

import (
	"fmt"
	"image"
	"strings"

	"gioui.org/io/input"
	"gioui.org/io/semantic"
)

type Node struct {
	ID          uint64   `json:"id"`
	ParentID    uint64   `json:"parentId,omitempty"`
	Class       string   `json:"class,omitempty"`
	Label       string   `json:"label,omitempty"`
	Description string   `json:"description,omitempty"`
	Bounds      Bounds   `json:"bounds"`
	Selected    bool     `json:"selected,omitempty"`
	Disabled    bool     `json:"disabled,omitempty"`
	Gestures    []string `json:"gestures,omitempty"`
	Children    []Node   `json:"children,omitempty"`
}

type Bounds struct {
	MinX int `json:"minX"`
	MinY int `json:"minY"`
	MaxX int `json:"maxX"`
	MaxY int `json:"maxY"`
}

func normalizeTree(nodes []input.SemanticNode) []Node {
	if len(nodes) == 0 {
		return []Node{}
	}
	// Gio returns the root first, followed by its descendants as a flat list.
	return []Node{normalizeNode(nodes[0])}
}

func normalizeNode(node input.SemanticNode) Node {
	result := Node{
		ID:          uint64(node.ID),
		ParentID:    uint64(node.ParentID),
		Class:       semanticClass(node.Desc.Class),
		Label:       node.Desc.Label,
		Description: node.Desc.Description,
		Bounds:      bounds(node.Desc.Bounds),
		Selected:    node.Desc.Selected,
		Disabled:    node.Desc.Disabled,
	}
	if node.Desc.Gestures&input.ClickGesture != 0 {
		result.Gestures = append(result.Gestures, "click")
	}
	if node.Desc.Gestures&input.ScrollGesture != 0 {
		result.Gestures = append(result.Gestures, "scroll")
	}
	for _, child := range node.Children {
		result.Children = append(result.Children, normalizeNode(child))
	}
	return result
}

func bounds(rect image.Rectangle) Bounds {
	return Bounds{MinX: rect.Min.X, MinY: rect.Min.Y, MaxX: rect.Max.X, MaxY: rect.Max.Y}
}

// Rect returns b as an image.Rectangle.
func (b Bounds) Rect() image.Rectangle {
	return image.Rect(b.MinX, b.MinY, b.MaxX, b.MaxY)
}

func semanticClass(class semantic.ClassOp) string {
	switch class {
	case semantic.Unknown:
		return "unknown"
	case semantic.Button:
		return "button"
	case semantic.CheckBox:
		return "checkbox"
	case semantic.Editor:
		return "editor"
	case semantic.RadioButton:
		return "radio"
	case semantic.Switch:
		return "switch"
	default:
		return "unknown"
	}
}

// Semantic bounds are not clipped to ancestors. Keep their intersection with
// the viewport so a nested label cannot target a point outside its container.
// This is only a candidate area: non-semantic clips and occluding controls
// require the router's hit testing.
func findNode(nodes []Node, id uint64, visible image.Rectangle) (Node, image.Rectangle, bool) {
	for _, node := range nodes {
		clipped := visible.Intersect(node.Bounds.Rect())
		if node.ID == id {
			return node, clipped, true
		}
		if found, area, ok := findNode(node.Children, id, clipped); ok {
			return found, area, true
		}
	}
	return Node{}, image.Rectangle{}, false
}

func findLabel(nodes []Node, label string, matches *[]Node) {
	for _, node := range nodes {
		if node.Label == label {
			*matches = append(*matches, node)
		}
		findLabel(node.Children, label, matches)
	}
}

// coveringControl returns the control that owns semantic node hit when it is
// unrelated to target: the nearest control at or above hit that is neither
// target, inside target, nor one of target's ancestors. Plain labels, such as
// an editor's hint, have no owning control and return nil.
func coveringControl(tree []Node, target Node, hit uint64) *Node {
	path := pathTo(tree, hit)
	for i := len(path) - 1; i >= 0; i-- {
		n := path[i]
		if n.ID == target.ID {
			return nil
		}
		if !isControl(n) {
			continue
		}
		if _, _, ok := findNode([]Node{n}, target.ID, image.Rectangle{}); ok {
			return nil
		}
		return &n
	}
	return nil
}

// pathTo returns the nodes from a root down to the node with id, or nil.
func pathTo(nodes []Node, id uint64) []Node {
	for _, node := range nodes {
		if node.ID == id {
			return []Node{node}
		}
		if path := pathTo(node.Children, id); path != nil {
			return append([]Node{node}, path...)
		}
	}
	return nil
}

// HasClass reports whether the node has a known semantic class.
func (n Node) HasClass() bool {
	return n.Class != "" && n.Class != "unknown"
}

func isControl(node Node) bool {
	if node.HasClass() {
		return true
	}
	for _, gesture := range node.Gestures {
		if gesture == "click" {
			return true
		}
	}
	return false
}

func describeNode(node Node) string {
	if node.Label != "" {
		return fmt.Sprintf("#%d %q", node.ID, node.Label)
	}
	return fmt.Sprintf("#%d", node.ID)
}

// Selection summarizes one selected semantic node, such as the D-pad focus.
type Selection struct {
	ID    uint64 `json:"id"`
	Label string `json:"label,omitempty"`
	// Bounds is the area left visible by the viewport and every ancestor.
	Bounds Bounds `json:"bounds"`
}

// Selected returns every selected node in depth-first order. A selection is
// named after its own label, else the nearest labelled control containing it
// (such as a row's button), else its descendants' labels joined with " · ".
// An unlabelled control in between hides labels of controls further out.
func Selected(tree []Node, viewport image.Rectangle) []Selection {
	selections := []Selection{}
	var walk func(nodes []Node, clip image.Rectangle, control string)
	walk = func(nodes []Node, clip image.Rectangle, control string) {
		for _, node := range nodes {
			visible := clip.Intersect(node.Bounds.Rect())
			if node.Selected {
				name := node.Label
				// An unlabelled selected control shadows the controls
				// further out, like it does for its own contents.
				if name == "" && !isControl(node) {
					name = control
				}
				if name == "" {
					name = strings.Join(descendantLabels(node.Children, nil), " · ")
				}
				selections = append(selections, Selection{ID: node.ID, Label: name, Bounds: bounds(visible)})
			}
			// The nearest control names its contents, even when it is
			// unlabelled and they fall back to their own labels.
			childControl := control
			if isControl(node) {
				childControl = node.Label
			}
			walk(node.Children, visible, childControl)
		}
	}
	walk(tree, viewport, "")
	return selections
}

func descendantLabels(nodes []Node, labels []string) []string {
	for _, node := range nodes {
		if node.Label != "" {
			labels = append(labels, node.Label)
		}
		labels = descendantLabels(node.Children, labels)
	}
	return labels
}
