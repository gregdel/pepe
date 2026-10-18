package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlayerInterfaceRuntime(t *testing.T) {
	for _, profile := range []ControlProfile{ControlsKeyboard, ControlsHDMICEC} {
		for _, hidden := range []bool{false, true} {
			for _, native := range []bool{false, true} {
				t.Run(string(profile)+"/hidden="+boolText(hidden)+"/native="+boolText(native), func(t *testing.T) {
					visible := !hidden
					cfg := DefaultConfig()
					cfg.Controls, cfg.HelpBar = profile, &visible
					if native {
						cfg.MPVOSD = "native"
					}
					a := New(nil, nil, cfg, nil, nil)
					command, err := a.buildPlayerCommand(playbackSource{url: "https://example/video"})
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.RemoveAll(command.runtimeDir) })
					args := strings.Join(command.args, "\x00")
					wantScript := !native || profile == ControlsHDMICEC
					if strings.Contains(args, "--script=") != wantScript {
						t.Fatal("script opt-out/profile mismatch")
					}
					if command.args[len(command.args)-1] != "https://example/video" {
						t.Fatal("media URL is not last")
					}
					if !wantScript {
						return
					}
					data, err := os.ReadFile(filepath.Join(command.runtimeDir, "pepe.lua"))
					if err != nil || !bytes.Equal(data, playerOSDScript) {
						t.Fatalf("embedded script mismatch: %v", err)
					}
					for _, part := range []string{"pepe-controls=" + string(profile), "pepe-help=" + boolText(visible), "pepe-panel=" + boolText(!native), "pepe-web=yes"} {
						if !strings.Contains(args, part) {
							t.Fatalf("missing player option %s", part)
						}
					}
					if strings.Contains(args, "--osd-fonts-dir=") == native {
						t.Fatal("font opt-out mismatch")
					}
					err = filepath.WalkDir(command.runtimeDir, func(path string, entry os.DirEntry, err error) error {
						if err != nil {
							return err
						}
						info, err := entry.Info()
						if err != nil {
							return err
						}
						want := os.FileMode(0o600)
						if entry.IsDir() {
							want = 0o700
						}
						if info.Mode().Perm() != want {
							t.Errorf("insecure runtime file %s: %v", path, info.Mode())
						}
						return nil
					})
					if err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

func boolText(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
