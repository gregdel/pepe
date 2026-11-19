package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	polochon "github.com/odwrtw/polochon/lib"
	"github.com/odwrtw/polochon/lib/papi"
)

func formatQualityBadge(quality polochon.Quality) string { return string(quality) }

// plural returns "1 season" or "2 seasons".
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// formatRating returns a labelled rating such as "8.3/10".
func formatRating(rating float64) string { return fmt.Sprintf("%.1f/10", rating) }

func formatContainer(container string) string { return strings.ToUpper(container) }

func formatCodecs(video, audio string) string {
	switch {
	case video == "" && audio == "":
		return ""
	case video == "":
		return audio
	case audio == "":
		return video
	default:
		return video + " + " + audio
	}
}

func formatTechnicalMetadata(size int64, quality polochon.Quality, video, audio, container, releaseGroup string) string {
	var parts []string
	if quality != "" {
		parts = append(parts, formatQualityBadge(quality))
	}
	if codecs := formatCodecs(video, audio); codecs != "" {
		parts = append(parts, codecs)
	}
	if container := formatContainer(container); container != "" {
		parts = append(parts, container)
	}
	if size > 0 {
		parts = append(parts, humanize.Bytes(uint64(size)))
	}
	if releaseGroup != "" {
		parts = append(parts, releaseGroup)
	}
	return strings.Join(parts, "  ·  ")
}

func formatAiredDate(value string) string {
	if value == "" {
		return ""
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339, "2006-01-02T15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.Format("Jan 2, 2006")
		}
	}
	return value
}

func formatGenres(genres []string) string { return strings.Join(genres, ", ") }

var languageNames = map[string]string{"en": "English", "fr": "French"}

func formatLanguageName(lang polochon.Language) string {
	short := lang.ShortForm()
	if name, ok := languageNames[short]; ok {
		return fmt.Sprintf("%s (%s)", name, short)
	}
	return fmt.Sprintf("%s (%s)", string(lang), string(lang))
}

// formatSubtitleSummary lists distinct available languages; individual tracks
// and their source remain in the subtitle popup and browse preview.
func formatSubtitleSummary(embeddedLangs []polochon.Language, externalSubs []*papi.Subtitle) string {
	var languages []string
	seen := make(map[string]bool)
	add := func(lang polochon.Language) {
		name := formatLanguageName(lang)
		if !seen[name] {
			seen[name] = true
			languages = append(languages, name)
		}
	}
	for _, lang := range embeddedLangs {
		add(lang)
	}
	for _, sub := range externalSubs {
		if sub != nil && sub.Subtitle != nil {
			add(sub.Lang)
		}
	}
	if len(languages) == 0 {
		return "Subtitles: None"
	}
	if len(languages) > 3 {
		languages = append(languages[:3], fmt.Sprintf("+%d more", len(languages)-3))
	}
	return "Subtitles: " + strings.Join(languages, " · ")
}
