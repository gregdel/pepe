package driver

import (
	"fmt"
	"image"

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
		bounds := image.Rectangle{
			Min: image.Pt(node.Bounds.MinX, node.Bounds.MinY),
			Max: image.Pt(node.Bounds.MaxX, node.Bounds.MaxY),
		}
		clipped := visible.Intersect(bounds)
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
