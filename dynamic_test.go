package main

import (
	"testing"

	polochon "github.com/odwrtw/polochon/lib"
	"github.com/odwrtw/polochon/lib/papi"
)

func TestCreateSeasonNodes(t *testing.T) {
	// Create test show with seasons and episodes
	show := &papi.Show{
		Show: &polochon.Show{
			ImdbID: "tt0903747",
			Title:  "Breaking Bad",
		},
		Seasons: map[int]*papi.Season{
			1: {
				Season:     1,
				ShowImdbID: "tt0903747",
				Episodes: map[int]*papi.Episode{
					1: {
						ShowEpisode: &polochon.ShowEpisode{
							Season:  1,
							Episode: 1,
							Title:   "Pilot",
						},
					},
					2: {
						ShowEpisode: &polochon.ShowEpisode{
							Season:  1,
							Episode: 2,
							Title:   "Cat's in the Bag...",
						},
					},
				},
			},
			2: {
				Season:     2,
				ShowImdbID: "tt0903747",
				Episodes: map[int]*papi.Episode{
					1: {
						ShowEpisode: &polochon.ShowEpisode{
							Season:  2,
							Episode: 1,
							Title:   "Seven Thirty-Seven",
						},
					},
				},
			},
		},
	}

	nodes := createSeasonNodes(show)

	// Should have 2 seasons
	if len(nodes) != 2 {
		t.Errorf("Expected 2 season nodes, got %d", len(nodes))
	}

	// Check first season
	if nodes[0].Type != DynamicNodeSeason {
		t.Errorf("Expected season node type, got %v", nodes[0].Type)
	}

	if nodes[0].Label != "Season 1" {
		t.Errorf("Expected 'Season 1', got %s", nodes[0].Label)
	}

	if nodes[0].SubtreeCount != 2 {
		t.Errorf("Expected season 1 to have 2 episodes, got %d", nodes[0].SubtreeCount)
	}

	// Check episodes in first season
	if len(nodes[0].Children) != 2 {
		t.Errorf("Expected 2 episode children, got %d", len(nodes[0].Children))
	}

	// Check episode labels
	ep1 := nodes[0].Children[0]
	if ep1.Type != DynamicNodeEpisode {
		t.Errorf("Expected episode node type, got %v", ep1.Type)
	}

	if ep1.Label != "S01E01 - Pilot" {
		t.Errorf("Expected 'S01E01 - Pilot', got %s", ep1.Label)
	}

	if !ep1.IsTerminalNode() {
		t.Error("Episode nodes should be terminal")
	}
}

func TestCreateSeasonNodesEmpty(t *testing.T) {
	// Show with no seasons
	show := &papi.Show{
		Show: &polochon.Show{
			ImdbID: "tt0000000",
			Title:  "Empty Show",
		},
		Seasons: nil,
	}

	nodes := createSeasonNodes(show)

	if nodes != nil {
		t.Errorf("Expected nil nodes for show with no seasons, got %v", nodes)
	}
}

func TestDynamicNodeNavigation(t *testing.T) {
	// Create test season structure
	seasonNodes := []*DynamicNode{
		{
			Type:         DynamicNodeSeason,
			Label:        "Season 1",
			SubtreeCount: 2,
			Children: []*DynamicNode{
				{
					Type:         DynamicNodeEpisode,
					Label:        "S01E01 - Pilot",
					SubtreeCount: 1,
				},
				{
					Type:         DynamicNodeEpisode,
					Label:        "S01E02 - Episode 2",
					SubtreeCount: 1,
				},
			},
		},
	}

	nav := newNavigationState()

	// Should start in radix tree mode
	if nav.inDynamicNav {
		t.Error("Should not start in dynamic nav mode")
	}

	// Enter dynamic navigation
	nav.enterDynamicNav(seasonNodes, "Breaking Bad")

	if !nav.inDynamicNav {
		t.Error("Should be in dynamic nav mode")
	}

	// Should be at season level
	current := nav.getCurrentDynamicNode()
	if current == nil {
		t.Fatal("Current dynamic node should not be nil")
	}

	if len(current.Children) != 1 {
		t.Errorf("Expected 1 season, got %d", len(current.Children))
	}

	// Select season and go forward
	selectedSeason := nav.getSelectedDynamicChild()
	if selectedSeason.Label != "Season 1" {
		t.Errorf("Expected 'Season 1', got %s", selectedSeason.Label)
	}

	nav.goForwardDynamic(selectedSeason)

	// Should now be inside the season
	current = nav.getCurrentDynamicNode()
	if current.Type != DynamicNodeSeason {
		t.Errorf("Expected to be in season node, got %v", current.Type)
	}

	if len(current.Children) != 2 {
		t.Errorf("Expected 2 episodes, got %d", len(current.Children))
	}

	// Cursor should be reset to 0
	if nav.cursor != 0 {
		t.Errorf("Expected cursor at 0, got %d", nav.cursor)
	}

	// Move cursor down
	nav.moveCursorDown(2)
	if nav.cursor != 1 {
		t.Errorf("Expected cursor at 1, got %d", nav.cursor)
	}

	// Go back to season list
	nav.goBack()

	if !nav.inDynamicNav {
		t.Error("Should still be in dynamic nav mode")
	}

	// Go back again should exit dynamic nav
	nav.goBack()

	if nav.inDynamicNav {
		t.Error("Should have exited dynamic nav mode")
	}
}
