package app

import (
	"testing"
)

func TestNavigationState_isAtRoot(t *testing.T) {
	nav := newNavigationState()

	if !nav.isAtRoot() {
		t.Error("Expected new navigation state to be at root")
	}

	// Add a node to path
	nav.path = []*RadixNode{{Label: "test"}}

	if nav.isAtRoot() {
		t.Error("Expected navigation state to not be at root after adding node")
	}
}

func TestNavigationState_isAtSectionRoot(t *testing.T) {
	nav := newNavigationState()

	if nav.isAtSectionRoot() {
		t.Error("Expected new navigation state to not be at section root")
	}

	nav.path = []*RadixNode{{Label: "root"}}

	if !nav.isAtSectionRoot() {
		t.Error("Expected navigation state with one node to be at section root")
	}

	nav.path = append(nav.path, &RadixNode{Label: "child"})

	if nav.isAtSectionRoot() {
		t.Error("Expected navigation state with two nodes to not be at section root")
	}
}

func TestNavigationState_getCurrentNode(t *testing.T) {
	nav := newNavigationState()

	if nav.getCurrentNode() != nil {
		t.Error("Expected getCurrentNode to return nil when path is empty")
	}

	node := &RadixNode{Label: "test"}
	nav.path = []*RadixNode{node}

	if nav.getCurrentNode() != node {
		t.Error("Expected getCurrentNode to return the last node in path")
	}
}

func TestNavigationState_getSelectedChild(t *testing.T) {
	nav := newNavigationState()

	// Empty path
	if nav.getSelectedChild() != nil {
		t.Error("Expected getSelectedChild to return nil when path is empty")
	}

	// Node with children
	child1 := &RadixNode{Label: "child1"}
	child2 := &RadixNode{Label: "child2"}
	parent := &RadixNode{
		Label:    "parent",
		Children: []*RadixNode{child1, child2},
	}

	nav.path = []*RadixNode{parent}
	nav.cursor = 0

	if nav.getSelectedChild() != child1 {
		t.Error("Expected getSelectedChild to return first child when cursor is 0")
	}

	nav.cursor = 1
	if nav.getSelectedChild() != child2 {
		t.Error("Expected getSelectedChild to return second child when cursor is 1")
	}

	nav.cursor = 2
	if nav.getSelectedChild() != nil {
		t.Error("Expected getSelectedChild to return nil when cursor is out of bounds")
	}
}

func TestNavigationState_goBack(t *testing.T) {
	nav := newNavigationState()
	node1 := &RadixNode{Label: "node1"}
	node2 := &RadixNode{Label: "node2"}

	nav.path = []*RadixNode{node1, node2}
	nav.cursor = 5

	nav.goBack()

	if len(nav.path) != 1 {
		t.Errorf("Expected path length 1, got %d", len(nav.path))
	}

	if nav.path[0] != node1 {
		t.Error("Expected first node to remain in path")
	}

	if nav.cursor != 0 {
		t.Errorf("Expected cursor to be reset to 0, got %d", nav.cursor)
	}
}

func TestNavigationState_goForward(t *testing.T) {
	nav := newNavigationState()
	node1 := &RadixNode{Label: "node1"}
	node2 := &RadixNode{Label: "node2"}

	nav.path = []*RadixNode{node1}
	nav.cursor = 5

	nav.goForward(node2)

	if len(nav.path) != 2 {
		t.Errorf("Expected path length 2, got %d", len(nav.path))
	}

	if nav.path[1] != node2 {
		t.Error("Expected second node to be added to path")
	}

	if nav.cursor != 0 {
		t.Errorf("Expected cursor to be reset to 0, got %d", nav.cursor)
	}
}

