package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gregdel/pepe/internal/driver"
	"github.com/gregdel/pepe/packaging"
)

func TestWriteConfigTemplate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new", "pepe", "config.yaml")
	created, err := WriteConfigTemplate(path)
	if err != nil || !created {
		t.Fatalf("WriteConfigTemplate = (%t, %v)", created, err)
	}
	want, err := packaging.Files.ReadFile("linux/config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	checkConfigFile(t, path, string(want), 0o600)
	if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("config directory = %v, %v", info, err)
	}

	if err := os.WriteFile(path, []byte("custom\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if created, err := WriteConfigTemplate(path); err != nil || created {
		t.Fatalf("existing file: WriteConfigTemplate = (%t, %v)", created, err)
	}
	checkConfigFile(t, path, "custom\n", 0o640)

	link := filepath.Join(t.TempDir(), "config.yaml")
	target := filepath.Join(t.TempDir(), "outside.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if created, err := WriteConfigTemplate(link); err != nil || created {
		t.Fatalf("dangling symlink: WriteConfigTemplate = (%t, %v)", created, err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("template followed a dangling symlink: %v", err)
	}
}

func checkConfigFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != content {
		t.Fatalf("%s = %q, %v", path, data, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != mode {
		t.Fatalf("%s mode = %v, %v; want %v", path, info.Mode().Perm(), err, mode)
	}
}

func TestLoadConfigRejectsTemplatePlaceholders(t *testing.T) {
	t.Setenv("POLOCHON_URL", "")
	t.Setenv("POLOCHON_TOKEN", "")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if _, err := WriteConfigTemplate(path); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "example value") {
		t.Fatalf("template config error = %v", err)
	}
	t.Setenv("POLOCHON_TOKEN", "real-token")
	if _, err := LoadConfig(path); err != nil {
		t.Fatalf("token from environment: %v", err)
	}
	if _, err := LoadConfig(filepath.Join(t.TempDir(), "missing.yaml")); err == nil || !strings.Contains(err.Error(), "polochon_url is not set") {
		t.Fatalf("missing config error = %v", err)
	}
}

func TestHeadlessSetupScreen(t *testing.T) {
	closed := false
	a := NewSetup("/home/user/.config/pepe/config.yaml", errors.New("polochon_url is not set (config file or POLOCHON_URL)"), true, nil, func() { closed = true })
	d, err := driver.New(driver.Config{Width: 1024, Height: 768, PxPerDp: 1, PxPerSp: 1}, a.Layout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	f := &headlessFixture{app: a, driver: d}
	tree := d.Tree()
	for _, label := range []string{"Set up pépé", "/home/user/.config/pepe/config.yaml", "polochon_url is not set (config file or POLOCHON_URL)"} {
		if !headlessHasLabel(tree, label) {
			t.Fatalf("setup screen missing %q", label)
		}
	}
	// Browse keys must not reach the hidden browse screen or close the window.
	for _, name := range []string{"Enter", "DownArrow", "RightArrow"} {
		headlessKey(t, f, name)
	}
	if closed || a.detail != nil {
		t.Fatal("navigation keys acted on the setup screen")
	}
	headlessKey(t, f, "Q")
	if !closed {
		t.Fatal("q did not close the setup window")
	}
}
