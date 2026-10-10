package app

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfiguredHTTPURLWarnsAtClientStartup(t *testing.T) {
	for _, tc := range []struct {
		name, fileURL, envURL string
		warn                  bool
	}{
		{"HTTP config", "http://polochon.example", "", true},
		{"HTTPS config", "https://polochon.example", "", false},
		{"HTTP environment override", "https://polochon.example", "http://env.example", true},
		{"HTTPS environment override", "http://polochon.example", "https://env.example", false},
		{"uppercase scheme", "HTTP://polochon.example", "", true},
		{"intentional local HTTP", "http://localhost:8080", "", true},
		{"credentials", "http://user:private-password@polochon.example/api?token=private-query", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("POLOCHON_URL", tc.envURL)
			t.Setenv("POLOCHON_TOKEN", "private-token")
			t.Setenv("POLOCHON_MPV", "")
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, fmt.Appendf(nil, "polochon_url: '%s'\n", tc.fileURL), 0o600); err != nil {
				t.Fatal(err)
			}
			config, err := LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			if _, err := NewAPIClient(config.PolochonURL, config.Token, slog.New(slog.NewTextHandler(&output, nil))); err != nil {
				t.Fatalf("HTTP warning must not prevent startup: %v", err)
			}
			wantURL := tc.fileURL
			if tc.envURL != "" {
				wantURL = tc.envURL
			}
			if config.PolochonURL != wantURL {
				t.Fatal("configured URL was changed")
			}
			if tc.warn {
				for _, want := range []string{"level=WARN", "polochon_url", "canonical HTTPS URL", "HTTP-to-HTTPS artwork redirects are blocked"} {
					if !strings.Contains(output.String(), want) {
						t.Errorf("warning %q does not contain %q", output.String(), want)
					}
				}
			} else if output.Len() != 0 {
				t.Errorf("HTTPS startup produced a warning: %s", &output)
			}
			for _, secret := range []string{wantURL, "private-token", "private-password", "private-query"} {
				if strings.Contains(output.String(), secret) {
					t.Error("warning leaked the configured URL or credentials")
				}
			}
		})
	}
}
