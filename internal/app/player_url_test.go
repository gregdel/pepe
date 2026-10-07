package app

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"gioui.org/op/paint"
)

func TestValidatePlaybackURL(t *testing.T) {
	for _, raw := range []string{
		"http://media.example/video.mkv", "https://media.example/video?signature=a%2Fb+z&x=1#part",
		" https://media.example/video ", "http://127.0.0.1:8080/video", "https://[::1]/video",
	} {
		got, err := validatePlaybackURL(raw)
		if err != nil || got != strings.TrimSpace(raw) {
			t.Fatalf("validatePlaybackURL(%q) = %q, %v", raw, got, err)
		}
	}
	for _, raw := range []string{
		"", "file:///etc/passwd", "/video.mkv", "rtsp://media/video", "//media/video", "https:///video",
		"https://media/%zz", "https://media/video\n", "https://media/\x00", "https://media/\xff",
		"--include=/file", "https://media/" + strings.Repeat("x", maxPlaybackURLLength),
	} {
		if _, err := validatePlaybackURL(raw); err == nil {
			t.Fatalf("accepted invalid URL %q", raw)
		}
	}
}

func TestWebPlaybackCommandHasNoCatalogCredentialsOrResume(t *testing.T) {
	t.Setenv("POLOCHON_TOKEN", "private-polochon-token")
	for _, managed := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "managed"}[managed], func(t *testing.T) {
			client, err := NewAPIClient("http://localhost", "private-polochon-token", nil)
			if err != nil {
				t.Fatal(err)
			}
			a := New(client, nil, &Config{MPVPath: "/opt/mpv", PlayerFullscreen: true, MPVManagedLauncher: managed}, nil, nil)
			defer a.Close()
			a.resumePos[""] = 123
			videoURL := "https://media.example/video?signature=a%2Fb+z&x=1"
			command, err := a.buildPlayerCommand(playbackSource{title: "Web UI video", url: videoURL})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.RemoveAll(command.runtimeDir) }()
			if command.mediaKey != "" || !command.fromWeb || command.args[0] != "/opt/mpv" || command.args[len(command.args)-1] != videoURL || !slices.Contains(command.args, "--fs") {
				t.Fatalf("web command = %+v", command)
			}
			args := strings.Join(command.args, "\n")
			if strings.Contains(args, "--start=") || strings.Contains(args, "--sub-file=") || strings.Contains(args, "private-polochon-token") || strings.Contains(args, "--title=") || strings.Contains(args, "--force-media-title=") {
				t.Fatalf("catalog data in web arguments: %s", args)
			}
			if strings.Contains(args, "--managed-runtime=") != managed {
				t.Fatal("managed policy not preserved")
			}
			data, err := os.ReadFile(filepath.Join(command.runtimeDir, "auth.conf"))
			if err != nil || len(data) != 0 {
				t.Fatalf("web include must be empty: %q, %v", data, err)
			}
			if slices.Contains(mpvEnvironment(os.Environ()), "POLOCHON_TOKEN=private-polochon-token") {
				t.Fatal("token remained in player environment")
			}
		})
	}
	// URL command construction also works with a completely non-networked client.
	a := New(nil, nil, nil, nil, nil)
	defer a.Close()
	command, err := a.buildPlayerCommand(playbackSource{url: "http://media/video"})
	if err != nil {
		t.Fatal(err)
	}
	_ = os.RemoveAll(command.runtimeDir)
}

func TestInvalidWebURLDoesNotStopPlayback(t *testing.T) {
	stop := make(chan struct{})
	a := &App{playerStatus: PlayerStatusRunning, playerStop: stop}
	if err := a.launchURL("file:///etc/passwd"); err == nil {
		t.Fatal("invalid URL accepted")
	}
	if a.pendingPlayback != nil || a.playerStopping {
		t.Fatal("invalid URL changed playback")
	}
	select {
	case <-stop:
		t.Fatal("invalid URL stopped playback")
	default:
	}
}

func TestWebAndCatalogReplacementUsesLatestURL(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "launches")
	script := filepath.Join(dir, "fake-mpv")
	body := `#!/bin/sh
trap 'printf "stopped\n" >> "$PEPE_URL_LOG"; exit 0' TERM
for arg do last=$arg; done
printf '%s\n' "$last" >> "$PEPE_URL_LOG"
while :; do sleep 0.05; done
`
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PEPE_URL_LOG", logPath)
	client, err := NewAPIClient("http://localhost", "private-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	movie := testMovie("catalog", "Catalog", 2020)
	client.movies[movie.ImdbID] = movie
	a := New(client, nil, &Config{MPVPath: script}, nil, nil)
	t.Cleanup(func() {
		a.pendingPlayback = nil
		a.requestPlayerStop()
		if a.playerDone != nil {
			<-a.playerDone
		}
		a.Close()
	})
	catalogURL, err := client.getDownloadURL(movie)
	if err != nil {
		t.Fatal(err)
	}
	media := &MediaItem{ID: movie.ImdbID, Title: "Catalog", Type: mediaKindMovie}
	a.launchMovie(media)
	waitForCache(t, func() bool {
		data, _ := os.ReadFile(logPath)
		return string(data) == catalogURL+"\n"
	})
	firstDone := a.playerDone
	for _, videoURL := range []string{"https://media/first", "https://media/latest"} {
		if err := a.launchURL(videoURL); err != nil {
			t.Fatal(err)
		}
	}
	if a.playerDone != firstDone || a.pendingPlayback.url != "https://media/latest" || a.playerFromWeb {
		t.Fatal("URL replacement did not retain the active catalog player")
	}
	waitForCache(t, func() bool { return len(firstDone) == 1 })
	a.drainChannels()
	waitForCache(t, func() bool {
		data, _ := os.ReadFile(logPath)
		return strings.Contains(string(data), "https://media/latest\n")
	})
	data, _ := os.ReadFile(logPath)
	if string(data) != catalogURL+"\nstopped\nhttps://media/latest\n" || !a.playerFromWeb {
		t.Fatalf("replacement launches = %q", data)
	}
	webDone := a.playerDone
	a.launchMovie(media)
	waitForCache(t, func() bool { return len(webDone) == 1 })
	a.drainChannels()
	waitForCache(t, func() bool {
		data, _ := os.ReadFile(logPath)
		return strings.HasSuffix(string(data), "stopped\n"+catalogURL+"\n")
	})
	if a.playerFromWeb || a.playerTitle != "Catalog (2020)" {
		t.Fatal("catalog replacement retained web identity")
	}
}

