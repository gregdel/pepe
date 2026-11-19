package app

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"github.com/gregdel/pepe/internal/driver"
)

func TestInterfaceConfig(t *testing.T) {
	t.Setenv("POLOCHON_URL", "http://localhost")
	t.Setenv("POLOCHON_TOKEN", "token")
	for _, tc := range []struct {
		yaml                    string
		profile                 ControlProfile
		hidden, native, invalid bool
	}{
		{"", ControlsKeyboard, false, false, false},
		{"controls: hdmi-cec\nhelp_bar: false\n", ControlsHDMICEC, true, false, false},
		{"controls: keyboard\nhelp_bar: true\nmpv_osd: native\n", ControlsKeyboard, false, true, false},
		{"controls: gamepad\n", "", false, false, true},
		{"controls: ''\n", "", false, false, true},
		{"mpv_osd: unknown\n", "", false, false, true},
		{"help_bar: []\n", "", false, false, true},
	} {
		t.Run(tc.yaml, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(path)
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid interface config accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			a := New(nil, nil, cfg, nil, nil)
			if a.controls != tc.profile || a.hideHelp != tc.hidden || a.nativeOSD != tc.native {
				t.Fatalf("interface = %q, hidden=%v, native=%v", a.controls, a.hideHelp, a.nativeOSD)
			}
		})
	}
	for _, cfg := range []*Config{nil, {}, {MPVPath: "mpv"}} {
		a := New(nil, nil, cfg, nil, nil)
		if a.hideHelp || a.nativeOSD || a.controls == ControlsHDMICEC {
			t.Fatal("minimal config changed defaults")
		}
	}
}

func TestContextHintsMatchActions(t *testing.T) {
	f := newHeadlessFixture(t)
	a := f.app
	has := func(key, label string) bool {
		return slices.ContainsFunc(a.currentHints(), func(h hint) bool { return h.key == key && h.label == label })
	}
	if !has("→/enter", "browse") {
		t.Fatal("home browse hint missing")
	}
	a.pushShowDetail(&MediaItem{ID: "show-one", Title: "Show", Type: mediaKindShow})
	if !has("↓/enter", "episodes") || has("enter", "play episode") {
		t.Fatal("tabs advertise immediate playback")
	}
	a.detail.show.down()
	if !has("enter", "play episode") {
		t.Fatal("episode playback hint missing")
	}
	for _, state := range []SubtitlePopupState{SubtitlePopupLoading, SubtitlePopupReady, SubtitlePopupDownloading, SubtitlePopupDone, SubtitlePopupError} {
		a.subPopup = &SubtitlePopup{state: state}
		if has("enter", "download") {
			t.Fatalf("empty/busy popup %v advertises download", state)
		}
		if has("enter", "close") != (state == SubtitlePopupDone || state == SubtitlePopupError) {
			t.Fatal("popup close hint is incorrect")
		}
	}
	a.subPopup = &SubtitlePopup{state: SubtitlePopupReady, entries: subtitleEntries(2)}
	if !has("enter", "download") || !has("↑↓", "navigate") {
		t.Fatal("ready popup hints missing")
	}
	a.controls = ControlsHDMICEC
	if !has("OK", "download") || !has("Back", "close") {
		t.Fatal("popup keyboard names leaked")
	}
	a.subPopup = nil
	if !has("Blue", "subtitles") || !has("Red", "home") {
		t.Fatal("detail remote colours missing")
	}
	a.popDetail()
	if !has("Green", "refresh") || !has("Yellow", "movies/shows") {
		t.Fatal("browse remote colours missing")
	}
	a.hideHelp = true
	if len(a.currentHints()) != 0 {
		t.Fatal("hidden help still advertises actions")
	}
}

func TestRemoteHintsGroupedInButtonOrder(t *testing.T) {
	f := newHeadlessFixture(t)
	a := f.app
	a.controls = ControlsHDMICEC
	check := func(want []string) {
		t.Helper()
		var got []string
		for _, h := range a.currentHints() {
			if h.color.A != 0 {
				got = append(got, h.key)
			} else if len(got) > 0 {
				t.Fatal("navigation hint split the colour group")
			}
		}
		if !slices.Equal(got, want) {
			t.Fatalf("remote button order = %v, want %v", got, want)
		}
	}
	check([]string{"Green", "Yellow"})
	headlessKey(t, f, "RightArrow")
	check([]string{"Red", "Green", "Yellow", "Blue"})
	a.pushMovieDetail(&MediaItem{ID: "movie-new", Title: "New Movie", Type: mediaKindMovie})
	check([]string{"Red", "Blue"})
	a.subPopup = &SubtitlePopup{state: SubtitlePopupReady, entries: subtitleEntries(2)}
	check(nil)
}

