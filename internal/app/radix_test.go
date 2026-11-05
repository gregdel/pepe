package app

import (
	"reflect"
	"sort"
	"testing"
	"unicode/utf8"
)

func TestRadixTreeUnicodeLabelsAreValidUTF8(t *testing.T) {
	input := []string{"Éclair", "École", "🎞️ Film", "🎞️ Festival", "🎥 Show"}
	tree := NewRadixTree(input)
	var visit func(*RadixNode)
	visit = func(node *RadixNode) {
		if !utf8.ValidString(node.Label) {
			t.Errorf("invalid UTF-8 radix edge label %q", node.Label)
		}
		if node.IsTerminal {
			if !utf8.ValidString(node.FullString) {
				t.Errorf("invalid UTF-8 terminal title %q", node.FullString)
			}
			if got := tree.Lookup(node.FullString); got == nil || got.FullString != node.FullString {
				t.Errorf("lookup failed for %q: %#v", node.FullString, got)
			}
		}
		for _, child := range node.Children {
			visit(child)
		}
	}
	visit(tree.Root())
	if got := len(tree.AllStrings()); got != len(input) {
		t.Fatalf("tree has %d strings, want %d", got, len(input))
	}
}

func TestRadixTreeEmpty(t *testing.T) {
	for _, test := range []struct {
		name   string
		titles []string
	}{
		{"nil", nil},
		{"empty", []string{}},
		{"empty strings", []string{"", ""}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := NewRadixTree(test.titles).Root()
			want := &RadixNode{Children: []*RadixNode{}}
			if !reflect.DeepEqual(root, want) {
				t.Errorf("empty tree root = %+v, want %+v", root, want)
			}
		})
	}
}

func TestRadixTreeSingleString(t *testing.T) {
	tree := NewRadixTree([]string{"hello"})

	if tree.root.SubtreeCount != 1 {
		t.Errorf("tree with one string should have subtree count 1, got %d", tree.root.SubtreeCount)
	}

	// Should have one child that is terminal
	if len(tree.root.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(tree.root.Children))
	}

	child := tree.root.Children[0]
	if !child.IsTerminal {
		t.Error("single string child should be terminal")
	}

	if child.Label != "hello" {
		t.Errorf("expected label 'hello', got '%s'", child.Label)
	}

	if child.FullString != "hello" {
		t.Errorf("expected FullString 'hello', got '%s'", child.FullString)
	}
}

func TestRadixTreeDuplicates(t *testing.T) {
	tree := NewRadixTree([]string{"hello", "hello", "world", "hello"})

	strings := tree.AllStrings()
	sort.Strings(strings)

	expected := []string{"hello", "world"}
	sort.Strings(expected)

	if len(strings) != len(expected) {
		t.Fatalf("expected %d unique strings, got %d", len(expected), len(strings))
	}

	for i := range expected {
		if strings[i] != expected[i] {
			t.Errorf("at index %d: expected '%s', got '%s'", i, expected[i], strings[i])
		}
	}
}

func TestRadixTreeCompression(t *testing.T) {
	// These should compress into a single chain
	tree := NewRadixTree([]string{"config", "confirm", "connection"})

	// Root should have one child with label "con"
	if len(tree.root.Children) != 1 {
		t.Fatalf("expected root to have 1 child, got %d", len(tree.root.Children))
	}

	con := tree.root.Children[0]
	if con.Label != "con" {
		t.Errorf("expected first node label 'con', got '%s'", con.Label)
	}

	// Should have 2 children: "fi" (which branches to g/rm) and "nection"
	if len(con.Children) != 2 {
		t.Fatalf("expected 'con' node to have 2 children, got %d", len(con.Children))
	}

	// The "fi" node should have 2 children: "g" and "rm"
	fiNode := con.Children[0]
	if fiNode.Label != "fi" {
		t.Errorf("expected first child label 'fi', got '%s'", fiNode.Label)
	}
	if len(fiNode.Children) != 2 {
		t.Errorf("expected 'fi' node to have 2 children, got %d", len(fiNode.Children))
	}

	// Verify all strings are present
	found := tree.AllStrings()
	sort.Strings(found)
	expected := []string{"config", "confirm", "connection"}
	sort.Strings(expected)

	if len(found) != len(expected) {
		t.Fatalf("expected %d strings, got %d", len(expected), len(found))
	}

	for i := range expected {
		if found[i] != expected[i] {
			t.Errorf("at index %d: expected '%s', got '%s'", i, expected[i], found[i])
		}
	}
}

