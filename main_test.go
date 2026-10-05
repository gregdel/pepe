package main

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gregdel/pepe/internal/app"
)

func TestMPVFlagOverrides(t *testing.T) {
	t.Setenv("POLOCHON_URL", "http://localhost")
	t.Setenv("POLOCHON_TOKEN", "token")
	t.Setenv("POLOCHON_MPV", "/opt/mpv")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("mpv_managed_launcher: true\nmpv_startup_timeout: 45s\nmpv_stop_timeout: 30s\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name    string
		args    []string
		managed bool
		startup time.Duration
		stop    time.Duration
	}{
		{"unset", nil, true, 45 * time.Second, 30 * time.Second},
		{"explicit false", []string{"-mpv-managed-launcher=false"}, false, 45 * time.Second, 30 * time.Second},
		{"durations", []string{"-mpv-startup-timeout=10s", "-mpv-stop-timeout=5s"}, true, 10 * time.Second, 5 * time.Second},
		{"explicit defaults", []string{"-mpv-startup-timeout=3s", "-mpv-stop-timeout=2s"}, true, 3 * time.Second, 2 * time.Second},
		{"partial", []string{"-mpv-stop-timeout=500ms"}, true, 45 * time.Second, 500 * time.Millisecond},
		{"unrelated", []string{"-fullscreen"}, true, 45 * time.Second, 30 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, err := app.LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			flags := flag.NewFlagSet(test.name, flag.ContinueOnError)
			flags.Bool("fullscreen", false, "")
			playerFlags := registerMPVFlags(flags)
			if err := flags.Parse(test.args); err != nil {
				t.Fatal(err)
			}
			if err := playerFlags.apply(config, flags); err != nil {
				t.Fatal(err)
			}
			if config.MPVManagedLauncher != test.managed || config.MPVStartupTimeout != test.startup || config.MPVStopTimeout != test.stop {
				t.Fatalf("playback settings = %+v", config)
			}
			if config.MPVPath != "/opt/mpv" || config.PolochonURL != "http://localhost" || config.Token != "token" {
				t.Fatal("player flags changed other configuration")
			}
		})
	}
}

func TestWebUIFlagOverrides(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		want    string
		invalid bool
	}{
		{"unset preserves YAML", nil, ":8081", false},
		{"override", []string{"-web-ui-addr=127.0.0.1:8082"}, "127.0.0.1:8082", false},
		{"disable", []string{"-web-ui-addr="}, "", false},
		{"whitespace disables", []string{"-web-ui-addr=  "}, "", false},
		{"invalid", []string{"-web-ui-addr=8081"}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flags := flag.NewFlagSet(tc.name, flag.ContinueOnError)
			addr := flags.String("web-ui-addr", "", "")
			if err := flags.Parse(tc.args); err != nil {
				t.Fatal(err)
			}
			config := app.DefaultConfig()
			config.WebUIAddr = ":8081"
			err := applyWebUIAddr(config, flags, *addr)
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid address accepted")
				}
				return
			}
			if err != nil || config.WebUIAddr != tc.want {
				t.Fatalf("address = %q, error %v", config.WebUIAddr, err)
			}
		})
	}
	flags := flag.NewFlagSet("defaults", flag.ContinueOnError)
	addr := flags.String("web-ui-addr", "", "")
	config := app.DefaultConfig()
	if err := applyWebUIAddr(config, flags, *addr); err != nil || config.WebUIAddr != "" {
		t.Fatal("web UI enabled by default")
	}
}

func TestMPVFlagDefaultsAndManagedOptIn(t *testing.T) {
	flags := flag.NewFlagSet("mpv", flag.ContinueOnError)
	playerFlags := registerMPVFlags(flags)
	if playerFlags.managedLauncher || playerFlags.startupTimeout != 3*time.Second || playerFlags.stopTimeout != 2*time.Second {
		t.Fatalf("flag defaults = %+v", playerFlags)
	}
	if err := flags.Parse([]string{"-mpv-managed-launcher"}); err != nil {
		t.Fatal(err)
	}
	config := app.DefaultConfig()
	if err := playerFlags.apply(config, flags); err != nil {
		t.Fatal(err)
	}
	if !config.MPVManagedLauncher {
		t.Fatal("managed flag did not enable launcher policy")
	}
}

func TestMPVFlagsRejectInvalidDurations(t *testing.T) {
	for _, name := range []string{"mpv-startup-timeout", "mpv-stop-timeout"} {
		for _, value := range []string{"0s", "-1s", "invalid", "30"} {
			t.Run(name+"/"+value, func(t *testing.T) {
				flags := flag.NewFlagSet(name, flag.ContinueOnError)
				flags.SetOutput(io.Discard)
				playerFlags := registerMPVFlags(flags)
				parseErr := flags.Parse([]string{"-" + name + "=" + value})
				if value == "invalid" || value == "30" {
					if parseErr == nil {
						t.Fatal("malformed duration accepted")
					}
					return
				}
				if parseErr != nil {
					t.Fatal(parseErr)
				}
				err := playerFlags.apply(app.DefaultConfig(), flags)
				if err == nil || !strings.Contains(err.Error(), strings.ReplaceAll(name, "-", "_")) {
					t.Fatalf("invalid timeout error = %v", err)
				}
			})
		}
	}
}