func TestNavigationState_resetToSection(t *testing.T) {
	nav := newNavigationState()
	nav.section = SectionShows
	nav.path = []*RadixNode{{Label: "old"}}
	nav.cursor = 5

	rootNode := &RadixNode{Label: "root"}
	nav.resetToSection(SectionMovies, rootNode)

	if nav.section != SectionMovies {
		t.Error("Expected section to be set to Movies")
	}

	if len(nav.path) != 1 || nav.path[0] != rootNode {
		t.Error("Expected path to contain only the root node")
	}

	if nav.cursor != 0 {
		t.Errorf("Expected cursor to be reset to 0, got %d", nav.cursor)
	}
}

func TestNavigationStateReturnToHomeKeepsSectionSelected(t *testing.T) {
	nav := newNavigationState()
	nav.resetToSection(SectionShows, NewRadixTree([]string{"show"}).Root())
	nav.goForward(nav.getSelectedChild())

	nav.returnToHome()

	if !nav.isAtRoot() || nav.cursor != int(SectionShows) {
		t.Errorf("home state = (root=%t, cursor=%d), want Shows selected at home", nav.isAtRoot(), nav.cursor)
	}
}

func TestNavigationState_moveCursorUp(t *testing.T) {
	nav := newNavigationState()
	nav.cursor = 5

	nav.moveCursorUp(10)
	if nav.cursor != 4 {
		t.Errorf("Expected cursor to be 4, got %d", nav.cursor)
	}

	nav.cursor = 0
	nav.moveCursorUp(10)
	if nav.cursor != 9 {
		t.Errorf("Expected cursor to wrap to 9, got %d", nav.cursor)
	}

	nav.moveCursorUp(0)
	if nav.cursor != 9 {
		t.Errorf("Expected cursor to stay at 9 for an empty list, got %d", nav.cursor)
	}
}

func TestNavigationState_moveCursorDown(t *testing.T) {
	nav := newNavigationState()
	nav.cursor = 0

	nav.moveCursorDown(10)
	if nav.cursor != 1 {
		t.Errorf("Expected cursor to be 1, got %d", nav.cursor)
	}

	nav.cursor = 9
	nav.moveCursorDown(10)
	if nav.cursor != 0 {
		t.Errorf("Expected cursor to wrap to 0, got %d", nav.cursor)
	}

	nav.moveCursorDown(0)
	if nav.cursor != 0 {
		t.Errorf("Expected cursor to stay at 0 for an empty list, got %d", nav.cursor)
	}
}

func TestNavigationState_getChildrenCount(t *testing.T) {
	nav := newNavigationState()

	// Empty path
	if nav.getChildrenCount() != 0 {
		t.Error("Expected children count to be 0 when path is empty")
	}

	// Node with children
	node := &RadixNode{
		Label:    "parent",
		Children: []*RadixNode{{Label: "c1"}, {Label: "c2"}, {Label: "c3"}},
	}

	nav.path = []*RadixNode{node}

	if nav.getChildrenCount() != 3 {
		t.Errorf("Expected children count to be 3, got %d", nav.getChildrenCount())
	}
}

func TestNavigationStateNavigateToMediaDistinguishesDuplicateTitles(t *testing.T) {
	first := &MediaItem{ID: "tt001", Type: mediaKindMovie}
	second := &MediaItem{ID: "tt002", Type: mediaKindMovie}
	root := &RadixNode{Children: []*RadixNode{
		{Label: "Duplicate", FullString: "Duplicate", IsTerminal: true, Media: first, SubtreeCount: 1},
		{Label: "Duplicate", FullString: "Duplicate", IsTerminal: true, Media: second, SubtreeCount: 1},
	}}
	nav := newNavigationState()

	if !nav.navigateToMedia(root, "movie", "tt002") {
		t.Fatal("navigateToMedia did not find the second duplicate")
	}
	if len(nav.path) != 1 || nav.path[0] != root || nav.cursor != 1 {
		t.Errorf("navigation = (path %v, cursor %d), want root path and cursor 1", nav.path, nav.cursor)
	}
	if nav.navigateToMedia(root, "movie", "missing") {
		t.Error("navigateToMedia found a missing identity")
	}
}
