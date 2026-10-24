package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

const ownership = "pepe-user-install-v1\n"

type paths struct {
	root, bin, desktop, config string
}

func environmentPaths() (paths, error) {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return paths{}, errors.New("HOME must be an absolute path")
	}
	data := os.Getenv("XDG_DATA_HOME")
	if !filepath.IsAbs(data) {
		data = filepath.Join(home, ".local", "share")
	}
	config := os.Getenv("XDG_CONFIG_HOME")
	if config == "" {
		config = filepath.Join(home, ".config")
	} else if !filepath.IsAbs(config) {
		return paths{}, errors.New("XDG_CONFIG_HOME must be absolute or unset")
	}
	p := paths{
		root:    filepath.Join(data, "pepe"),
		bin:     filepath.Join(home, ".local", "bin", "pepe"),
		desktop: filepath.Join(data, "applications", "pepe.desktop"),
		config:  filepath.Join(config, "pepe", "config.yaml"),
	}
	for _, path := range []string{p.root, p.bin, p.desktop, p.config} {
		if strings.ContainsFunc(path, unicode.IsControl) {
			return paths{}, errors.New("installation paths must not contain control characters")
		}
	}
	if strings.ContainsAny(p.bin, "=%") {
		return paths{}, errors.New("launcher paths containing '=' or '%' are unsupported by desktop launchers")
	}
	return p, nil
}

// Do not write or remove files through pre-existing symlink directories.
func checkParents(path string) error {
	for {
		info, err := os.Lstat(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && !info.IsDir() {
			return fmt.Errorf("not a real directory (symlinks are refused): %s", path)
		}
		parent := filepath.Dir(path)
		if parent == path {
			return nil
		}
		path = parent
	}
}

func (p paths) validate() error {
	for _, path := range []string{p.root, filepath.Dir(p.bin), filepath.Dir(p.desktop), filepath.Dir(p.config)} {
		if err := checkParents(path); err != nil {
			return err
		}
	}
	if regularContents(filepath.Join(p.root, "app", ".pepe-managed"), ownership) {
		return fmt.Errorf("old installation layout under %s: uninstall it with its original installer first, then update any old wrapper paths in configuration", p.root)
	}
	marker := filepath.Join(p.root, ".pepe-managed")
	if _, err := os.Lstat(marker); err == nil {
		if !regularContents(marker, ownership) {
			return fmt.Errorf("unrelated installation directory: %s", p.root)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else {
		// Existing user files (including the source icon) may share the data
		// directory, but never adopt an unmarked executable or manifest.
		for _, name := range []string{"pepe", "SHA256SUMS"} {
			if _, err := os.Lstat(filepath.Join(p.root, name)); err == nil {
				return fmt.Errorf("unmarked installation directory: %s", p.root)
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	if err := expectedLink(p.bin, filepath.Join(p.root, "pepe")); err != nil {
		return err
	}
	info, err := os.Lstat(p.desktop)
	if err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unrelated desktop entry: %s", p.desktop)
		}
		content, err := os.ReadFile(p.desktop)
		if err != nil {
			return err
		}
		if !strings.Contains("\n"+string(content), "\nX-Pepe-Bundle=true\n") {
			return fmt.Errorf("unrelated desktop entry: %s (unregister the checkout first if applicable)", p.desktop)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func expectedLink(path, target string) error {
	got, err := os.Readlink(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || got != target {
		return fmt.Errorf("unrelated launcher: %s", path)
	}
	return nil
}

func regularContents(path, want string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	data, err := os.ReadFile(path)
	return err == nil && string(data) == want
}

func safeName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	for _, c := range name {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._-", c) {
			continue
		}
		return false
	}
	return true
}

func desktopEntry(p paths, template string) string {
	escape := func(s string) string { return strings.ReplaceAll(s, "\\", "\\\\") }
	execPath := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "`", "\\`", "$", "\\$").Replace(p.bin)
	return template + fmt.Sprintf("X-Pepe-Bundle=true\nExec=\"%s\"\nTryExec=%s\nIcon=%s\n",
		escape(execPath), escape(p.bin), escape(filepath.Join(p.root, "assets", "application_icon_1024.png")))
}
