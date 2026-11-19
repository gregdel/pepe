package app

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed mpv/pepe.lua
var playerOSDScript []byte

// playerInterfaceArgs only changes the player launched by pépé. Assets share
// the authentication/IPC runtime's lifetime, including managed recovery.
func (a *App) playerInterfaceArgs(runtimeDir string, fromWeb bool) ([]string, error) {
	if a.nativeOSD && a.controls != ControlsHDMICEC {
		return nil, nil
	}
	scriptPath := filepath.Join(runtimeDir, "pepe.lua")
	if err := os.WriteFile(scriptPath, playerOSDScript, 0o600); err != nil {
		return nil, fmt.Errorf("write playback interface: %w", err)
	}
	profile := ControlsKeyboard
	if a.controls == ControlsHDMICEC {
		profile = ControlsHDMICEC
	}
	yesNo := func(value bool) string {
		if value {
			return "yes"
		}
		return "no"
	}
	args := []string{
		"--script=" + scriptPath,
		"--script-opts-append=pepe-controls=" + string(profile),
		"--script-opts-append=pepe-help=" + yesNo(!a.hideHelp),
		"--script-opts-append=pepe-panel=" + yesNo(!a.nativeOSD),
		"--script-opts-append=pepe-web=" + yesNo(fromWeb),
	}
	if !a.nativeOSD {
		fontDir := filepath.Join(runtimeDir, "fonts")
		if err := os.Mkdir(fontDir, 0o700); err != nil {
			return nil, fmt.Errorf("create playback fonts directory: %w", err)
		}
		for _, font := range []struct {
			name string
			data []byte
		}{
			{"roboto.ttf", embeddedRoboto},
			{"roboto-italic.ttf", embeddedRobotoItalic},
		} {
			if err := os.WriteFile(filepath.Join(fontDir, font.name), font.data, 0o600); err != nil {
				return nil, fmt.Errorf("write playback font: %w", err)
			}
		}
		args = append(args, "--osd-fonts-dir="+fontDir, "--osd-font=Roboto",
			"--script-opts-append=console-background_alpha=0")
	}
	return args, nil
}
