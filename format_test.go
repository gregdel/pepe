package main

import (
	"strings"
	"testing"

	polochon "github.com/odwrtw/polochon/lib"
	"github.com/odwrtw/polochon/lib/papi"
)

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