func TestHelpBarLayoutAndVisibility(t *testing.T) {
	for _, size := range []image.Point{image.Pt(1920, 1080), image.Pt(1280, 720), image.Pt(800, 600)} {
		for _, profile := range []ControlProfile{ControlsKeyboard, ControlsHDMICEC} {
			t.Run(fmt.Sprintf("%dx%d/%s", size.X, size.Y, profile), func(t *testing.T) {
				f := newHeadlessFixture(t)
				f.app.controls = profile
				headlessKey(t, f, "RightArrow")
				if err := f.driver.Resize(size.X, size.Y); err != nil {
					t.Fatal(err)
				}
				tvCapture(t, f, fmt.Sprintf("help-%s-%d", profile, size.X))
				checkHelpBounds(t, f.driver.Tree(), size)
				if profile == ControlsHDMICEC {
					checkRemoteBars(t, f.driver.Tree())
				}
				f.app.hideHelp = true
				if err := f.driver.Frame(); err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"navigate", "refresh", "Blue", "Green", "Yellow"} {
					if headlessHasLabel(f.driver.Tree(), name) {
						t.Fatalf("hidden hint %q remains", name)
					}
				}
				tvCapture(t, f, fmt.Sprintf("help-hidden-%s-%d", profile, size.X))
				// Controls still work when their instructions are hidden.
				headlessKey(t, f, "Tab")
				if f.app.browse.nav.section != SectionShows {
					t.Fatal("hiding help disabled controls")
				}
				f.app.subPopup = &SubtitlePopup{video: f.app.client.movies["movie-new"], state: SubtitlePopupReady, entries: subtitleEntries(2), list: layout.List{Axis: layout.Vertical}}
				if err := f.driver.Frame(); err != nil {
					t.Fatal(err)
				}
				if headlessHasLabel(f.driver.Tree(), "download") {
					t.Fatal("hidden popup hints remain")
				}
				f.app.hideHelp = false
				if err := f.driver.Frame(); err != nil {
					t.Fatal(err)
				}
				count := countHelpLabel(f.driver.Tree(), "download")
				if count != 1 {
					t.Fatalf("popup help rendered %d times", count)
				}
				checkHelpBounds(t, f.driver.Tree(), size)
				tvCapture(t, f, fmt.Sprintf("help-popup-%s-%d", profile, size.X))
			})
		}
	}
}

func checkRemoteBars(t *testing.T, nodes []driver.Node) {
	t.Helper()
	bars := make(map[string]driver.Node)
	var visit func([]driver.Node)
	visit = func(nodes []driver.Node) {
		for _, node := range nodes {
			if slices.Contains([]string{"Red", "Green", "Yellow", "Blue"}, node.Label) {
				bars[node.Label] = node
			}
			visit(node.Children)
		}
	}
	visit(nodes)
	previousX, previousY := -1, -1
	for _, name := range []string{"Red", "Green", "Yellow", "Blue"} {
		node, ok := bars[name]
		if !ok {
			t.Fatalf("missing accessible %s bar", name)
		}
		b := node.Bounds
		if b.MaxX-b.MinX < 3*(b.MaxY-b.MinY) || b.MaxY <= b.MinY {
			t.Fatalf("%s is not a horizontal bar: %+v", name, b)
		}
		if previousX >= b.MinX || (previousY >= 0 && previousY != b.MinY) {
			t.Fatalf("%s is not in the grouped colour row: %+v", name, b)
		}
		previousX, previousY = b.MinX, b.MinY
	}
}

func countHelpLabel(nodes []driver.Node, label string) int {
	count := 0
	for _, node := range nodes {
		if node.Label == label {
			count++
		}
		count += countHelpLabel(node.Children, label)
	}
	return count
}

