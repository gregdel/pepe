package app

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Opt-in real-mpv test: synthetic video, null outputs, no server or user config.
// PEPE_TEST_MPV=1 go test ./internal/app -run TestMPVInterfaceIntegration -count=1
// With PEPE_MPV_SCREENSHOT_DIR and an isolated DISPLAY, also render X11 captures
// at PEPE_MPV_SCREENSHOT_SIZE (default 1280x720) and click the native mouse OSC.
func TestMPVInterfaceIntegration(t *testing.T) {
	if os.Getenv("PEPE_TEST_MPV") != "1" {
		t.Skip("set PEPE_TEST_MPV=1 to exercise the installed mpv")
	}
	for _, profile := range []ControlProfile{ControlsKeyboard, ControlsHDMICEC} {
		t.Run(string(profile), func(t *testing.T) {
			dir := t.TempDir()
			a := New(nil, nil, nil, nil, nil)
			a.controls = profile
			args, err := a.playerInterfaceArgs(dir, false)
			if err != nil {
				t.Fatal(err)
			}
			socket := filepath.Join(dir, "mpv.sock")
			args = append([]string{"--no-config", "--load-scripts=no", "--vo=null", "--ao=null", "--pause=yes", "--terminal=yes", "--input-terminal=no", "--term-status-msg=", "--input-ar-delay=100", "--input-ar-rate=10", "--input-ipc-server=" + socket}, args...)
			shotDir := os.Getenv("PEPE_MPV_SCREENSHOT_DIR")
			shotSize := os.Getenv("PEPE_MPV_SCREENSHOT_SIZE")
			if shotSize == "" {
				shotSize = "1280x720"
			}
			if shotDir != "" {
				if err := os.MkdirAll(shotDir, 0o700); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--vo=x11", "--force-window=yes", "--geometry="+shotSize)
			}
			args = append(args, "--force-media-title=Fixture Movie (2026)", "--msg-level=all=warn", "av://lavfi:color=size=160x90:rate=1")
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			logPath := filepath.Join(dir, "mpv.log")
			log, err := os.Create(logPath)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = log.Close() }()
			cmd := exec.CommandContext(ctx, "mpv", args...)
			cmd.Stdout, cmd.Stderr = log, log
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				if t.Failed() {
					data, _ := os.ReadFile(logPath)
					t.Log(string(data))
				}
			}()
			var conn net.Conn
			until := time.Now().Add(5 * time.Second)
			for time.Now().Before(until) {
				conn, err = net.Dial("unix", socket)
				if err == nil {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			decoder, encoder := json.NewDecoder(conn), json.NewEncoder(conn)
			requestID := 0
			request := func(command ...any) json.RawMessage {
				t.Helper()
				requestID++
				if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
					t.Fatal(err)
				}
				if err := encoder.Encode(map[string]any{"command": command, "request_id": requestID}); err != nil {
					t.Fatal(err)
				}
				for {
					var response struct {
						RequestID int             `json:"request_id"`
						Error     string          `json:"error"`
						Data      json.RawMessage `json:"data"`
					}
					if err := decoder.Decode(&response); err != nil {
						t.Fatal(err)
					}
					if response.RequestID != requestID {
						continue
					}
					if (response.Error == "property not found" || response.Error == "property unavailable") && command[0] == "get_property" {
						return json.RawMessage("null") // Scripts and media loading publish properties asynchronously.
					}
					if response.Error != "success" {
						t.Fatalf("mpv command %v: %s", command, response.Error)
					}
					return response.Data
				}
			}
			wait := func(property, expected string) {
				t.Helper()
				var want, got any
				if err := json.Unmarshal([]byte(expected), &want); err != nil {
					t.Fatal(err)
				}
				deadline := time.Now().Add(3 * time.Second)
				for time.Now().Before(deadline) {
					if err := json.Unmarshal(request("get_property", property), &got); err != nil {
						t.Fatal(err)
					}
					if reflect.DeepEqual(got, want) {
						return
					}
					time.Sleep(20 * time.Millisecond)
				}
				t.Fatalf("mpv property %s = %v, want %s", property, got, expected)
			}
			waitKeyOwner := func(key, owner string) {
				t.Helper()
				deadline := time.Now().Add(3 * time.Second)
				for time.Now().Before(deadline) {
					var bindings []struct {
						Key, Owner string
						Priority   int
					}
					if err := json.Unmarshal(request("get_property", "input-bindings"), &bindings); err != nil {
						t.Fatal(err)
					}
					best, got := -1, ""
					for _, binding := range bindings {
						if binding.Key == key && binding.Priority > best {
							best, got = binding.Priority, binding.Owner
						}
					}
					if got == owner {
						return
					}
					time.Sleep(20 * time.Millisecond)
				}
				t.Fatalf("%s binding did not return to %s", key, owner)
			}
			wait("idle-active", "false")
			if profile == ControlsHDMICEC {
				waitKeyOwner("ENTER", "pepe")
			}
			bindings := string(request("get_property", "input-bindings"))
			if strings.Contains(bindings, "pepe/cec-") != (profile == ControlsHDMICEC) {
				t.Fatal("playback overrides are not profile-scoped")
			}
			capture := func(name string) {
				t.Helper()
				if shotDir == "" {
					return
				}
				time.Sleep(300 * time.Millisecond)
				request("screenshot-to-file", filepath.Join(shotDir, "mpv-"+string(profile)+"-"+shotSize+"-"+name+".png"), "window")
			}
			if shotDir != "" {
				moveMouseToControls := func() (int, int) {
					t.Helper()
					var width, height int
					if err := json.Unmarshal(request("get_property", "osd-width"), &width); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(request("get_property", "osd-height"), &height); err != nil {
						t.Fatal(err)
					}
					request("mouse", width/2, height-height/48)
					return width, height
				}
				for _, shade := range []string{"white", "black"} {
					source := "av://lavfi:color=c=" + shade + ":size=1920x1080:rate=1"
					request("loadfile", source)
					encoded, _ := json.Marshal(source)
					wait("path", string(encoded))
					wait("pause", "true")
					// Exercise explicit help alongside automatic pause guidance.
					request("script-binding", "pepe/toggle-help")
					capture(shade)
					request("mouse", 1, 1)
					moveMouseToControls()
					capture(shade + "-mouse")
				}
				// A paused panel must survive mouse activity and both
				// fullscreen transitions alongside the native controls.
				moveMouseToControls()
				request("keypress", "f")
				wait("fullscreen", "true")
				wait("pause", "true")
				capture("fullscreen")
				request("keypress", "f")
				wait("fullscreen", "false")
				capture("windowed")
				// Exercise native OSC pointer routing without installing mouse bindings.
				width, height := moveMouseToControls()
				capture("mouse")
				// Default built-in bottombar's play/pause button; first let its
				// asynchronous mouse-area update activate that hit region.
				request("mouse", width/64, height-height/48)
				time.Sleep(100 * time.Millisecond)
				request("keydown", "MBTN_LEFT")
				request("keyup", "MBTN_LEFT")
				wait("pause", "false")
				request("keydown", "MBTN_LEFT")
				request("keyup", "MBTN_LEFT")
				wait("pause", "true")
				capture("mouse-paused")
			}
			for _, name := range []string{"English", "French"} {
				path := filepath.Join(dir, name+".srt")
				if err := os.WriteFile(path, []byte("1\n00:00:00,000 --> 00:01:00,000\nSubtitle fixture\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				request("sub-add", path, "auto", name)
			}
			request("set_property", "sid", 1)
			cycleKey := "j"
			if profile == ControlsHDMICEC {
				cycleKey = "s"
			}
			request("keypress", cycleKey)
			wait("sid", "2")
			request("set_property", "sid", 1)
			request("keypress", "TAB")
			wait("user-data/mpv/console/open", "true")
			waitKeyOwner("ENTER", "console")
			capture("subtitles")
			request("keypress", "DOWN")
			request("keypress", "ENTER")
			wait("user-data/mpv/console/open", "false")
			wait("sid", "2")
			wait("pause", "true") // Enter selected a track, not pause/resume.
			request("keypress", "TAB")
			wait("user-data/mpv/console/open", "true")
			waitKeyOwner("ENTER", "console")
			request("keypress", "ESC")
			wait("user-data/mpv/console/open", "false") // Back closed the list, not mpv.
			if profile == ControlsKeyboard {
				// The native two-key subtitle menu remains available too.
				request("keypress", "g")
				request("keypress", "s")
				wait("user-data/mpv/console/open", "true")
				request("keypress", "ESC")
				wait("user-data/mpv/console/open", "false")
				return
			}
			waitKeyOwner("ENTER", "pepe")
			request("keypress", "ENTER")
			wait("pause", "false") // Original playback binding restored.

			// A tiny, seekable ten-minute video needs no encoder or downloads.
			fixture := filepath.Join(dir, "seek.y4m")
			video := "YUV4MPEG2 W2 H2 F1:1 Ip A1:1 C420\n" +
				strings.Repeat("FRAME\n\x10\x10\x10\x10\x80\x80", 600)
			if err := os.WriteFile(fixture, []byte(video), 0o600); err != nil {
				t.Fatal(err)
			}
			request("set_property", "pause", true)
			request("loadfile", fixture)
			wait("duration", "600")
			wait("seekable", "true")
			waitKeyOwner("F9", "pepe")
			waitKeyOwner("F10", "pepe")
			holdSeek := func(key string, direction float64) {
				t.Helper()
				request("seek", 300, "absolute+exact")
				wait("time-pos", "300")
				request("keydown", key)
				defer request("keyup", key)
				deadline := time.Now().Add(2 * time.Second)
				var position float64
				for time.Now().Before(deadline) {
					if err := json.Unmarshal(request("get_property", "time-pos"), &position); err != nil {
						t.Fatal(err)
					}
					if (position-300)*direction >= 20 {
						return // A held key must seek more than a single ten-second step.
					}
					time.Sleep(20 * time.Millisecond)
				}
				t.Fatalf("holding %s did not repeat seeking: 300 -> %g", key, position)
			}
			holdSeek("F9", -1)
			holdSeek("F10", 1)
			wait("pause", "true")
		})
	}
}
