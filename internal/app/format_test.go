package app

import (
	"strings"
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

func TestFormatSubtitles(t *testing.T) {
	tests := []struct {
		name     string
		embedded []polochon.Language
		external []*papi.Subtitle
		want     []string
	}{
		{"none", nil, nil, []string{"Subtitles: None"}},
		{"embedded", []polochon.Language{"en"}, nil, []string{"English (en) - embedded"}},
		{"external", nil, []*papi.Subtitle{{Subtitle: &polochon.Subtitle{Lang: "fr", File: polochon.File{Size: 55000}}}}, []string{"French (fr) - external"}},
		{"embedded and external", []polochon.Language{"en"}, []*papi.Subtitle{{Subtitle: &polochon.Subtitle{Lang: "fr", File: polochon.File{Size: 42000}}}}, []string{"English (en) - embedded", "French (fr) - external"}},
		{"unknown language", []polochon.Language{"de_DE"}, nil, []string{"de_DE (de_DE) - embedded"}},
		{"multiple embedded", []polochon.Language{"en", "fr", "es"}, nil, []string{"English (en) - embedded", "French (fr) - embedded", "es (es) - embedded"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := formatSubtitles(test.embedded, test.external)
			if strings.Contains(got, "kB") {
				t.Fatalf("subtitle summary includes file sizes: %q", got)
			}
			for _, want := range test.want {
				if !strings.Contains(got, want) {
					t.Errorf("formatSubtitles() = %q, missing %q", got, want)
				}
			}
		})
	}
}
