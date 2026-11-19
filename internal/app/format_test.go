package app

import (
	"testing"

	polochon "github.com/odwrtw/polochon/lib"
	"github.com/odwrtw/polochon/lib/papi"
)

func TestFormatTechnicalMetadata(t *testing.T) {
	got := formatTechnicalMetadata(1_100_000_000, polochon.Quality("720p"), "H.264", "Dolby Digital", "mkv", "KILLERS[ettv]")
	want := "720p  ·  H.264 + Dolby Digital  ·  MKV  ·  1.1 GB  ·  KILLERS[ettv]"
	if got != want {
		t.Fatalf("formatTechnicalMetadata() = %q, want %q", got, want)
	}
	if got := formatTechnicalMetadata(0, "", "", "", "", ""); got != "" {
		t.Fatalf("empty formatTechnicalMetadata() = %q", got)
	}
}

func TestFormatAiredDate(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{input: "2018-02-26", want: "Feb 26, 2018"},
		{input: "2018-02-26T20:00:00Z", want: "Feb 26, 2018"},
		{input: "unknown", want: "unknown"},
		{input: "", want: ""},
	} {
		if got := formatAiredDate(test.input); got != test.want {
			t.Errorf("formatAiredDate(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestFormatSubtitleSummary(t *testing.T) {
	for _, test := range []struct {
		name     string
		embedded []polochon.Language
		external []*papi.Subtitle
		want     string
	}{
		{name: "none", want: "Subtitles: None"},
		{name: "invalid tracks", external: []*papi.Subtitle{nil, {}}, want: "Subtitles: None"},
		{name: "embedded", embedded: []polochon.Language{"en", "fr"}, want: "Subtitles: English (en) · French (fr)"},
		{name: "external without file size", external: []*papi.Subtitle{
			{Subtitle: &polochon.Subtitle{Lang: "fr", File: polochon.File{Size: 55000}}},
		}, want: "Subtitles: French (fr)"},
		{name: "unknown language", embedded: []polochon.Language{"de_DE"}, want: "Subtitles: de_DE (de_DE)"},
		{name: "deduplicated", embedded: []polochon.Language{"en", "en"}, external: []*papi.Subtitle{
			{Subtitle: &polochon.Subtitle{Lang: "en"}},
			{Subtitle: &polochon.Subtitle{Lang: "fr"}},
		}, want: "Subtitles: English (en) · French (fr)"},
		{name: "many languages", embedded: []polochon.Language{"en", "fr", "de", "es", "it"}, want: "Subtitles: English (en) · French (fr) · de (de) · +2 more"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := formatSubtitleSummary(test.embedded, test.external); got != test.want {
				t.Fatalf("formatSubtitleSummary() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestBuildMetaLine(t *testing.T) {
	if got, want := buildMetaLine(1999, 136, "1080p", 8.7, []string{"Action", "Sci-Fi"}), "1999  ·  2h 16m  ·  1080p  ·  8.7/10  ·  Action, Sci-Fi"; got != want {
		t.Fatalf("buildMetaLine() = %q, want %q", got, want)
	}
	if got, want := buildMetaLine(0, 45, "", 0, nil), "45m"; got != want {
		t.Fatalf("buildMetaLine() without metadata = %q, want %q", got, want)
	}
}
