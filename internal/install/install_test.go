package install

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gregdel/pepe/packaging"
)

const fakeExecutable = "fake executable v1\n"

func newTestInstaller(t *testing.T) (installer, string) {
	t.Helper()
	base := t.TempDir()
	home := filepath.Join(base, "home with spaces")
	data := filepath.Join(base, "data")
	config := filepath.Join(base, "config")
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("PATH", "") // No player, desktop utilities or network commands.
	i := installer{paths: paths{
		root: filepath.Join(data, "pepe"), bin: filepath.Join(home, ".local", "bin", "pepe"),
		desktop: filepath.Join(data, "applications", "pepe.desktop"), config: filepath.Join(config, "pepe", "config.yaml"),
	}, out: io.Discard}
	source := filepath.Join(base, "download", "pepe")
	writeTestFile(t, source, fakeExecutable, 0o755)
	return i, source
}

func writeTestFile(t *testing.T, path, content string, mode fs.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func checkTestFile(t *testing.T, path, content string, mode fs.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != mode {
		t.Fatalf("%s: mode %v, want regular %o", path, info.Mode(), mode)
	}
	if readTestFile(t, path) != content {
		t.Fatalf("%s: contents changed", path)
	}
}

func checkAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("%s: want absent, got %v", path, err)
	}
}

func testLink(t *testing.T, path string) string {
	t.Helper()
	target, err := os.Readlink(path)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func makeTestLink(t *testing.T, target, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

func installTestSource(t *testing.T, i installer, source string) string {
	t.Helper()
	if err := i.install(source); err != nil {
		t.Fatal(err)
	}
	return i.paths.root
}

func TestInstallLifecycle(t *testing.T) {
	i, source := newTestInstaller(t)
	p := i.paths
	current := installTestSource(t, i, source)
	installTestSource(t, i, source)
	if got := testLink(t, p.bin); got != filepath.Join(p.root, "pepe") {
		t.Fatalf("launcher target = %q", got)
	}
	checkTestFile(t, filepath.Join(p.root, ".pepe-managed"), ownership, 0o644)
	config := readTestFile(t, p.config)
	if !strings.Contains(config, "mpv_path: '"+filepath.Join(p.root, "mpv-auto.sh")+"'\n") {
		t.Fatal("fresh configuration does not select installed mpv-auto.sh")
	}
	checkTestFile(t, p.config, config, 0o600)
	desktop := readTestFile(t, p.desktop)
	for _, want := range []string{"X-Pepe-Bundle=true\n", "Exec=\"" + p.bin + "\"\n", "Icon=" + filepath.Join(p.root, "assets", "application_icon_1024.png") + "\n"} {
		if !strings.Contains(desktop, want) {
			t.Errorf("desktop entry missing %q", want)
		}
	}

	const upgraded = "different fake executable v2\n"
	writeTestFile(t, source, upgraded, 0o755)
	installTestSource(t, i, source)
	if err := os.RemoveAll(filepath.Dir(source)); err != nil {
		t.Fatal(err)
	}
	if got := readTestFile(t, p.bin); got != upgraded {
		t.Fatal("installed launcher depends on removed source")
	}
	checkTestFile(t, filepath.Join(current, "pepe"), upgraded, 0o755)
	// Compare all embedded payloads, including artwork and all three wrappers.
	// They must be copies, not links to the download or checkout. Never run them.
	if err := fs.WalkDir(packaging.Files, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := packaging.Files.ReadFile(name)
		if err != nil {
			return err
		}
		dest := strings.TrimPrefix(strings.Replace(name, "embed/", "assets/", 1), "linux/")
		mode := fs.FileMode(0o644)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		checkTestFile(t, filepath.Join(current, dest), string(data), mode)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mpv-auto.sh", "mpv-inhibit.sh", "mpv-keepawake.sh"} {
		if _, err := os.Stat(filepath.Join(current, name)); err != nil {
			t.Fatal(err)
		}
	}
	checkTestFile(t, p.config, config, 0o600)
	info, err := os.Lstat(current)
	if err != nil || !info.IsDir() {
		t.Fatalf("want a real installation directory: %v, %v", info, err)
	}
	for _, name := range []string{"app", "current", "releases", "RELEASE"} {
		checkAbsent(t, filepath.Join(p.root, name))
	}
	entries, err := os.ReadDir(p.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".install-") {
			t.Fatalf("staging directory left behind: %s", entry.Name())
		}
	}
	checkAbsent(t, p.root+".lock")
	for range 2 { // Uninstall also succeeds after the managed tree is gone.
		if err := i.uninstall(); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{p.root, p.bin, p.desktop} {
		checkAbsent(t, path)
	}
	checkTestFile(t, p.config, config, 0o600)
}

func TestInstallPreservesConfig(t *testing.T) {
	for _, kind := range []string{"regular", "symlink", "dangling symlink"} {
		for _, shared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/shared=%t", kind, shared), func(t *testing.T) {
				i, source := newTestInstaller(t)
				if shared {
					i.paths.config = filepath.Join(i.paths.root, "config.yaml")
				}
				const content = "polochon_token: private\nmpv_path: /custom/player\n"
				target := i.paths.config
				if kind != "regular" {
					target = filepath.Join(filepath.Dir(source), "user-config")
					makeTestLink(t, target, i.paths.config)
				}
				if kind != "dangling symlink" {
					writeTestFile(t, target, content, 0o640)
				}
				var output bytes.Buffer
				i.out = &output
				for range 2 {
					output.Reset()
					installTestSource(t, i, source)
					if want := "Configuration already exists; keeping " + i.paths.config + "\n"; !strings.Contains(output.String(), want) {
						t.Fatalf("missing config preservation notice: %q", output.String())
					}
				}
				if err := i.uninstall(); err != nil {
					t.Fatal(err)
				}
				if kind != "regular" && testLink(t, i.paths.config) != target {
					t.Fatal("config symlink changed")
				}
				if kind == "dangling symlink" {
					checkAbsent(t, target)
				} else {
					checkTestFile(t, target, content, 0o640)
				}
			})
		}
	}
}

func TestInstallWithReadOnlyConfigDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory write permissions")
	}
	i, source := newTestInstaller(t)
	const content = "polochon_token: private\nmpv_path: /custom/player\n"
	writeTestFile(t, i.paths.config, content, 0o640)
	dir := filepath.Dir(i.paths.config)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Error(err)
		}
	})
	installTestSource(t, i, source)
	const upgraded = "different fake executable v2\n"
	writeTestFile(t, source, upgraded, 0o755)
	installTestSource(t, i, source)
	checkTestFile(t, filepath.Join(i.paths.root, "pepe"), upgraded, 0o755)
	if err := i.uninstall(); err != nil {
		t.Fatal(err)
	}
	checkTestFile(t, i.paths.config, content, 0o640)
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o500 {
		t.Fatalf("config directory permissions changed: %v", info.Mode())
	}
}

func TestOwnershipConflicts(t *testing.T) {
	for _, kind := range []string{"unmarked root", "foreign marker", "symlink marker", "launcher file", "launcher symlink", "desktop file", "desktop symlink"} {
		t.Run(kind, func(t *testing.T) {
			i, source := newTestInstaller(t)
			installTestSource(t, i, source)
			p := i.paths
			path, content := "", "foreign contents\n"
			switch kind {
			case "unmarked root", "foreign marker", "symlink marker":
				path = filepath.Join(p.root, ".pepe-managed")
			case "launcher file", "launcher symlink":
				path = p.bin
			case "desktop file", "desktop symlink":
				path = p.desktop
			}
			if err := os.RemoveAll(path); err != nil {
				t.Fatal(err)
			}
			link := strings.Contains(kind, "symlink")
			if link {
				content = filepath.Join(filepath.Dir(source), "foreign")
				writeTestFile(t, content, ownership, 0o644)
				makeTestLink(t, content, path)
			} else if kind != "unmarked root" {
				writeTestFile(t, path, content, 0o644)
			}
			if err := i.install(source); err == nil {
				t.Fatal("install accepted ownership conflict")
			}
			if err := i.uninstall(); err == nil {
				t.Fatal("uninstall accepted ownership conflict")
			}
			if link {
				if testLink(t, path) != content {
					t.Fatal("foreign link changed")
				}
				checkTestFile(t, content, ownership, 0o644)
			} else if kind == "unmarked root" {
				checkAbsent(t, path)
			} else {
				checkTestFile(t, path, content, 0o644)
			}
			checkAbsent(t, p.root+".lock")
		})
	}
}

