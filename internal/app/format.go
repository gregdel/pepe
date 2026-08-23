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

func formatDateAdded(date time.Time) string {
	if date.IsZero() {
		return ""
	}
	return date.Format("2006-01-02")
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

func formatSubtitles(embeddedLangs []polochon.Language, externalSubs []*papi.Subtitle) string {
	if len(embeddedLangs) == 0 && len(externalSubs) == 0 {
		return "Subtitles: None"
	}

	var text strings.Builder
	text.WriteString("Subtitles:\n")
	for _, lang := range embeddedLangs {
		fmt.Fprintf(&text, "  %s - embedded\n", formatLanguageName(lang))
	}
	for _, subtitle := range externalSubs {
		size := ""
		if subtitle.Size > 0 {
			size = fmt.Sprintf(", %s", humanize.Bytes(uint64(subtitle.Size)))
		}
		fmt.Fprintf(&text, "  %s - external%s\n", formatLanguageName(subtitle.Lang), size)
	}
	return strings.TrimRight(text.String(), "\n")
}
