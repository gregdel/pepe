package main

import "strings"

// Section represents which main section we're in
type Section int

const (
	SectionMovies Section = iota
	SectionShows
)

// NavNode is a common interface for both RadixNode and DynamicNode
type NavNode interface {
	GetLabel() string
	GetSubtreeCount() int
	IsTerminalNode() bool
}

// NavigationState tracks where we are in the navigation tree
type NavigationState struct {
	section               Section        // Current section (Movies or Shows)
	path                  []*RadixNode   // Path from root to current position in radix tree
	dynamicPath           []*DynamicNode // Path in dynamic navigation (seasons/episodes)
	cursor                int            // Current cursor position in the children list
	inDynamicNav          bool           // True when navigating seasons/episodes
	dynamicTitle          string         // Show title for dynamic navigation
	dynamicShowID         string         // ImdbID of show being navigated
	dynamicFormattedTitle string         // Formatted title (with year) of the show in the radix tree
}

func newNavigationState() *NavigationState {
	return &NavigationState{
		section:       SectionMovies,
		path:          []*RadixNode{},
		dynamicPath:   []*DynamicNode{},
		cursor:        0,
		inDynamicNav:  false,
		dynamicTitle:  "",
		dynamicShowID: "",
	}
}

// isAtRoot returns true if we're at the root level (selecting Movies/Shows)
func (n *NavigationState) isAtRoot() bool {
	return len(n.path) == 0 && !n.inDynamicNav
}

// isAtSectionRoot returns true if we're at the root of a section (first level in tree)
func (n *NavigationState) isAtSectionRoot() bool {
	return len(n.path) == 1
}

// getCurrentNode returns the current node in the tree, or nil if at root
func (n *NavigationState) getCurrentNode() *RadixNode {
	if len(n.path) == 0 {
		return nil
	}
	return n.path[len(n.path)-1]
}

// getSelectedChild returns the currently selected child node, or nil if none
func (n *NavigationState) getSelectedChild() *RadixNode {
	current := n.getCurrentNode()
	if current == nil {
		return nil
	}

	if n.cursor >= 0 && n.cursor < len(current.Children) {
		return current.Children[n.cursor]
	}

	return nil
}

// goBack moves up one level in the tree
func (n *NavigationState) goBack() {
	if n.inDynamicNav {
		// In dynamic nav, go back through dynamic path
		if len(n.dynamicPath) > 0 {
			n.dynamicPath = n.dynamicPath[:len(n.dynamicPath)-1]
			n.cursor = 0

			// If we've exhausted dynamic path, return to radix tree
			if len(n.dynamicPath) == 0 {
				n.inDynamicNav = false
				n.dynamicTitle = ""
				n.dynamicShowID = ""
				n.dynamicFormattedTitle = ""
			}
		}
	} else {
		// In radix tree nav
		if len(n.path) > 0 {
			n.path = n.path[:len(n.path)-1]
			n.cursor = 0
		}
	}
}

// goForward enters the currently selected child node
func (n *NavigationState) goForward(child *RadixNode) {
	if child != nil {
		n.path = append(n.path, child)
		n.cursor = 0
	}
}

// resetToSection resets navigation to the root of the given section
func (n *NavigationState) resetToSection(section Section, rootNode *RadixNode) {
	n.section = section
	if rootNode != nil {
		n.path = []*RadixNode{rootNode}
	} else {
		n.path = []*RadixNode{}
	}
	n.cursor = 0
	n.inDynamicNav = false
	n.dynamicPath = []*DynamicNode{}
	n.dynamicTitle = ""
	n.dynamicShowID = ""
	n.dynamicFormattedTitle = ""
}

// moveCursorUp moves the cursor up (decrements), bounded at 0
func (n *NavigationState) moveCursorUp() {
	if n.cursor > 0 {
		n.cursor--
	}
}

// moveCursorDown moves the cursor down (increments), bounded by maxChildren
func (n *NavigationState) moveCursorDown(maxChildren int) {
	if n.cursor < maxChildren-1 {
		n.cursor++
	}
}

// getChildrenCount returns the number of children for the current node
func (n *NavigationState) getChildrenCount() int {
	if n.inDynamicNav {
		current := n.getCurrentDynamicNode()
		if current == nil {
			return 0
		}
		return len(current.Children)
	}

	current := n.getCurrentNode()
	if current == nil {
		return 0
	}
	return len(current.Children)
}

// getCurrentPath returns the accumulated path string (current prefix being explored)
func (n *NavigationState) getCurrentPath() string {
	// Don't show path in dynamic navigation
	if n.inDynamicNav {
		return ""
	}

	if len(n.path) <= 1 {
		// At root or section root, no path to show
		return ""
	}

	// Accumulate labels from path (skip root which has empty label)
	var pathStr string
	for i := 1; i < len(n.path); i++ {
		pathStr += n.path[i].Label
	}

	return pathStr
}

// getCurrentDynamicNode returns the current node in dynamic navigation
func (n *NavigationState) getCurrentDynamicNode() *DynamicNode {
	if len(n.dynamicPath) == 0 {
		return nil
	}
	return n.dynamicPath[len(n.dynamicPath)-1]
}

// getSelectedDynamicChild returns the currently selected dynamic child
func (n *NavigationState) getSelectedDynamicChild() *DynamicNode {
	current := n.getCurrentDynamicNode()
	if current == nil {
		return nil
	}

	if n.cursor >= 0 && n.cursor < len(current.Children) {
		return current.Children[n.cursor]
	}

	return nil
}

// navigateToTitle walks the radix tree rooted at root to the terminal node
// whose FullString == target, reconstructing path and cursor.
// Returns true on success; on failure path is left in a partial state — caller
// should reset navigation.
func (n *NavigationState) navigateToTitle(root *RadixNode, target string) bool {
	n.path = []*RadixNode{root}
	n.cursor = 0
	remaining := target

	for remaining != "" {
		current := n.path[len(n.path)-1]
		found := false
		for i, child := range current.Children {
			if strings.HasPrefix(remaining, child.Label) {
				rest := remaining[len(child.Label):]
				n.cursor = i
				if rest == "" {
					return child.IsTerminal
				}
				n.path = append(n.path, child)
				n.cursor = 0
				remaining = rest
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return false
}

// enterDynamicNav enters dynamic navigation mode with season nodes
func (n *NavigationState) enterDynamicNav(seasonNodes []*DynamicNode, showTitle, showID, formattedTitle string) {
	// Create a virtual root node for seasons
	root := &DynamicNode{
		Type:         DynamicNodeSeason,
		Label:        "Seasons",
		Children:     seasonNodes,
		SubtreeCount: len(seasonNodes),
	}

	n.inDynamicNav = true
	n.dynamicPath = []*DynamicNode{root}
	n.cursor = 0
	n.dynamicTitle = showTitle
	n.dynamicShowID = showID
	n.dynamicFormattedTitle = formattedTitle
}

// goForwardDynamic enters the selected dynamic child
func (n *NavigationState) goForwardDynamic(child *DynamicNode) {
	if child != nil {
		n.dynamicPath = append(n.dynamicPath, child)
		n.cursor = 0
	}
}