func TestCheckoutDesktopEntryRefused(t *testing.T) {
	for _, installed := range []bool{false, true} {
		t.Run(fmt.Sprintf("installed=%t", installed), func(t *testing.T) {
			i, source := newTestInstaller(t)
			if installed {
				installTestSource(t, i, source)
			}
			const checkoutEntry = "[Desktop Entry]\nName=pépé\nX-Pepe-Managed=true\nExec=/old/checkout/pepe\n"
			writeTestFile(t, i.paths.desktop, checkoutEntry, 0o644)
			for _, err := range []error{i.install(source), i.uninstall()} {
				if err == nil || !strings.Contains(err.Error(), "unregister the checkout first") {
					t.Fatalf("want checkout entry refusal with guidance, got %v", err)
				}
			}
			checkTestFile(t, i.paths.desktop, checkoutEntry, 0o644)
			if installed {
				checkTestFile(t, filepath.Join(i.paths.root, "pepe"), fakeExecutable, 0o755)
				if got := testLink(t, i.paths.bin); got != filepath.Join(i.paths.root, "pepe") {
					t.Fatalf("installed launcher changed: %s", got)
				}
			} else {
				checkAbsent(t, i.paths.root)
				checkAbsent(t, i.paths.bin)
			}
			checkAbsent(t, i.paths.root+".lock")
		})
	}
}

func TestSymlinkParentsRefused(t *testing.T) {
	for _, kind := range []string{"root", "bin", "desktop", "config"} {
		t.Run(kind, func(t *testing.T) {
			i, source := newTestInstaller(t)
			installTestSource(t, i, source)
			p := i.paths
			path := map[string]string{"root": p.root, "bin": filepath.Dir(p.bin), "desktop": filepath.Dir(p.desktop), "config": filepath.Dir(p.config)}[kind]
			outside := filepath.Join(filepath.Dir(source), "outside")
			if err := os.Rename(path, outside); err != nil {
				t.Fatal(err)
			}
			makeTestLink(t, outside, path)
			sentinel := filepath.Join(outside, "sentinel")
			writeTestFile(t, sentinel, "untouched", 0o600)
			if err := i.install(source); err == nil {
				t.Fatal("install traversed symlink parent")
			}
			if err := i.uninstall(); err == nil {
				t.Fatal("uninstall traversed symlink parent")
			}
			checkTestFile(t, sentinel, "untouched", 0o600)
			if readTestFile(t, p.bin) != fakeExecutable {
				t.Fatal("current executable changed")
			}
			if testLink(t, path) != outside {
				t.Fatal("parent link changed")
			}
		})
	}
}

func TestForeignLockRemains(t *testing.T) {
	for _, kind := range []string{"empty", "occupied"} {
		t.Run(kind, func(t *testing.T) {
			i, source := newTestInstaller(t)
			installTestSource(t, i, source)
			occupied := strings.HasSuffix(kind, "occupied")
			lock := i.paths.root + ".lock"
			if err := os.Mkdir(lock, 0o700); err != nil {
				t.Fatal(err)
			}
			if occupied {
				writeTestFile(t, filepath.Join(lock, "owner"), "another installer", 0o600)
			}
			if err := i.install(source); err == nil {
				t.Fatal("install acquired foreign lock")
			}
			if err := i.uninstall(); err == nil {
				t.Fatal("uninstall acquired foreign lock")
			}
			if info, err := os.Lstat(lock); err != nil || !info.IsDir() {
				t.Fatalf("foreign lock removed or replaced: %v", err)
			}
			if occupied {
				checkTestFile(t, filepath.Join(lock, "owner"), "another installer", 0o600)
			}
			if readTestFile(t, i.paths.bin) != fakeExecutable {
				t.Fatal("foreign lock failure changed installed executable")
			}
		})
	}
}