func TestRadixTreeTerminalWithChildren(t *testing.T) {
	// "test" is both a complete string and prefix of "testing"
	tree := NewRadixTree([]string{"test", "testing"})

	// Find the "test" node
	testNode := tree.Lookup("test")
	if testNode == nil {
		t.Fatal("'test' should be found")
	}

	if !testNode.IsTerminal {
		t.Error("'test' node should be terminal")
	}

	// The node should have children (for "testing")
	if len(testNode.Children) == 0 {
		t.Error("'test' node should have children")
	}

	// Verify both strings exist
	testingNode := tree.Lookup("testing")
	if testingNode == nil {
		t.Fatal("'testing' should be found")
	}

	if !testingNode.IsTerminal {
		t.Error("'testing' node should be terminal")
	}
}

func TestRadixTreeSubtreeCount(t *testing.T) {
	tree := NewRadixTree([]string{
		"apple", "application", "apply",
		"banana", "band",
		"car",
	})

	// Root should have subtree count of 6
	if tree.root.SubtreeCount != 6 {
		t.Errorf("root subtree count should be 6, got %d", tree.root.SubtreeCount)
	}

	// Verify subtree counts are computed correctly recursively
	var validateSubtreeCounts func(*RadixNode) int
	validateSubtreeCounts = func(node *RadixNode) int {
		expected := 0
		if node.IsTerminal {
			expected = 1
		}

		for _, child := range node.Children {
			expected += validateSubtreeCounts(child)
		}

		if node.SubtreeCount != expected {
			t.Errorf("node with label '%s' has subtree count %d, expected %d",
				node.Label, node.SubtreeCount, expected)
		}

		return expected
	}

	validateSubtreeCounts(tree.root)
}

func TestRadixTreeChildOrdering(t *testing.T) {
	// Create tree where different branches have different sizes
	tree := NewRadixTree([]string{
		"a1", "a2", "a3", "a4", "a5", // 5 items starting with 'a'
		"b1", "b2", // 2 items starting with 'b'
		"c1", "c2", "c3", // 3 items starting with 'c'
	})

	// Root's children should be ordered: a (5), c (3), b (2)
	if len(tree.root.Children) != 3 {
		t.Fatalf("expected 3 root children, got %d", len(tree.root.Children))
	}

	// Check ordering
	expectedOrder := []struct {
		prefix string
		count  int
	}{
		{"a", 5},
		{"c", 3},
		{"b", 2},
	}

	for i, expected := range expectedOrder {
		child := tree.root.Children[i]
		if child.Label[0] != expected.prefix[0] {
			t.Errorf("child %d: expected prefix '%s', got '%s'", i, expected.prefix, child.Label)
		}
		if child.SubtreeCount != expected.count {
			t.Errorf("child %d: expected count %d, got %d", i, expected.count, child.SubtreeCount)
		}
	}

	// Verify ordering is stable and deterministic
	var checkOrdering func(*RadixNode)
	checkOrdering = func(node *RadixNode) {
		for i := 0; i < len(node.Children)-1; i++ {
			curr := node.Children[i]
			next := node.Children[i+1]

			if curr.SubtreeCount < next.SubtreeCount {
				t.Errorf("children not ordered: child[%d] count=%d < child[%d] count=%d",
					i, curr.SubtreeCount, i+1, next.SubtreeCount)
			}

			// If counts are equal, check lexicographic ordering
			if curr.SubtreeCount == next.SubtreeCount && curr.Label > next.Label {
				t.Errorf("children with equal counts not lexicographically ordered: '%s' > '%s'",
					curr.Label, next.Label)
			}

			checkOrdering(curr)
		}
	}

	checkOrdering(tree.root)
}

