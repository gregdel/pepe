package app

import (
	"fmt"
	"image"
	"net"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gregdel/pepe/internal/driver"
)

func TestWebRemoteURLs(t *testing.T) {
	ips := []net.IP{
		net.ParseIP("127.0.0.1"), net.ParseIP("::1"), net.ParseIP("fe80::1"),
		net.ParseIP("169.254.1.2"), net.ParseIP("224.0.0.1"),
		net.ParseIP("fd00::2"), net.ParseIP("192.168.1.12"),
		net.ParseIP("10.0.0.4"), net.ParseIP("192.168.1.12"),
	}
	for _, tc := range []struct {
		name string
		ip   string
		want []string
	}{
		{"dual-stack wildcard", "::", []string{"http://10.0.0.4:43210/", "http://192.168.1.12:43210/", "http://[fd00::2]:43210/"}},
		{"IPv4 wildcard", "0.0.0.0", []string{"http://10.0.0.4:43210/", "http://192.168.1.12:43210/"}},
		{"specific IPv4", "192.168.1.12", []string{"http://192.168.1.12:43210/"}},
		{"specific IPv6", "fd00::2", []string{"http://[fd00::2]:43210/"}},
		{"loopback", "127.0.0.1", nil},
		{"IPv6 loopback", "::1", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := webRemoteURLs(&net.TCPAddr{IP: net.ParseIP(tc.ip), Port: 43210}, ips)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("URLs = %v, want %v", got, tc.want)
			}
		})
	}
	if got := webRemoteURLs(&net.TCPAddr{IP: net.IPv4zero, Port: 8081}, nil); len(got) != 0 {
		t.Fatalf("offline URLs = %v", got)
	}
}

func TestWebRemoteNavigationPreservesListing(t *testing.T) {
	a := newModeTestApp(nil, nil)
	a.applyListingMode(ListingModeDateAdded)
	a.browse.nav.resetToSection(SectionShows, a.sectionRoot(SectionShows))
	nav, snapshot := a.browse.nav, a.browse.library.showRadix
	a.browse.focus = browseFocusModeRail
	a.navigateDown()
	if a.browse.remote == nil || a.browse.library.mode != ListingModeDateAdded || a.browse.nav != nav || a.browse.library.showRadix != snapshot {
		t.Fatal("opening remote changed the catalog or navigation")
	}
	a.navigateRight()
	a.navigateRight()
	a.navigateRight()
	if a.browse.nav != nav || a.detail != nil {
		t.Fatal("QR page navigated the hidden catalog")
	}
	if a.currentVideo() != nil {
		t.Fatal("QR page exposes hidden catalog subtitle actions")
	}
	a.navigateLeft()
	if a.browse.focus != browseFocusModeRail || a.browse.remote == nil {
		t.Fatal("Left did not return to the remote's rail entry")
	}
	a.navigateEscape()
	if a.browse.remote != nil || a.browse.nav != nav || a.railOption() != railLastAdded {
		t.Fatal("Back did not restore the existing listing")
	}
	a.navigateUp()
	if a.railOption() != railSmartList {
		t.Fatal("Up from Last added did not select Smart list")
	}
	a.navigateUp()
	if a.browse.logs == nil || a.browse.remote != nil {
		t.Fatal("Up from Smart list did not wrap to Logs")
	}
	a.navigateUp()
	if a.browse.remote == nil || a.browse.logs != nil {
		t.Fatal("Up from Logs did not select Web remote")
	}
	a.navigateDown()
	a.navigateDown()
	if a.railOption() != railSmartList || a.browse.onPage() {
		t.Fatal("Down from Logs did not wrap to Smart list")
	}
	a.selectRailOption(railWebRemote)
	a.navigateHome()
	if a.browse.remote != nil || !a.browse.nav.isAtRoot() || a.browse.focus != browseFocusContent {
		t.Fatal("Home did not leave the QR page")
	}
}

func TestHeadlessWebRemoteListingClicks(t *testing.T) {
	for _, mode := range []ListingMode{ListingModeTitle, ListingModeDateAdded} {
		for _, section := range []string{"Home", "Movies", "Shows"} {
			t.Run(listingModeLabel(mode)+"/"+section, func(t *testing.T) {
				f := newHeadlessFixture(t)
				if section != "Home" {
					selected := SectionMovies
					if section == "Shows" {
						selected = SectionShows
					}
					f.app.browse.nav.resetToSection(selected, f.app.sectionRoot(selected))
				}
				wasAtRoot, selectedSection := f.app.browse.nav.isAtRoot(), f.app.browse.nav.section
				if err := f.driver.Frame(); err != nil {
					t.Fatal(err)
				}
				var remoteMessage string
				for _, label := range []string{"Web remote", listingModeLabel(mode)} {
					id, ok := headlessButtonID(f.driver.Tree(), label)
					if !ok {
						t.Fatalf("missing rail button %q", label)
					}
					headlessClick(t, f, driver.ClickTarget{ID: &id})
					if label == "Web remote" {
						if f.app.browse.remote == nil {
							t.Fatal("remote click did not open the remote page")
						}
						remoteMessage = f.app.browse.remote.message
						if !headlessHasLabel(f.driver.Tree(), remoteMessage) {
							t.Fatal("remote click did not render the remote content in the same frame")
						}
					} else if headlessHasLabel(f.driver.Tree(), remoteMessage) {
						t.Fatal("listing click still rendered remote content in the same frame")
					}
					if err := f.driver.Frame(); err != nil {
						t.Fatal(err)
					}
				}
				if f.app.browse.remote != nil || f.app.browse.library.mode != mode || f.app.browse.focus != browseFocusModeRail {
					t.Fatal("listing click did not leave the remote and select the mode rail")
				}
				if f.app.browse.nav.isAtRoot() != wasAtRoot || f.app.browse.nav.section != selectedSection {
					t.Fatal("listing click lost the selected browse section")
				}
				if !headlessHasLabel(f.driver.Tree(), section) || headlessHasLabel(f.driver.Tree(), "Scan to connect") {
					t.Fatal("listing click did not restore the selected browse section")
				}
			})
		}
	}
}