func checkHelpBounds(t *testing.T, nodes []driver.Node, size image.Point) {
	t.Helper()
	for _, node := range nodes {
		if slices.Contains([]string{"navigate", "refresh", "download", "Blue", "Green", "Yellow", "Red"}, node.Label) {
			b := node.Bounds
			if b.MinX < 0 || b.MaxX > size.X || b.MinY < size.Y/2 || b.MaxY > size.Y {
				t.Fatalf("hint %q out of bounds: %+v", node.Label, b)
			}
		}
		checkHelpBounds(t, node.Children, size)
	}
}

func TestKeycapsShareHeightAndBaseline(t *testing.T) {
	a := &App{th: newAppTheme()}
	var ops op.Ops
	gtx := layout.Context{Ops: &ops, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Constraints: layout.Constraints{Max: image.Pt(1920, 1080)}}
	want := a.keycap(gtx, "esc")
	// Arrows use the symbol fallback font, whose line box is taller than Roboto's.
	for _, key := range []string{"↑↓", "←", "→/enter", "home"} {
		got := a.keycap(gtx, key)
		if got.Size.Y != want.Size.Y || got.Baseline != want.Baseline {
			t.Errorf("keycap %q height/baseline = %d/%d, want %d/%d like esc", key, got.Size.Y, got.Baseline, want.Size.Y, want.Baseline)
		}
	}
}

func TestHelpDetailsArtwork(t *testing.T) {
	for _, width := range []int{1920, 1280} {
		for _, shade := range []uint8{16, 255} {
			for _, profile := range []ControlProfile{ControlsKeyboard, ControlsHDMICEC} {
				t.Run(fmt.Sprintf("%d/%d/%s", width, shade, profile), func(t *testing.T) {
					f := newHeadlessFixture(t)
					f.app.controls = profile
					art := image.NewNRGBA(image.Rect(0, 0, 32, 32))
					for y := range 32 {
						for x := range 32 {
							art.SetNRGBA(x, y, color.NRGBA{R: shade, G: shade, B: shade, A: 255})
						}
					}
					for _, key := range []string{"fanart:movie-new", "thumb:movie-new", "fanart:show-one", "poster:show-one"} {
						f.app.images.store(key, paint.NewImageOp(art))
					}
					f.app.pushMovieDetail(&MediaItem{ID: "movie-new", Title: "New Movie", Type: mediaKindMovie})
					if err := f.driver.Resize(width, width*9/16); err != nil {
						t.Fatal(err)
					}
					tvCapture(t, f, fmt.Sprintf("help-movie-%s-%d-%d", profile, width, shade))
					f.app.pushShowDetail(&MediaItem{ID: "show-one", Title: "Fixture Show", Type: mediaKindShow})
					if err := f.driver.Refresh(); err != nil {
						t.Fatal(err)
					}
					tvCapture(t, f, fmt.Sprintf("help-show-tabs-%s-%d-%d", profile, width, shade))
					headlessKey(t, f, "DownArrow")
					tvCapture(t, f, fmt.Sprintf("help-show-episodes-%s-%d-%d", profile, width, shade))
				})
			}
		}
	}
}

func TestCECKeymapSharesKeyboardBindings(t *testing.T) {
	data, err := os.ReadFile("../../packaging/linux/cec.toml")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{
		`0x30 = "KEY_F3"`, `0x31 = "KEY_F4"`,
		`0x71 = "KEY_S"`, `0x72 = "KEY_HOME"`, `0x73 = "KEY_R"`, `0x74 = "KEY_TAB"`,
	} {
		if !strings.Contains(string(data), entry) {
			t.Fatalf("missing CEC mapping %s", entry)
		}
	}
	for _, code := range []string{"0x40", "0x41", "0x42", "0x43"} {
		if strings.Contains(string(data), code+" =") {
			t.Fatalf("TV power/volume/mute code %s must remain unmapped", code)
		}
	}
}

func TestRemoteHintContrast(t *testing.T) {
	for _, col := range []color.NRGBA{colorRemoteBlue, colorRemoteRed, colorRemoteGreen, colorRemoteYellow} {
		if got := contrastRatio(col, colorSurface2); got < 4.5 {
			t.Fatalf("remote bar %v contrast = %.2f:1", col, got)
		}
	}
}
