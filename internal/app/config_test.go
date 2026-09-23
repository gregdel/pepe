package app

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLoadConfigFromFile(t *testing.T) {
	t.Setenv("POLOCHON_URL", "")
	t.Setenv("POLOCHON_TOKEN", "")
	t.Setenv("POLOCHON_MPV", "")

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`polochon_url: http://deck.local:8080
polochon_token: file-token
mpv_path: /usr/bin/mpv
fullscreen: true
player_fullscreen: true
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.PolochonURL != "http://deck.local:8080" {
		t.Fatalf("PolochonURL = %q", cfg.PolochonURL)
	}
	if cfg.Token != "file-token" {
		t.Fatalf("Token = %q", cfg.Token)
	}
	if cfg.MPVPath != "/usr/bin/mpv" {
		t.Fatalf("MPVPath = %q", cfg.MPVPath)
	}
	if !cfg.Fullscreen || !cfg.PlayerFullscreen {
		t.Fatalf("fullscreen flags = %v/%v", cfg.Fullscreen, cfg.PlayerFullscreen)
	}
}

func TestLoadConfigEnvOverridesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(`polochon_url: http://file.example
polochon_token: file-token
mpv_path: mpv
`), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("POLOCHON_URL", "http://env.example")
	t.Setenv("POLOCHON_TOKEN", "env-token")
	t.Setenv("POLOCHON_MPV", "/opt/mpv")

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.PolochonURL != "http://env.example" {
		t.Fatalf("PolochonURL = %q", cfg.PolochonURL)
	}
	if cfg.Token != "env-token" {
		t.Fatalf("Token = %q", cfg.Token)
	}
	if cfg.MPVPath != "/opt/mpv" {
		t.Fatalf("MPVPath = %q", cfg.MPVPath)
	}
}

func TestLoadConfigNormalizesFileValues(t *testing.T) {
	t.Setenv("POLOCHON_URL", "")
	t.Setenv("POLOCHON_TOKEN", "")
	t.Setenv("POLOCHON_MPV", "")

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("polochon_url: ' http://deck.local:8080 \t'\n"+
		"polochon_token: ' file-token \t'\n"+
		"mpv_path: ' \t'\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	want := Config{PolochonURL: "http://deck.local:8080", Token: "file-token", MPVPath: "mpv"}
	if *cfg != want {
		t.Fatalf("config = %#v, want %#v", *cfg, want)
	}
}

func TestLoadConfigMissingFileWithEnvironment(t *testing.T) {
	t.Setenv("POLOCHON_URL", "http://deck.local:8080")
	t.Setenv("POLOCHON_TOKEN", "env-token")
	t.Setenv("POLOCHON_MPV", "")

	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	want := Config{PolochonURL: "http://deck.local:8080", Token: "env-token", MPVPath: "mpv"}
	if *cfg != want {
		t.Fatalf("config = %#v, want %#v", *cfg, want)
	}
}

func TestLoadConfigWhitespaceEnvironmentOverridesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("polochon_url: http://file.example\npolochon_token: file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name, url, token string
	}{
		{"url", " \t", "env-token"},
		{"token", "http://env.example", " \t"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("POLOCHON_URL", test.url)
			t.Setenv("POLOCHON_TOKEN", test.token)
			if _, err := LoadConfig(path); err == nil {
				t.Fatal("LoadConfig succeeded with a blank environment override")
			}
		})
	}
}

func TestLoadConfigInvalidYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("polochon_url: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadConfig(path); err == nil {
		t.Fatal("LoadConfig succeeded for invalid YAML")
	}
}

func TestBuildMPVArgsUsesPathAndFullscreen(t *testing.T) {
	args := buildMPVArgs("/opt/bin/mpv", "Movie", "http://media", nil, PlayerModeGUI, 0, "/tmp/mpv.sock", true, "/tmp/auth.conf")

	if args[0] != "/opt/bin/mpv" {
		t.Fatalf("mpv path = %q", args[0])
	}
	if !slices.Contains(args, "--fs") {
		t.Fatalf("expected --fs in %v", args)
	}
	if !slices.Contains(args, "--no-border") {
		t.Fatalf("expected --no-border in %v", args)
	}
}
