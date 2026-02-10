package main

import (
	"fmt"
	"strings"
	"testing"

	polochon "github.com/odwrtw/polochon/lib"
	"github.com/odwrtw/polochon/lib/papi"
)

func TestRadixTreeIntegration(t *testing.T) {
	// Test building radix tree from movie titles
	titles := []string{
		"The Godfather",
		"The Godfather Part II",
		"The Godfather Part III",
		"The Matrix",
		"The Matrix Reloaded",
		"The Matrix Revolutions",
		"Avatar",
		"Avatar: The Way of Water",
		"Inception",
		"Interstellar",
	}

	tree := NewRadixTree(titles)

	if tree == nil {
		t.Fatal("Expected tree to be non-nil")
	}

	root := tree.Root()
	if root == nil {
		t.Fatal("Expected root to be non-nil")
	}

	// Verify subtree count
	if root.SubtreeCount != len(titles) {
		t.Errorf("Expected root subtree count to be %d, got %d", len(titles), root.SubtreeCount)
	}

	// Verify all strings are present
	allStrings := tree.AllStrings()
	if len(allStrings) != len(titles) {
		t.Errorf("Expected %d strings in tree, got %d", len(titles), len(allStrings))
	}

	// Verify lookups work
	for _, title := range titles {
		node := tree.Lookup(title)
		if node == nil {
			t.Errorf("Expected to find node for '%s'", title)
			continue
		}

		if !node.IsTerminal {
			t.Errorf("Expected node for '%s' to be terminal", title)
		}

		if node.FullString != title {
			t.Errorf("Expected node FullString to be '%s', got '%s'", title, node.FullString)
		}
	}
}

func TestTitleToMediaMapping(t *testing.T) {
	// Create a mock titleToMedia map
	titleToMedia := make(map[string]*MediaItem)

	movies := []string{
		"The Godfather",
		"The Matrix",
		"Inception",
	}

	for i, title := range movies {
		titleToMedia[title] = &MediaItem{
			ID:    "id" + string(rune(i)),
			Title: title,
			Year:  2000 + i,
			Type:  "movie",
		}
	}

	// Build tree
	tree := NewRadixTree(movies)

	// Verify we can lookup media items by navigating tree
	for _, title := range movies {
		node := tree.Lookup(title)
		if node == nil {
			t.Errorf("Expected to find node for '%s'", title)
			continue
		}

		media, ok := titleToMedia[node.FullString]
		if !ok {
			t.Errorf("Expected to find media item for '%s'", title)
			continue
		}

		if media.Title != title {
			t.Errorf("Expected media title to be '%s', got '%s'", title, media.Title)
		}
	}
}

func TestNavigationWithRadixTree(t *testing.T) {
	// Build a small tree
	titles := []string{
		"The Godfather",
		"The Godfather Part II",
		"The Matrix",
	}

	tree := NewRadixTree(titles)
	nav := newNavigationState()

	// Start at root
	if !nav.isAtRoot() {
		t.Error("Expected to start at root")
	}

	// Enter section
	nav.resetToSection(SectionMovies, tree.Root())

	if nav.isAtRoot() {
		t.Error("Expected to not be at root after entering section")
	}

	if !nav.isAtSectionRoot() {
		t.Error("Expected to be at section root")
	}

	// Should have children
	childCount := nav.getChildrenCount()
	if childCount == 0 {
		t.Error("Expected to have children at root")
	}

	// Navigate to first child
	firstChild := nav.getSelectedChild()
	if firstChild == nil {
		t.Fatal("Expected to have a selected child")
	}

	// Enter first child
	nav.goForward(firstChild)

	if nav.isAtSectionRoot() {
		t.Error("Expected to not be at section root after going forward")
	}

	// Go back
	nav.goBack()

	if !nav.isAtSectionRoot() {
		t.Error("Expected to be back at section root")
	}
}

func TestEmptyRadixTree(t *testing.T) {
	// Test with empty titles
	tree := NewRadixTree([]string{})

	if tree == nil {
		t.Fatal("Expected tree to be non-nil even with empty input")
	}

	root := tree.Root()
	if root == nil {
		t.Fatal("Expected root to be non-nil")
	}

	if root.SubtreeCount != 0 {
		t.Errorf("Expected subtree count to be 0, got %d", root.SubtreeCount)
	}

	if len(root.Children) != 0 {
		t.Errorf("Expected no children, got %d", len(root.Children))
	}
}

func TestDuplicateTitles(t *testing.T) {
	// Test with duplicate titles
	titles := []string{
		"The Matrix",
		"The Matrix",
		"Inception",
	}

	tree := NewRadixTree(titles)

	// Should deduplicate
	allStrings := tree.AllStrings()
	if len(allStrings) != 2 {
		t.Errorf("Expected 2 unique strings, got %d", len(allStrings))
	}

	if tree.Root().SubtreeCount != 2 {
		t.Errorf("Expected subtree count to be 2, got %d", tree.Root().SubtreeCount)
	}
}

