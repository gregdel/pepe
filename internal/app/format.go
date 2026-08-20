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
