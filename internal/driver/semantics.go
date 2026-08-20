package driver

import (
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
	result := make([]Node, 0, len(nodes))
	for _, node := range nodes {
		result = append(result, normalizeNode(node))
	}
	return result
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

func findNode(nodes []Node, id uint64) (Node, bool) {
	for _, node := range nodes {
		if node.ID == id {
			return node, true
		}
		if found, ok := findNode(node.Children, id); ok {
			return found, true
		}
	}
	return Node{}, false
}

func findLabel(nodes []Node, label string, matches *[]Node) {
	for _, node := range nodes {
		if node.Label == label {
			*matches = append(*matches, node)
		}
		findLabel(node.Children, label, matches)
	}
}