func TestUninstallHostileManifest(t *testing.T) {
	for _, kind := range []string{"absolute", "traversal", "dot component", "empty component", "self", "ownership marker", "duplicate", "invalid hash", "manifest symlink", "file symlink", "directory symlink", "unknown symlink"} {
		t.Run(kind, func(t *testing.T) {
			i, source := newTestInstaller(t)
			release := installTestSource(t, i, source)
			outside := filepath.Join(filepath.Dir(source), "outside")
			sentinel := filepath.Join(outside, "sentinel")
			writeTestFile(t, sentinel, "must survive", 0o600)
			sums := filepath.Join(release, "SHA256SUMS")
			digest := fmt.Sprintf("%x", sha256.Sum256([]byte("must survive")))
			name := "sentinel"
			switch kind {
			case "absolute":
				name = sentinel
			case "traversal":
				relative, err := filepath.Rel(release, sentinel)
				if err != nil {
					t.Fatal(err)
				}
				name = filepath.ToSlash(relative)
			case "dot component":
				name = "assets/./sentinel"
			case "empty component":
				name = "assets//sentinel"
			case "self":
				name = "SHA256SUMS"
			case "ownership marker":
				name = ".pepe-managed"
			case "invalid hash":
				digest = "not-a-sha256"
			case "manifest symlink":
				if err := os.Remove(sums); err != nil {
					t.Fatal(err)
				}
				makeTestLink(t, sentinel, sums)
			case "file symlink":
				makeTestLink(t, sentinel, filepath.Join(release, name))
			case "directory symlink":
				name = "linked/sentinel"
				makeTestLink(t, outside, filepath.Join(release, "linked"))
			case "unknown symlink":
				makeTestLink(t, outside, filepath.Join(i.paths.root, "user-link"))
			}
			if kind != "manifest symlink" && kind != "unknown symlink" {
				line := digest + "  " + name + "\n"
				if kind == "duplicate" {
					line += line
				}
				writeTestFile(t, sums, line, 0o644)
			}
			if err := i.uninstall(); err != nil {
				t.Fatal(err)
			}
			checkTestFile(t, sentinel, "must survive", 0o600)
			if kind == "unknown symlink" {
				if testLink(t, filepath.Join(i.paths.root, "user-link")) != outside {
					t.Fatal("unknown symlink removed")
				}
			} else {
				checkTestFile(t, filepath.Join(release, "pepe"), fakeExecutable, 0o755)
			}
		})
	}
}

func TestUninstallPreservesModifiedAndUnknownFiles(t *testing.T) {
	i, source := newTestInstaller(t)
	release := installTestSource(t, i, source)
	modified := filepath.Join(release, "mpv-keepawake.sh")
	unknown := filepath.Join(release, "notes", "mine.txt")
	writeTestFile(t, modified, "local wrapper changes", 0o700)
	writeTestFile(t, unknown, "user notes", 0o640)
	for range 2 {
		if err := i.uninstall(); err != nil {
			t.Fatal(err)
		}
	}
	checkTestFile(t, modified, "local wrapper changes", 0o700)
	checkTestFile(t, unknown, "user notes", 0o640)
	checkAbsent(t, filepath.Join(release, "pepe"))
	checkAbsent(t, i.paths.bin)
	checkAbsent(t, i.paths.desktop)
	checkTestFile(t, filepath.Join(i.paths.root, ".pepe-managed"), ownership, 0o644)
}

func TestInstallOverwritesManagedFiles(t *testing.T) {
	i, source := newTestInstaller(t)
	current := installTestSource(t, i, source)
	writeTestFile(t, filepath.Join(current, "pepe"), "modified executable", 0o600)
	writeTestFile(t, filepath.Join(current, "assets", "logo_1280x720.png"), "modified artwork", 0o600)
	writeTestFile(t, filepath.Join(current, "mpv-auto.sh"), "modified wrapper", 0o644)
	if err := os.Remove(filepath.Join(current, "assets", "cover_600x900.jpg")); err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(current, "notes", "mine.txt")
	writeTestFile(t, unknown, "user notes", 0o640)
	installTestSource(t, i, source)
	m, err := readManifest(current)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyPayload(current, m); err != nil {
		t.Fatal(err)
	}
	checkTestFile(t, unknown, "user notes", 0o640)
	checkAbsent(t, filepath.Join(i.paths.root, "releases"))
}