func TestRadixTreeLexicographicTieBreak(t *testing.T) {
	// Create scenario where multiple children have same subtree count
	tree := NewRadixTree([]string{"aa", "bb", "cc", "dd"})

	// All children should have count 1, so should be ordered lexicographically
	if len(tree.root.Children) != 4 {
		t.Fatalf("expected 4 children, got %d", len(tree.root.Children))
	}

	expectedLabels := []string{"aa", "bb", "cc", "dd"}
	for i, expected := range expectedLabels {
		if tree.root.Children[i].Label != expected {
			t.Errorf("child %d: expected label '%s', got '%s'",
				i, expected, tree.root.Children[i].Label)
		}
	}
}

func TestRadixTreeEarlyUniqueCollapse(t *testing.T) {
	// When a subtree has exactly 1 leaf, it should be fully collapsed
	tree := NewRadixTree([]string{
		"common-very-long-unique-path",
		"another-different-path",
	})

	// Each root child should be fully collapsed since they each have subtree count 1
	if len(tree.root.Children) != 2 {
		t.Fatalf("expected 2 root children, got %d", len(tree.root.Children))
	}

	for i, child := range tree.root.Children {
		if child.SubtreeCount != 1 {
			t.Errorf("child %d should have subtree count 1, got %d", i, child.SubtreeCount)
		}

		if !child.IsTerminal {
			t.Errorf("child %d with subtree count 1 should be terminal (collapsed)", i)
		}

		if len(child.Children) != 0 {
			t.Errorf("child %d with subtree count 1 should have no children (collapsed), got %d",
				i, len(child.Children))
		}
	}
}

func TestRadixTreeLookup(t *testing.T) {
	strings := []string{"hello", "help", "world", "wonderful"}
	tree := NewRadixTree(strings)

	// Test finding existing strings
	for _, s := range strings {
		node := tree.Lookup(s)
		if node == nil {
			t.Errorf("Lookup('%s') returned nil", s)
			continue
		}

		if !node.IsTerminal {
			t.Errorf("Lookup('%s') returned non-terminal node", s)
		}

		if node.FullString != s {
			t.Errorf("Lookup('%s') returned node with FullString='%s'", s, node.FullString)
		}
	}

	// Test non-existent strings
	notFound := []string{"hel", "helping", "wor", "wonderfully", "xyz", ""}
	for _, s := range notFound {
		node := tree.Lookup(s)
		if node != nil {
			t.Errorf("Lookup('%s') should return nil, got node with label '%s'", s, node.Label)
		}
	}
}

func TestRadixTreeStructuralCorrectness(t *testing.T) {
	input := []string{
		"apple", "application", "apply", "apricot",
		"banana", "band", "bandage",
		"car", "card", "care", "career",
		"dog", "dot",
	}

	tree := NewRadixTree(input)

	// Verify all input strings are present
	found := tree.AllStrings()

	if len(found) != len(input) {
		t.Errorf("expected %d strings, got %d", len(input), len(found))
	}

	// Check each input string exists
	for _, s := range input {
		node := tree.Lookup(s)
		if node == nil {
			t.Errorf("string '%s' not found in tree", s)
		}
	}

	// Verify no extra strings
	foundMap := make(map[string]bool)
	for _, s := range found {
		foundMap[s] = true
	}

	for _, s := range input {
		if !foundMap[s] {
			t.Errorf("expected string '%s' not in tree", s)
		}
	}

	for s := range foundMap {
		found := false
		for _, expected := range input {
			if s == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("unexpected extra string '%s' in tree", s)
		}
	}
}

