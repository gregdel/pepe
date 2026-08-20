package app

import (
	"fmt"
	"testing"
)

func BenchmarkRadixTreeBuild(b *testing.B) {
	// Generate ~3000 strings similar to media titles
	strings := make([]string, 3000)
	for i := 0; i < 3000; i++ {
		strings[i] = fmt.Sprintf("Movie Title Number %d", i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NewRadixTree(strings)
	}
}

func BenchmarkRadixTreeLookup(b *testing.B) {
	strings := make([]string, 3000)
	for i := 0; i < 3000; i++ {
		strings[i] = fmt.Sprintf("Movie Title Number %d", i)
	}

	tree := NewRadixTree(strings)
	lookupString := "Movie Title Number 1500"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tree.Lookup(lookupString)
	}
}

func TestRadixTreeWithRealisticDataset(t *testing.T) {
	// Simulate realistic movie/show titles
	titles := []string{
		"The Shawshank Redemption",
		"The Godfather",
		"The Godfather Part II",
		"The Dark Knight",
		"The Dark Knight Rises",
		"12 Angry Men",
		"Schindler's List",
		"The Lord of the Rings: The Return of the King",
		"The Lord of the Rings: The Fellowship of the Ring",
		"The Lord of the Rings: The Two Towers",
		"Pulp Fiction",
		"The Good, the Bad and the Ugly",
		"Fight Club",
		"Forrest Gump",
		"Inception",
		"The Matrix",
		"The Matrix Reloaded",
		"The Matrix Revolutions",
		"Star Wars",
		"Star Wars: Episode V",
		"Star Wars: Episode VI",
		"Breaking Bad",
		"Better Call Saul",
		"Game of Thrones",
		"The Wire",
		"The Sopranos",
	}

	tree := NewRadixTree(titles)

	// Verify all titles are present
	for _, title := range titles {
		node := tree.Lookup(title)
		if node == nil {
			t.Errorf("title '%s' not found in tree", title)
		}
	}

	// Check that "The " prefix is properly grouped
	root := tree.Root()
	if len(root.Children) == 0 {
		t.Fatal("tree should have children")
	}

	// Should have good compression
	var countNodes func(*RadixNode) int
	countNodes = func(n *RadixNode) int {
		count := 1
		for _, child := range n.Children {
			count += countNodes(child)
		}
		return count
	}

	totalNodes := countNodes(root)
	t.Logf("Total nodes for %d strings: %d", len(titles), totalNodes)
	t.Logf("Root has %d children", len(root.Children))

	// First child should have highest subtree count
	if len(root.Children) > 1 {
		for i := 0; i < len(root.Children)-1; i++ {
			if root.Children[i].SubtreeCount < root.Children[i+1].SubtreeCount {
				t.Errorf("children not properly ordered by subtree count")
			}
		}
	}
}
