package app

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
	if nodes[0].Label != "Season 1" {
		t.Errorf("Expected 'Season 1', got %s", nodes[0].Label)
	}

	if len(nodes[0].Children) != 2 {
		t.Errorf("Expected 2 episode children, got %d", len(nodes[0].Children))
	}

	// Check episode labels
	ep1 := nodes[0].Children[0]
	if ep1.Label != "S01E01 - Pilot" {
		t.Errorf("Expected 'S01E01 - Pilot', got %s", ep1.Label)
	}
	if ep1.Episode == nil {
		t.Fatal("Expected episode to be set on episode node")
	}
	if ep1.Episode.Title != "Pilot" {
		t.Errorf("Expected episode title 'Pilot', got %s", ep1.Episode.Title)
	}
	if ep1.ShowID != "tt0903747" {
		t.Errorf("Expected ShowID 'tt0903747', got %s", ep1.ShowID)
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