func TestInvalidUpgradeKeepsInstalledFiles(t *testing.T) {
	for _, kind := range []string{"missing source", "directory source", "corrupt manifest", "manifest symlink", "file symlink", "directory symlink", "unknown payload conflict"} {
		t.Run(kind, func(t *testing.T) {
			i, source := newTestInstaller(t)
			current := installTestSource(t, i, source)
			writeTestFile(t, source, "fake executable v2\n", 0o755)
			switch kind {
			case "missing source":
				source += ".missing"
			case "directory source":
				source = filepath.Dir(source)
			case "corrupt manifest":
				writeTestFile(t, filepath.Join(current, "SHA256SUMS"), "invalid\n", 0o644)
			case "manifest symlink", "file symlink", "directory symlink":
				name := map[string]string{"manifest symlink": "SHA256SUMS", "file symlink": "mpv-auto.sh", "directory symlink": "assets"}[kind]
				path := filepath.Join(current, name)
				moved := filepath.Join(filepath.Dir(source), "moved-payload")
				if err := os.Rename(path, moved); err != nil {
					t.Fatal(err)
				}
				makeTestLink(t, moved, path)
			case "unknown payload conflict":
				m, err := readManifest(current)
				if err != nil {
					t.Fatal(err)
				}
				delete(m, "assets/logo_1280x720.png")
				writeTestFile(t, filepath.Join(current, "SHA256SUMS"), string(m.bytes()), 0o644)
			}
			desktop, config := readTestFile(t, i.paths.desktop), readTestFile(t, i.paths.config)
			if err := i.install(source); err == nil {
				t.Fatal("upgrade unexpectedly succeeded")
			}
			checkTestFile(t, filepath.Join(current, "pepe"), fakeExecutable, 0o755)
			checkTestFile(t, i.paths.desktop, desktop, 0o644)
			checkTestFile(t, i.paths.config, config, 0o600)
			checkAbsent(t, i.paths.root+".lock")
			entries, err := os.ReadDir(i.paths.root)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".install-") {
					t.Fatalf("failed upgrade left staging files: %s", entry.Name())
				}
			}
		})
	}
}

func TestInstallPreservesExistingData(t *testing.T) {
	for _, kind := range []string{"empty", "source icon", "unknown symlink"} {
		t.Run(kind, func(t *testing.T) {
			i, source := newTestInstaller(t)
			if err := os.MkdirAll(i.paths.root, 0o755); err != nil {
				t.Fatal(err)
			}
			icon := filepath.Join(i.paths.root, "icon.png")
			outside := filepath.Join(filepath.Dir(source), "user-file")
			if kind == "source icon" {
				writeTestFile(t, icon, "source artwork", 0o640)
			} else if kind == "unknown symlink" {
				writeTestFile(t, outside, "user data", 0o640)
				makeTestLink(t, outside, icon)
			}
			if err := i.uninstall(); err != nil {
				t.Fatal(err)
			}
			installTestSource(t, i, source)
			if err := i.uninstall(); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "empty":
				checkAbsent(t, i.paths.root)
			case "source icon":
				checkTestFile(t, icon, "source artwork", 0o640)
			case "unknown symlink":
				if got := testLink(t, icon); got != outside {
					t.Fatalf("unknown symlink changed: %s", got)
				}
				checkTestFile(t, outside, "user data", 0o640)
			}
		})
	}
}

func TestInstallRetriesInterruptedCopy(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory write permissions")
	}
	for _, installed := range []bool{false, true} {
		t.Run(fmt.Sprintf("installed=%t", installed), func(t *testing.T) {
			i, source := newTestInstaller(t)
			if installed {
				installTestSource(t, i, source)
			}
			const upgraded = "fake executable v2\n"
			writeTestFile(t, source, upgraded, 0o755)
			assets := filepath.Join(i.paths.root, "assets")
			if err := os.MkdirAll(assets, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(assets, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(assets, 0o755) })
			if err := i.install(source); !os.IsPermission(err) {
				t.Fatalf("want copy permission error, got %v", err)
			}
			if err := os.Chmod(assets, 0o755); err != nil {
				t.Fatal(err)
			}
			installTestSource(t, i, source)
			checkTestFile(t, filepath.Join(i.paths.root, "pepe"), upgraded, 0o755)
		})
	}
}

func TestUninstallNeverRemovesConfigListedInManifest(t *testing.T) {
	i, source := newTestInstaller(t)
	i.paths.config = filepath.Join(i.paths.root, "config.yaml")
	const content = "mpv_path: /custom/player\n"
	writeTestFile(t, i.paths.config, content, 0o640)
	installTestSource(t, i, source)
	m, err := readManifest(i.paths.root)
	if err != nil {
		t.Fatal(err)
	}
	m["config.yaml"] = fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	writeTestFile(t, filepath.Join(i.paths.root, "SHA256SUMS"), string(m.bytes()), 0o644)
	if err := i.uninstall(); err != nil {
		t.Fatal(err)
	}
	checkTestFile(t, i.paths.config, content, 0o640)
	checkAbsent(t, filepath.Join(i.paths.root, "pepe"))
}

