package main

import (
	"fmt"
	"sort"

	"github.com/odwrtw/polochon/lib/papi"
)

// DynamicNodeType represents the type of dynamic node
type DynamicNodeType int

const (
	DynamicNodeSeason DynamicNodeType = iota
	DynamicNodeEpisode
)

// DynamicNode represents a runtime-created node for seasons/episodes
type DynamicNode struct {
	Type         DynamicNodeType
	Label        string
	Num          int           // Season number for season nodes; episode number for episode nodes
	Episode      *papi.Episode // Only set for episode nodes
	ShowID       string        // ImdbID of parent show (only set on episode nodes)
	Children     []*DynamicNode
	SubtreeCount int
}

// GetLabel implements NavNode interface
func (d *DynamicNode) GetLabel() string {
	return d.Label
}

// GetSubtreeCount implements NavNode interface
func (d *DynamicNode) GetSubtreeCount() int {
	return d.SubtreeCount
}

// IsTerminalNode implements NavNode interface
func (d *DynamicNode) IsTerminalNode() bool {
	return d.Type == DynamicNodeEpisode
}

// createSeasonNodes builds dynamic nodes for all seasons in a show
func createSeasonNodes(show *papi.Show) []*DynamicNode {
	if len(show.Seasons) == 0 {
		return nil
	}

	// Extract season numbers and sort
	seasonNums := make([]int, 0, len(show.Seasons))
	for num := range show.Seasons {
		seasonNums = append(seasonNums, num)
	}
	sort.Ints(seasonNums)

	// Create season nodes
	nodes := make([]*DynamicNode, 0, len(seasonNums))
	for _, num := range seasonNums {
		season := show.Seasons[num]
		episodeNodes := createEpisodeNodes(season, show.ImdbID)

		node := &DynamicNode{
			Type:         DynamicNodeSeason,
			Label:        fmt.Sprintf("Season %d", num),
			Num:          num,
			ShowID:       show.ImdbID,
			Children:     episodeNodes,
			SubtreeCount: len(episodeNodes),
		}

		nodes = append(nodes, node)
	}

	return nodes
}

// createEpisodeNodes builds dynamic nodes for all episodes in a season
func createEpisodeNodes(season *papi.Season, showID string) []*DynamicNode {
	if len(season.Episodes) == 0 {
		return nil
	}

	// Extract episode numbers and sort
	episodeNums := make([]int, 0, len(season.Episodes))
	for num := range season.Episodes {
		episodeNums = append(episodeNums, num)
	}
	sort.Ints(episodeNums)

	// Create episode nodes
	nodes := make([]*DynamicNode, 0, len(episodeNums))
	for _, num := range episodeNums {
		episode := season.Episodes[num]

		// Format: S01E01 - Episode Title
		label := fmt.Sprintf("S%02dE%02d", episode.Season, episode.Episode)
		if episode.Title != "" {
			label = fmt.Sprintf("%s - %s", label, episode.Title)
		}

		node := &DynamicNode{
			Type:         DynamicNodeEpisode,
			Label:        label,
			Num:          episode.Episode,
			Episode:      episode,
			ShowID:       showID,
			Children:     nil,
			SubtreeCount: 1,
		}

		nodes = append(nodes, node)
	}

	return nodes
}