func TestWebURLManagedCompletionAndBlocking(t *testing.T) {
	for _, confirmed := range []bool{false, true} {
		t.Run(fmt.Sprintf("confirmed=%t", confirmed), func(t *testing.T) {
			a := New(nil, nil, &Config{MPVManagedLauncher: true}, nil, nil)
			defer a.Close()
			body := "exit 0\n"
			if confirmed {
				body = confirmRuntime + body
			}
			launcher := lifecycleCommand(t, a, body)
			a.mpvPath = launcher.args[0]
			if err := a.launchURL("https://media/video"); err != nil || a.playerStatus != PlayerStatusRunning {
				t.Fatalf("managed URL launch = %v, %s", err, a.playerError)
			}
			result := lifecycleResult(t, a)
			defer func() { _ = os.RemoveAll(result.retainedRuntime) }()
			a.playerDone <- result
			a.drainChannels()
			if confirmed {
				if a.playerStatus != PlayerStatusIdle || a.playerCleanupDir != "" {
					t.Fatal("confirmed URL completion failed")
				}
			} else {
				if a.playerStatus != PlayerStatusError || a.playerCleanupDir == "" {
					t.Fatal("unconfirmed URL teardown was not retained")
				}
				if err := a.launchURL("https://media/next"); err != nil || a.playerStatus != PlayerStatusError || a.playerDone != nil {
					t.Fatal("URL replacement bypassed teardown block")
				}
			}
		})
	}
}

func TestHeadlessWebPlayingBar(t *testing.T) {
	for _, size := range []image.Point{image.Pt(1920, 1080), image.Pt(1024, 768)} {
		for _, shade := range []uint8{16, 255} {
			t.Run(fmt.Sprintf("%dx%d/art-%d", size.X, size.Y, shade), func(t *testing.T) {
				f := newHeadlessFixture(t)
				art := image.NewNRGBA(image.Rect(0, 0, 64, 64))
				draw.Draw(art, art.Bounds(), image.NewUniform(color.NRGBA{R: shade, G: shade, B: shade, A: 255}), image.Point{}, draw.Src)
				for _, key := range []string{"fanart:movie-new", "thumb:movie-new", "fanart:show-one", "poster:show-one"} {
					f.app.images.store(key, paint.NewImageOp(art))
				}
				f.app.playerStatus = PlayerStatusRunning
				f.app.playerFromWeb = true
				f.app.playerTitle = webPlaybackTitle
				f.app.playerTracker = newPlaybackTracker("")
				resolvedTitle := "A resolved web video — Linux systems, explained"
				f.app.playerTracker.updateTitle(resolvedTitle)
				f.app.applyListingMode(ListingModeDateAdded)
				f.app.navigateRight()
				for _, view := range []string{"browse", "movie", "show"} {
					switch view {
					case "movie":
						f.app.pushMovieDetail(&MediaItem{ID: "movie-new", Title: "New Movie", Type: mediaKindMovie})
					case "show":
						f.app.pushShowDetail(&MediaItem{ID: "show-one", Title: "Fixture Show", Type: mediaKindShow})
					}
					if f.app.detail != nil {
						f.app.detail.enteredAt = time.Now().Add(-detailFadeDuration)
					}
					if err := f.driver.Resize(size.X, size.Y); err != nil {
						t.Fatal(err)
					}
					if !headlessLabelInside(f.driver.Tree(), "Now playing from Web UI", size) || !headlessLabelInside(f.driver.Tree(), resolvedTitle, size) {
						t.Fatal("web playing indicator missing or clipped")
					}
					tvCapture(t, f, fmt.Sprintf("web-%s-%d-art-%d", view, size.X, shade))
				}
				f.app.playerDone = make(chan playerResult, 1)
				f.app.playerDone <- playerResult{}
				if err := f.driver.Frame(); err != nil {
					t.Fatal(err)
				}
				if f.app.playerFromWeb || headlessHasLabel(f.driver.Tree(), "Now playing from Web UI") {
					t.Fatal("web playing indicator remained after completion")
				}
			})
		}
	}
}
