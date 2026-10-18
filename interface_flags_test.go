package main

import (
	"flag"
	"testing"

	"github.com/gregdel/pepe/internal/app"
)

func TestInterfaceFlagOverrides(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		profile app.ControlProfile
		help    bool
		osd     string
		invalid bool
	}{
		{"unset preserves YAML", nil, app.ControlsHDMICEC, false, "native", false},
		{"explicit defaults", []string{"-controls=keyboard", "-help-bar=true", "-mpv-osd=pepe"}, app.ControlsKeyboard, true, "pepe", false},
		{"independent help", []string{"-help-bar=true"}, app.ControlsHDMICEC, true, "native", false},
		{"independent controls", []string{"-controls=keyboard"}, app.ControlsKeyboard, false, "native", false},
		{"invalid controls", []string{"-controls=remote"}, "", false, "", true},
		{"empty controls", []string{"-controls="}, "", false, "", true},
		{"invalid overlay", []string{"-mpv-osd=unknown"}, "", false, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := app.DefaultConfig()
			hidden := false
			config.Controls, config.HelpBar, config.MPVOSD = app.ControlsHDMICEC, &hidden, "native"
			flags := flag.NewFlagSet(tc.name, flag.ContinueOnError)
			f := registerInterfaceFlags(flags)
			if err := flags.Parse(tc.args); err != nil {
				t.Fatal(err)
			}
			err := f.apply(config, flags)
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid interface accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if config.Controls != tc.profile || *config.HelpBar != tc.help || config.MPVOSD != tc.osd {
				t.Fatalf("flags applied incorrectly: %+v", config)
			}
			if config.MPVPath != "mpv" {
				t.Fatal("interface flags changed playback path")
			}
		})
	}
}
