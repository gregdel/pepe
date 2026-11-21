package app

import (
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	yaml "github.com/goccy/go-yaml"
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
	want := *DefaultConfig()
	want.PolochonURL, want.Token = "http://deck.local:8080", "file-token"
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
	want := *DefaultConfig()
	want.PolochonURL, want.Token = "http://deck.local:8080", "env-token"
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

// A URL main cannot build a client from must be refused before a settings
// save restarts pépé.
func TestValidateRejectsUnusableURL(t *testing.T) {
	for _, url := range []string{"http://nas:80a", "http://[fe80::1%eth0]:8080", "nas.local:8080", "ftp://nas", "http://", "/polochon"} {
		cfg := DefaultConfig()
		cfg.PolochonURL, cfg.Token = url, "token"
		if err := cfg.Validate(); err == nil {
			t.Errorf("Validate accepted %q", url)
		}
	}
	for _, url := range []string{"http://nas:8080", "https://polochon.example/api", "http://[fe80::1%25eth0]:8080"} {
		cfg := DefaultConfig()
		cfg.PolochonURL, cfg.Token = url, "token"
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate(%q) = %v", url, err)
		}
		if _, err := NewAPIClient(url, "token", slog.New(slog.DiscardHandler)); err != nil {
			t.Errorf("NewAPIClient(%q) = %v", url, err)
		}
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

func TestLoadConfigPlayerSettings(t *testing.T) {
	t.Setenv("POLOCHON_URL", "http://localhost")
	t.Setenv("POLOCHON_TOKEN", "token")
	t.Setenv("POLOCHON_MPV", "")

	for _, test := range []struct {
		name    string
		yaml    string
		managed bool
		startup time.Duration
		stop    time.Duration
	}{
		{"empty", "", false, 3 * time.Second, 2 * time.Second},
		{"comments", "# Playback defaults\n", false, 3 * time.Second, 2 * time.Second},
		{"empty document", "---\n", false, 3 * time.Second, 2 * time.Second},
		{"defaults", "mpv_path: mpv\n", false, 3 * time.Second, 2 * time.Second},
		{"managed", "mpv_managed_launcher: true\nmpv_startup_timeout: 45s\nmpv_stop_timeout: 30s\n", true, 45 * time.Second, 30 * time.Second},
		{"partial", "mpv_stop_timeout: 500ms\n", false, 3 * time.Second, 500 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(test.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			config, err := LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			if config.MPVManagedLauncher != test.managed || config.MPVStartupTimeout != test.startup || config.MPVStopTimeout != test.stop {
				t.Fatalf("playback settings = %+v", config)
			}
			a := New(nil, nil, config, nil, nil)
			if a.playerPolicy.managedLauncher != test.managed || a.playerPolicy.connectTimeout != test.startup || a.playerPolicy.stopTimeout != test.stop {
				t.Fatalf("application policy = %+v", a.playerPolicy)
			}
		})
	}
}

func TestLoadConfigRejectsInvalidPlayerSettings(t *testing.T) {
	t.Setenv("POLOCHON_URL", "http://localhost")
	t.Setenv("POLOCHON_TOKEN", "token")
	t.Setenv("POLOCHON_MPV", "")

	for _, key := range []string{"mpv_startup_timeout", "mpv_stop_timeout"} {
		for _, value := range []string{"0s", "-1s", "invalid", "30", "true", "[]"} {
			t.Run(key+"/"+value, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "config.yaml")
				if err := os.WriteFile(path, []byte(key+": "+value+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				_, err := LoadConfig(path)
				if err == nil {
					t.Fatal("invalid player setting accepted")
				}
				if (value == "0s" || value == "-1s") && !strings.Contains(err.Error(), key) {
					t.Fatalf("error does not identify setting: %v", err)
				}
			})
		}
	}
}

func TestLoadConfigWebUIAddress(t *testing.T) {
	t.Setenv("POLOCHON_URL", "http://localhost")
	t.Setenv("POLOCHON_TOKEN", "token")
	for _, tc := range []struct {
		name, yaml, want string
		invalid          bool
	}{
		{"omitted", "", "", false},
		{"empty", "web_ui_addr: ''\n", "", false},
		{"whitespace", "web_ui_addr: '  '\n", "", false},
		{"LAN", "web_ui_addr: ':8081'\n", ":8081", false},
		{"loopback", "web_ui_addr: ' 127.0.0.1:8081 '\n", "127.0.0.1:8081", false},
		{"IPv6", "web_ui_addr: '[::1]:8081'\n", "[::1]:8081", false},
		{"ephemeral", "web_ui_addr: 'localhost:0'\n", "localhost:0", false},
		{"URL", "web_ui_addr: 'http://localhost:8081'\n", "", true},
		{"missing port", "web_ui_addr: 'localhost'\n", "", true},
		{"named port", "web_ui_addr: ':http'\n", "", true},
		{"large port", "web_ui_addr: ':65536'\n", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(path)
			if tc.invalid {
				if err == nil || !strings.Contains(err.Error(), "web_ui_addr") {
					t.Fatalf("invalid web address error = %v", err)
				}
				return
			}
			if err != nil || cfg.WebUIAddr != tc.want {
				t.Fatalf("config = %+v, error %v", cfg, err)
			}
		})
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

func TestReadConfigFileIgnoresEnvironment(t *testing.T) {
	t.Setenv("POLOCHON_URL", "http://env.example")
	t.Setenv("POLOCHON_TOKEN", "env-token")
	t.Setenv("POLOCHON_MPV", "/opt/mpv")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("polochon_url: ' http://file.example '\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := ReadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PolochonURL != "http://file.example" || cfg.Token != "" || cfg.MPVPath != "mpv" {
		t.Fatalf("file config = %+v", cfg)
	}
	if got := EnvOverrides(); len(got) != 3 || got["polochon_url"] != "POLOCHON_URL" || got["mpv_path"] != "POLOCHON_MPV" {
		t.Fatalf("EnvOverrides = %v", got)
	}
}

func TestSaveConfigFileKeepsCommentsAndOtherKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := `# pépé configuration
polochon_url: https://polochon.example # server
polochon_token: replace-me
# Use hdmi-cec for remote colour hints.
controls: keyboard
mpv_startup_timeout: 5s
custom_key: kept
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := ReadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.PolochonURL = "http://deck.local:8080"
	cfg.Token = "secret"
	cfg.Controls = ControlsHDMICEC
	cfg.WebUIAddr = ":8081"
	cfg.Fullscreen = true
	if err := SaveConfigFile(path, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"# pépé configuration", "# Use hdmi-cec for remote colour hints.", "custom_key: kept", "mpv_startup_timeout: 5s", "# server"} {
		if !strings.Contains(text, want) {
			t.Fatalf("saved config lost %q:\n%s", want, text)
		}
	}
	saved, err := ReadConfigFile(path)
	if err != nil {
		t.Fatalf("saved config does not parse: %v\n%s", err, text)
	}
	if saved.PolochonURL != cfg.PolochonURL || saved.Token != "secret" || saved.Controls != ControlsHDMICEC ||
		saved.WebUIAddr != ":8081" || !saved.Fullscreen || saved.MPVStartupTimeout != 5*time.Second ||
		(saved.HelpBar != nil && !*saved.HelpBar) || saved.MPVOSD != "pepe" {
		t.Fatalf("saved config = %+v\n%s", saved, text)
	}
	for _, unchanged := range []string{"mpv_path", "help_bar", "mpv_osd", "player_fullscreen"} {
		if strings.Contains(text, unchanged) {
			t.Fatalf("unchanged %s was written:\n%s", unchanged, text)
		}
	}
	if strings.Index(text, "polochon_url") > strings.Index(text, "custom_key") {
		t.Fatalf("existing keys were reordered:\n%s", text)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, %v", info, err)
	}
}

func TestSaveConfigFileCreatesMissingFile(t *testing.T) {
	for name, content := range map[string]*string{"missing": nil, "empty": new(string), "comment only": ptr("# nothing yet\n")} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "pepe", "config.yaml")
			if content != nil {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(*content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cfg := DefaultConfig()
			cfg.PolochonURL, cfg.Token = "http://p", "t"
			if err := SaveConfigFile(path, cfg); err != nil {
				t.Fatal(err)
			}
			t.Setenv("POLOCHON_URL", "")
			t.Setenv("POLOCHON_TOKEN", "")
			t.Setenv("POLOCHON_MPV", "")
			saved, err := LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			if saved.PolochonURL != "http://p" || saved.Token != "t" {
				t.Fatalf("saved = %+v", saved)
			}
			if data, _ := os.ReadFile(path); strings.Contains(string(data), "mpv_path") {
				t.Fatalf("unchanged defaults were written: %q", data)
			}
			if content != nil && *content != "" {
				if data, _ := os.ReadFile(path); !strings.HasPrefix(string(data), *content) {
					t.Fatalf("comment lost: %q", data)
				}
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

// go-yaml leaves these strings unquoted, where they would read back as floats.
func TestSaveConfigFileQuotesSpecialStrings(t *testing.T) {
	for _, existing := range []string{"", "polochon_token: old # token\n"} {
		for _, token := range []string{".inf", "-.Inf", ".NaN", "plain"} {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg := DefaultConfig()
			cfg.Token = token
			if err := SaveConfigFile(path, cfg); err != nil {
				t.Fatalf("existing %q, token %q: %v", existing, token, err)
			}
			saved, err := ReadConfigFile(path)
			if err != nil || saved.Token != token {
				t.Fatalf("existing %q, token %q: read back %+v, %v", existing, token, saved, err)
			}
		}
	}
}

func TestSaveConfigFileFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles.yaml")
	link := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(target, []byte("polochon_url: old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.PolochonURL, cfg.Token = "new", "t"
	if err := SaveConfigFile(link, cfg); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink replaced: %v, %v", info, err)
	}
	if saved, err := ReadConfigFile(target); err != nil || saved.PolochonURL != "new" {
		t.Fatalf("target = %+v, %v", saved, err)
	}
}

func TestSaveConfigFileRefusesUnparsableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	broken := "polochon_url: [unterminated\n"
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.PolochonURL, cfg.Token = "http://p", "t"
	if err := SaveConfigFile(path, cfg); err == nil {
		t.Fatal("unparsable config was replaced")
	}
	if data, _ := os.ReadFile(path); string(data) != broken {
		t.Fatalf("file changed: %q", data)
	}
}

func TestSaveConfigFileUnusualKeys(t *testing.T) {
	for _, test := range []struct{ name, content, want string }{
		{"double-quoted key", "\"polochon_url\": http://old\n", ""},
		{"single-quoted key", "'polochon_url': http://old\n", ""},
		{"explicit key", "? polochon_url\n: http://old\n", ""},
		{"comment before value", "polochon_url: # server\n  http://old\n", "# server"},
		{"anchor", "polochon_url: &url http://old # server\nother: *url\n", "&url"},
		{"tag", "polochon_url: !!str http://old\n", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := ReadConfigFile(path)
			if err != nil {
				t.Fatal(err)
			}
			cfg.PolochonURL, cfg.Token = "http://new", "t"
			if err := SaveConfigFile(path, cfg); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(path)
			saved, err := ReadConfigFile(path)
			if err != nil {
				t.Fatalf("saved config does not parse: %v\n%s", err, data)
			}
			if saved.PolochonURL != "http://new" || strings.Count(string(data), "polochon_url") != 1 || !strings.Contains(string(data), test.want) {
				t.Fatalf("saved config:\n%s", data)
			}
			if test.name == "anchor" {
				var other struct{ Other string }
				if err := yaml.Unmarshal(data, &other); err != nil || other.Other != "http://new" {
					t.Fatalf("alias = %q, %v\n%s", other.Other, err, data)
				}
			}
		})
	}
}

func TestSaveConfigFileKeepsMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("polochon_url: old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.PolochonURL, cfg.Token = "new", "t"
	if err := SaveConfigFile(path, cfg); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v, %v", info, err)
	}
}

func TestEnvOverridesMatchApplyEnv(t *testing.T) {
	t.Setenv("POLOCHON_URL", " \t")
	t.Setenv("POLOCHON_TOKEN", "")
	t.Setenv("POLOCHON_MPV", " ")
	got := EnvOverrides()
	if len(got) != 1 || got["polochon_url"] != "POLOCHON_URL" {
		t.Fatalf("EnvOverrides = %v", got)
	}
	cfg := DefaultConfig()
	cfg.PolochonURL, cfg.Token, cfg.MPVPath = "http://file", "file", "/file/mpv"
	cfg.applyEnv()
	if cfg.PolochonURL != "" || cfg.Token != "file" || cfg.MPVPath != "/file/mpv" {
		t.Fatalf("applyEnv = %+v", cfg)
	}
}

func TestSaveConfigFileRefusesUpdateThatDoesNotReadBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	// goccy rejects an explicit key next to the same merged key.
	content := "base: &b\n  polochon_url: http://old\n<<: *b\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := ReadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.PolochonURL, cfg.Token = "http://new", "t"
	if err := SaveConfigFile(path, cfg); err == nil {
		t.Fatal("an update that does not parse was written")
	}
	if data, _ := os.ReadFile(path); string(data) != content {
		t.Fatalf("file changed: %q", data)
	}
}
