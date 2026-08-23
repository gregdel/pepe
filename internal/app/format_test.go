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

func TestFormatSubtitles(t *testing.T) {
	tests := []struct {
		name     string
		embedded []polochon.Language
		external []*papi.Subtitle
		want     []string
	}{
		{"none", nil, nil, []string{"Subtitles: None"}},
		{"embedded", []polochon.Language{"en"}, nil, []string{"English (en) - embedded"}},
		{"external", nil, []*papi.Subtitle{{Subtitle: &polochon.Subtitle{Lang: "fr", File: polochon.File{Size: 55000}}}}, []string{"French (fr) - external, 55 kB"}},
		{"embedded and external", []polochon.Language{"en"}, []*papi.Subtitle{{Subtitle: &polochon.Subtitle{Lang: "fr", File: polochon.File{Size: 42000}}}}, []string{"English (en) - embedded", "French (fr) - external, 42 kB"}},
		{"unknown language", []polochon.Language{"de_DE"}, nil, []string{"de_DE (de_DE) - embedded"}},
		{"multiple embedded", []polochon.Language{"en", "fr", "es"}, nil, []string{"English (en) - embedded", "French (fr) - embedded", "es (es) - embedded"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := formatSubtitles(test.embedded, test.external)
			for _, want := range test.want {
				if !strings.Contains(got, want) {
					t.Errorf("formatSubtitles() = %q, missing %q", got, want)
				}
			}
		})
	}
}