func TestOldLayoutRefused(t *testing.T) {
	for _, release := range []bool{false, true} {
		t.Run(fmt.Sprintf("release=%t", release), func(t *testing.T) {
			i, source := newTestInstaller(t)
			p := i.paths
			app := filepath.Join(p.root, "app")
			writeTestFile(t, filepath.Join(app, ".pepe-managed"), ownership, 0o644)
			dir := filepath.Join(app, "current")
			if release {
				dir = filepath.Join(app, "releases", "pepe-old-linux-amd64")
				makeTestLink(t, "releases/pepe-old-linux-amd64", filepath.Join(app, "current"))
			}
			writeTestFile(t, filepath.Join(dir, "pepe"), "old binary", 0o755)
			makeTestLink(t, filepath.Join(app, "current", "pepe"), p.bin)
			writeTestFile(t, p.desktop, "[Desktop Entry]\nX-Pepe-Bundle=true\n", 0o644)
			writeTestFile(t, p.config, "mpv_path: /old/player\n", 0o640)
			for _, err := range []error{i.install(source), i.uninstall()} {
				if err == nil || !strings.Contains(err.Error(), "original installer") {
					t.Fatalf("want old-layout refusal with uninstall instructions, got %v", err)
				}
			}
			checkTestFile(t, filepath.Join(dir, "pepe"), "old binary", 0o755)
			checkTestFile(t, p.config, "mpv_path: /old/player\n", 0o640)
			if got := testLink(t, p.bin); got != filepath.Join(app, "current", "pepe") {
				t.Fatalf("old launcher changed: %s", got)
			}
			checkAbsent(t, filepath.Join(p.root, ".pepe-managed"))
			checkAbsent(t, p.root+".lock")
		})
	}
}

func TestEnvironmentPaths(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	for _, tc := range []struct {
		name, home, data, config string
		wantErr                  bool
	}{
		{name: "defaults", home: home},
		{name: "absolute XDG", home: home, data: filepath.Join(base, "data"), config: filepath.Join(base, "config")},
		{name: "relative data ignored", home: home, data: "relative"},
		{name: "relative config rejected", home: home, config: "relative", wantErr: true},
		{name: "missing home", wantErr: true},
		{name: "relative home", home: "relative", wantErr: true},
		{name: "control character", home: home, data: filepath.Join(base, "bad\npath"), wantErr: true},
		{name: "percent launcher", home: home + "%", wantErr: true},
		{name: "equals launcher", home: home + "=", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", tc.home)
			t.Setenv("XDG_DATA_HOME", tc.data)
			t.Setenv("XDG_CONFIG_HOME", tc.config)
			got, err := environmentPaths()
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected invalid environment error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			data, config := tc.data, tc.config
			if !filepath.IsAbs(data) {
				data = filepath.Join(tc.home, ".local", "share")
			}
			if config == "" {
				config = filepath.Join(tc.home, ".config")
			}
			want := paths{root: filepath.Join(data, "pepe"), bin: filepath.Join(tc.home, ".local", "bin", "pepe"), desktop: filepath.Join(data, "applications", "pepe.desktop"), config: filepath.Join(config, "pepe", "config.yaml")}
			if got != want {
				t.Fatalf("paths = %+v, want %+v", got, want)
			}
		})
	}
}

func TestRunHelpAndInvalidArgs(t *testing.T) {
	t.Setenv("HOME", "") // These paths must not reach environment/config lookup.
	t.Setenv("XDG_CONFIG_HOME", "invalid-relative-path")
	t.Setenv("PATH", "")
	for _, command := range []string{"install", "uninstall"} {
		for _, arg := range []string{"-h", "--help", "--unknown", "extra", "--"} {
			t.Run(command+"/"+arg, func(t *testing.T) {
				args := []string{command, arg}
				if arg == "--" {
					args = append(args, "extra")
				}
				var stdout, stderr bytes.Buffer
				handled, status := Run(args, &stdout, &stderr)
				want := 2
				if arg == "-h" || arg == "--help" {
					want = 0
				}
				if !handled || status != want || stdout.Len() != 0 || !strings.Contains(stderr.String(), "Usage: pepe "+command) {
					t.Fatalf("Run(%q) = %t, %d; stdout=%q stderr=%q", args, handled, status, stdout.String(), stderr.String())
				}
			})
		}
	}
	for _, args := range [][]string{nil, {"-h"}, {"-config", "missing.yaml"}, {"unknown"}} {
		var stdout, stderr bytes.Buffer
		if handled, status := Run(args, &stdout, &stderr); handled || status != 0 || stdout.Len()+stderr.Len() != 0 {
			t.Fatalf("ordinary app arguments %q were handled", args)
		}
	}
}
