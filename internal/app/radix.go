package app

import (
	"log"
	"sort"
	"strings"
)

// RadixNode represents a node in the compressed radix tree
type RadixNode struct {
	Label        string       // Edge label (string segment from parent to this node)
	IsTerminal   bool         // True if this node represents a complete string
	FullString   string       // The complete string if terminal
	Media        *MediaItem   // App-owned media identity for listing terminals
	SubtreeCount int          // Total number of terminal strings in subtree
	Children     []*RadixNode // Ordered by descending subtree count
}

// RadixTree is a static, compressed radix tree optimized for arrow-navigation
type RadixTree struct {
	root *RadixNode
}

// buildNode is internal helper for building the uncompressed trie
type buildNode struct {
	children   map[byte]*buildNode
	terminal   bool
	fullString string
}

func newBuildNode() *buildNode {
	return &buildNode{
		children: make(map[byte]*buildNode),
	}
}

// NewRadixTree constructs a compressed radix tree from a set of strings
func NewRadixTree(strings []string) *RadixTree {
	// Remove duplicates and empty strings
	uniqueStrings := deduplicateStrings(strings)

	if len(uniqueStrings) == 0 {
		return &RadixTree{
			root: &RadixNode{
				Label:        "",
				Children:     []*RadixNode{},
				SubtreeCount: 0,
			},
		}
	}

	// Build uncompressed trie
	trieRoot := buildUncompressedTrie(uniqueStrings)

	// Convert to compressed radix tree
	radixRoot := &RadixNode{
		Label: "",
	}

	// Convert children
	for char, child := range trieRoot.children {
		radixChild := compressNode(child, string(char))
		if radixChild != nil {
			radixRoot.Children = append(radixRoot.Children, radixChild)
		}
	}

	// Compute subtree counts
	computeSubtreeCounts(radixRoot)

	// Order children by subtree count
	orderChildren(radixRoot)

	return &RadixTree{root: radixRoot}
}

// deduplicateStrings removes duplicates and empty strings
func deduplicateStrings(strings []string) []string {
	seen := make(map[string]bool)
	result := []string{}

	for _, s := range strings {
		if s != "" && !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}

	return result
}

// buildUncompressedTrie creates an uncompressed trie from strings
func buildUncompressedTrie(strings []string) *buildNode {
	root := newBuildNode()

	for _, s := range strings {
		node := root
		for i := 0; i < len(s); i++ {
			char := s[i]
			if node.children[char] == nil {
				node.children[char] = newBuildNode()
			}
			node = node.children[char]
		}
		node.terminal = true
		node.fullString = s
	}

	return root
}

// compressNode recursively compresses a trie node into a radix node
func compressNode(node *buildNode, label string) *RadixNode {
	if node == nil {
		return nil
	}

	radixNode := &RadixNode{
		Label:      label,
		IsTerminal: node.terminal,
		FullString: node.fullString,
		Children:   []*RadixNode{},
	}

	// If this node has exactly one child and is not terminal, compress
	for len(node.children) == 1 && !node.terminal {
		// Get the single child
		var childChar byte
		var childNode *buildNode
		for c, n := range node.children {
			childChar = c
			childNode = n
			break
		}

		// Merge into current label
		label = label + string(childChar)
		node = childNode

		// Update node state
		radixNode.Label = label
		radixNode.IsTerminal = node.terminal
		radixNode.FullString = node.fullString
	}

	// Process children
	for char, child := range node.children {
		childRadix := compressNode(child, string(char))
		if childRadix != nil {
			radixNode.Children = append(radixNode.Children, childRadix)
		}
	}

	return radixNode
}

// computeSubtreeCounts computes subtree leaf counts for all nodes
func computeSubtreeCounts(node *RadixNode) int {
	if node == nil {
		return 0
	}

	count := 0
	if node.IsTerminal {
		count = 1
	}

	for _, child := range node.Children {
		count += computeSubtreeCounts(child)
	}

	node.SubtreeCount = count

	// Validation: non-terminal nodes with SubtreeCount=1 should not exist after compression
	if !node.IsTerminal && node.SubtreeCount == 1 && len(node.Children) > 0 {
		log.Printf("WARNING: Non-terminal node with SubtreeCount=1 detected (Label: %q). This indicates incomplete compression.", node.Label)
	}

	// Early uniqueness collapse (Option B): if subtree has exactly 1 leaf, collapse it
	// But don't collapse the root node
	if node.Label != "" && node.SubtreeCount == 1 && !node.IsTerminal && len(node.Children) == 1 {
		// Collapse: merge child into this node
		child := node.Children[0]
		node.Label = node.Label + child.Label
		node.IsTerminal = child.IsTerminal
		node.FullString = child.FullString
		node.Children = child.Children
		// Note: SubtreeCount is already correct (1)
	}

	return count
}

// orderChildren recursively orders children by descending subtree count,
// with lexicographic tie-breaking
func orderChildren(node *RadixNode) {
	if node == nil || len(node.Children) == 0 {
		return
	}

	// Sort children
	sort.Slice(node.Children, func(i, j int) bool {
		ci, cj := node.Children[i], node.Children[j]

		// Primary: descending subtree count
		if ci.SubtreeCount != cj.SubtreeCount {
			return ci.SubtreeCount > cj.SubtreeCount
		}

		// Tie-break: ascending lexicographic
		return ci.Label < cj.Label
	})

	// Recursively order children's children
	for _, child := range node.Children {
		orderChildren(child)
	}
}

// Lookup performs exact string lookup and returns the terminal node or nil
func (t *RadixTree) Lookup(s string) *RadixNode {
	if t.root == nil {
		return nil
	}

	return lookupHelper(t.root, s)
}

func lookupHelper(node *RadixNode, remaining string) *RadixNode {
	// If we're at root (empty label), search children
	if node.Label == "" {
		for _, child := range node.Children {
			if strings.HasPrefix(remaining, child.Label) {
				result := lookupHelper(child, remaining)
				if result != nil {
					return result
				}
			}
		}
		return nil
	}

	// Check if label matches prefix of remaining string
	if !strings.HasPrefix(remaining, node.Label) {
		return nil
	}

	remaining = remaining[len(node.Label):]

	// If nothing remains and this is terminal, found it
	if remaining == "" {
		if node.IsTerminal {
			return node
		}
		return nil
	}

	// Search children
	for _, child := range node.Children {
		if strings.HasPrefix(remaining, child.Label) {
			return lookupHelper(child, remaining)
		}
	}

	return nil
}

// Root returns the root node of the tree
func (t *RadixTree) Root() *RadixNode {
	return t.root
}

// walkTree recursively walks the tree and collects all terminal strings
func walkTree(node *RadixNode, result *[]string) {
	if node.IsTerminal {
		*result = append(*result, node.FullString)
	}

	for _, child := range node.Children {
		walkTree(child, result)
	}
}

// AllStrings returns all terminal strings in the tree
func (t *RadixTree) AllStrings() []string {
	result := []string{}
	if t.root != nil {
		walkTree(t.root, &result)
	}
	return result
}