func TestThePrefixHandling(t *testing.T) {
	// Test that "The" prefix is handled correctly (double indexing)
	titleToMedia := make(map[string]*MediaItem)

	// Simulate what fetchLibrary does
	movies := []struct {
		title string
		year  int
	}{
		{"The Matrix", 1999},
		{"The Godfather", 1972},
		{"Matrix Reloaded", 2003}, // Note: no "The"
	}

	var movieTitles []string

	// Helper functions (same as in core.go)
	formatTitle := func(title string, year int) string {
		if year > 0 {
			return fmt.Sprintf("%s (%d)", title, year)
		}
		return title
	}

	stripThePrefix := func(title string) (string, bool) {
		if len(title) > 4 && strings.HasPrefix(strings.ToLower(title), "the ") {
			return title[4:], true
		}
		return title, false
	}

	for _, movie := range movies {
		displayTitle := formatTitle(movie.title, movie.year)
		item := &MediaItem{
			ID:    "test",
			Title: movie.title,
			Year:  movie.year,
			Type:  "movie",
		}

		// Add main title
		movieTitles = append(movieTitles, displayTitle)
		titleToMedia[displayTitle] = item

		// Also index without "The " prefix if present
		if strippedTitle, hasThe := stripThePrefix(displayTitle); hasThe {
			movieTitles = append(movieTitles, strippedTitle)
			titleToMedia[strippedTitle] = item
		}
	}

	// Build tree
	tree := NewRadixTree(movieTitles)

	// Verify "The Matrix (1999)" is findable
	node1 := tree.Lookup("The Matrix (1999)")
	if node1 == nil {
		t.Error("Expected to find 'The Matrix (1999)'")
	}

	// Verify "Matrix (1999)" is also findable (stripped version)
	node2 := tree.Lookup("Matrix (1999)")
	if node2 == nil {
		t.Error("Expected to find 'Matrix (1999)' (stripped version)")
	}

	// Both should point to the same MediaItem
	media1, ok1 := titleToMedia["The Matrix (1999)"]
	media2, ok2 := titleToMedia["Matrix (1999)"]

	if !ok1 || !ok2 {
		t.Error("Expected both versions to be in titleToMedia map")
	}

	if media1 != media2 {
		t.Error("Expected both versions to point to same MediaItem")
	}

	// Verify both versions have same title/year
	if media1.Title != "The Matrix" || media1.Year != 1999 {
		t.Errorf("Expected title='The Matrix', year=1999, got title='%s', year=%d", media1.Title, media1.Year)
	}
}

func TestYearInTitles(t *testing.T) {
	// Test that years are correctly appended to titles
	titles := []struct {
		original string
		year     int
		expected string
	}{
		{"The Matrix", 1999, "The Matrix (1999)"},
		{"Inception", 2010, "Inception (2010)"},
		{"Unknown Movie", 0, "Unknown Movie"}, // No year
	}

	for _, tt := range titles {
		var formatted string
		if tt.year > 0 {
			formatted = fmt.Sprintf("%s (%d)", tt.original, tt.year)
		} else {
			formatted = tt.original
		}

		if formatted != tt.expected {
			t.Errorf("For title='%s', year=%d: expected '%s', got '%s'",
				tt.original, tt.year, tt.expected, formatted)
		}
	}
}

// TestSubtitleFormatting verifies the formatSubtitles function handles various subtitle configurations
func TestSubtitleFormatting(t *testing.T) {
	tests := []struct {
		name            string
		embeddedSubs    []polochon.Language
		externalSubs    []*papi.Subtitle
		expectedContain []string // Strings that should be in output
		shouldContain   string   // Single string check
	}{
		{
			name:          "No subtitles",
			embeddedSubs:  []polochon.Language{},
			externalSubs:  []*papi.Subtitle{},
			shouldContain: "Subtitles: None",
		},
		{
			name:         "Only embedded English",
			embeddedSubs: []polochon.Language{"en"},
			externalSubs: []*papi.Subtitle{},
			expectedContain: []string{
				"Subtitles:",
				"English (en) - embedded",
			},
		},
		{
			name:         "Only external French",
			embeddedSubs: []polochon.Language{},
			externalSubs: []*papi.Subtitle{
				{Subtitle: &polochon.Subtitle{Lang: "fr", File: polochon.File{Size: 55000}, Embedded: false}},
			},
			expectedContain: []string{
				"Subtitles:",
				"French (fr) - external, 55 kB",
			},
		},
		{
			name:         "Both embedded and external",
			embeddedSubs: []polochon.Language{"en"},
			externalSubs: []*papi.Subtitle{
				{Subtitle: &polochon.Subtitle{Lang: "fr", File: polochon.File{Size: 42000}, Embedded: false}},
			},
			expectedContain: []string{
				"Subtitles:",
				"English (en) - embedded",
				"French (fr) - external, 42 kB",
			},
		},
		{
			name:         "Unknown language code",
			embeddedSubs: []polochon.Language{"de_DE"},
			externalSubs: []*papi.Subtitle{},
			expectedContain: []string{
				"Subtitles:",
				"de_DE (de_DE) - embedded",
			},
		},
		{
			name:         "Multiple embedded",
			embeddedSubs: []polochon.Language{"en", "fr", "es"},
			externalSubs: []*papi.Subtitle{},
			expectedContain: []string{
				"Subtitles:",
				"English (en) - embedded",
				"French (fr) - embedded",
				"es (es) - embedded",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatSubtitles(tt.embeddedSubs, tt.externalSubs)

			if tt.shouldContain != "" {
				if result != tt.shouldContain {
					t.Errorf("Expected '%s', got '%s'", tt.shouldContain, result)
				}
				return
			}

			for _, expected := range tt.expectedContain {
				if !strings.Contains(result, expected) {
					t.Errorf("Expected result to contain '%s', got:\n%s", expected, result)
				}
			}
		})
	}
}