func TestHeadlessWebRemoteSetup(t *testing.T) {
	f := newHeadlessFixture(t)
	headlessKey(t, f, "LeftArrow")
	headlessKey(t, f, "UpArrow")
	headlessKey(t, f, "UpArrow") // past Logs
	if !headlessHasLabel(f.driver.Tree(), "Web remote") || !headlessHasLabel(f.driver.Tree(), f.app.browse.remote.message) {
		t.Fatal("disabled remote is missing its setup instructions")
	}
	if !headlessHasLabel(f.driver.Tree(), "Unavailable") || headlessHasLabel(f.driver.Tree(), "Scan to connect") {
		t.Fatal("disabled remote header still invites scanning")
	}
	if err := f.app.startWebUI("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.app.Close)
	f.app.selectRailOption(railWebRemote)
	if len(f.app.browse.remote.urls) != 0 || !strings.Contains(f.app.browse.remote.message, "instead of localhost") {
		t.Fatal("loopback listener was advertised to a phone")
	}
}

func TestWebRemoteBoundPort(t *testing.T) {
	a := New(nil, nil, nil, nil, nil)
	if err := a.startWebUI(":0"); err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	bound := a.webUI.listener.Addr().(*net.TCPAddr)
	urls := webRemoteURLs(bound, []net.IP{net.ParseIP("192.168.1.12")})
	if bound.Port == 0 || len(urls) != 1 || urls[0] != fmt.Sprintf("http://192.168.1.12:%d/", bound.Port) {
		t.Fatalf("actual listener %v produced URLs %v", bound, urls)
	}
}

func TestHeadlessWebRemoteQR(t *testing.T) {
	for _, size := range []image.Point{image.Pt(1920, 1080), image.Pt(1280, 720), image.Pt(800, 600)} {
		t.Run(size.String(), func(t *testing.T) {
			f := newHeadlessFixture(t)
			f.app.browse.remote = &webRemotePage{urls: []string{"http://192.168.1.12:8081/", "http://[fd00::2]:8081/"}}
			f.app.browse.remote.move(0)
			f.app.browse.focus = browseFocusModeRail
			if err := f.driver.Resize(size.X, size.Y); err != nil {
				t.Fatal(err)
			}
			for _, label := range []string{"Web remote QR code", "http://192.168.1.12:8081/", "Scan to connect"} {
				if !headlessLabelInside(f.driver.Tree(), label, size) {
					t.Errorf("missing visible label %q", label)
				}
			}
			for _, label := range []string{"Scan with your phone's camera", "Use the same trusted network · No authentication or TLS"} {
				if headlessHasLabel(f.driver.Tree(), label) {
					t.Errorf("removed text remains visible: %q", label)
				}
			}
			// Optional independent decoder checks the actual composited render,
			// not just the encoder's bitmap. Ordinary tests need no external tools.
			decoder, err := exec.LookPath("zbarimg")
			if err != nil {
				t.Log("zbarimg unavailable; rendered QR decoding not checked")
			}
			path := filepath.Join(t.TempDir(), "remote.png")
			if err := f.driver.Screenshot(path); err != nil {
				t.Fatal(err)
			}
			if decoder != "" {
				out, err := exec.Command(decoder, "--quiet", "--raw", path).Output()
				if err != nil || strings.TrimSpace(string(out)) != f.app.browse.remote.urls[0] {
					t.Fatalf("rendered QR decode = %q, %v", out, err)
				}
			}
			headlessKey(t, f, "RightArrow")
			headlessKey(t, f, "DownArrow")
			if f.app.browse.remote.cursor != 1 || !headlessHasLabel(f.driver.Tree(), "http://[fd00::2]:8081/") {
				t.Fatal("address selection did not update the QR page")
			}
			if err := f.driver.Screenshot(path); err != nil {
				t.Fatal(err)
			}
			if decoder != "" {
				out, err := exec.Command(decoder, "--quiet", "--raw", path).Output()
				if err != nil || strings.TrimSpace(string(out)) != f.app.browse.remote.urls[1] {
					t.Fatalf("changed QR decode = %q, %v", out, err)
				}
			}
		})
	}
}