func TestRadixTreeCompressionCorrectness(t *testing.T) {
	tree := NewRadixTree([]string{
		"test", "testing", "tester",
		"unique",
	})

	// Walk tree and verify no node has single child + nonterminal
	var validateCompression func(*RadixNode)
	validateCompression = func(node *RadixNode) {
		if len(node.Children) == 1 && !node.IsTerminal && node.Label != "" {
			t.Errorf("node with label '%s' has single child and is nonterminal (not compressed)",
				node.Label)
		}

		for _, child := range node.Children {
			validateCompression(child)
		}
	}

	validateCompression(tree.root)
}

func TestRadixTreeDeterminism(t *testing.T) {
	input := []string{
		"zebra", "apple", "banana", "cherry", "dog",
		"application", "apply", "band", "car", "card",
	}

	// Build tree multiple times
	tree1 := NewRadixTree(input)
	tree2 := NewRadixTree(input)

	// Trees should be identical
	var compareNodes func(n1, n2 *RadixNode, path string) bool
	compareNodes = func(n1, n2 *RadixNode, path string) bool {
		if n1.Label != n2.Label {
			t.Errorf("at %s: label mismatch: '%s' vs '%s'", path, n1.Label, n2.Label)
			return false
		}

		if n1.IsTerminal != n2.IsTerminal {
			t.Errorf("at %s: terminal mismatch", path)
			return false
		}

		if n1.SubtreeCount != n2.SubtreeCount {
			t.Errorf("at %s: subtree count mismatch: %d vs %d",
				path, n1.SubtreeCount, n2.SubtreeCount)
			return false
		}

		if len(n1.Children) != len(n2.Children) {
			t.Errorf("at %s: children count mismatch: %d vs %d",
				path, len(n1.Children), len(n2.Children))
			return false
		}

		for i := range n1.Children {
			if !compareNodes(n1.Children[i], n2.Children[i], path+"/"+n1.Children[i].Label) {
				return false
			}
		}

		return true
	}

	if !compareNodes(tree1.root, tree2.root, "root") {
		t.Error("trees built from same input are not identical")
	}
}

func TestRadixTreeEmptyStrings(t *testing.T) {
	// Empty strings should be filtered out
	tree := NewRadixTree([]string{"", "hello", "", "world", ""})

	found := tree.AllStrings()
	if len(found) != 2 {
		t.Errorf("expected 2 strings (empty filtered), got %d", len(found))
	}
}

func TestRadixTreeComplexDataset(t *testing.T) {
	// Simulate a more realistic dataset
	input := []string{
		"config", "configuration", "configure",
		"connect", "connection", "connector",
		"control", "controller",
		"data", "database", "dataset",
		"debug", "debugger",
		"deploy", "deployment",
		"test", "testing", "tester", "tests",
		"unique-single-entry",
	}

	tree := NewRadixTree(input)

	// Verify all strings present
	for _, s := range input {
		node := tree.Lookup(s)
		if node == nil {
			t.Errorf("string '%s' not found", s)
		}
	}

	// Verify root's children are ordered by subtree count
	if len(tree.root.Children) > 1 {
		for i := 0; i < len(tree.root.Children)-1; i++ {
			curr := tree.root.Children[i]
			next := tree.root.Children[i+1]

			if curr.SubtreeCount < next.SubtreeCount {
				t.Errorf("root children not ordered: child[%d](%s) count=%d < child[%d](%s) count=%d",
					i, curr.Label, curr.SubtreeCount, i+1, next.Label, next.SubtreeCount)
			}
		}
	}

	// Check that unique-single-entry is fully collapsed
	uniqueNode := tree.Lookup("unique-single-entry")
	if uniqueNode == nil {
		t.Fatal("unique-single-entry not found")
	}

	if len(uniqueNode.Children) != 0 {
		t.Error("unique-single-entry should have no children (collapsed)")
	}
}
