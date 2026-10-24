package install

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gregdel/pepe/packaging"
)

type installer struct {
	paths paths
	out   io.Writer
}

func (i installer) install(source string) error {
	p := i.paths
	if err := p.validate(); err != nil {
		return err
	}
	binary, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = binary.Close() }()
	info, err := binary.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("executable is not a regular file")
	}
	if err := os.MkdirAll(filepath.Dir(p.root), 0o755); err != nil {
		return err
	}
	unlock, err := acquireLock(p.root)
	if err != nil {
		return err
	}
	defer unlock()
	// Another install/uninstall may have finished since the initial checks.
	if err := p.validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(p.root, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(p.root, ".pepe-managed"), []byte(ownership), 0o644); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(p.root, ".install-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	m, err := stagePayload(stage, binary)
	if err != nil {
		return err
	}
	for _, path := range []string{filepath.Dir(p.bin), filepath.Dir(p.desktop), filepath.Dir(p.config)} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return err
		}
	}
	if err := i.createConfig(); err != nil {
		return err
	}
	template, err := packaging.Files.ReadFile("linux/pepe.desktop.in")
	if err != nil {
		return err
	}
	desktop, err := temporaryFile(filepath.Dir(p.desktop), []byte(desktopEntry(p, string(template))), 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(desktop) }()
	launcher, err := temporaryLink(filepath.Dir(p.bin), filepath.Join(p.root, "pepe"))
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(launcher) }()
	if err := i.installPayload(stage, m); err != nil {
		return err
	}
	for _, move := range [][2]string{{launcher, p.bin}, {desktop, p.desktop}} {
		if err := os.Rename(move[0], move[1]); err != nil {
			return err
		}
	}
	i.refreshDesktop()
	_, _ = fmt.Fprintf(i.out, "Installed pépé\nLauncher: %s\nConfiguration: %s\nArtwork and mpv wrappers: %s\n", p.bin, p.config, p.root)
	return nil
}

func stagePayload(dir string, binary io.Reader) (manifest, error) {
	path := filepath.Join(dir, "pepe")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		return nil, err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(file, hash), binary)
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr, os.Chmod(path, 0o755)); err != nil {
		return nil, err
	}
	m := manifest{"pepe": hex.EncodeToString(hash.Sum(nil))}
	write := func(name string, data []byte, mode fs.FileMode) error {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, mode); err != nil {
			return err
		}
		if err := os.Chmod(path, mode); err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		m[name] = hex.EncodeToString(sum[:])
		return nil
	}
	err = fs.WalkDir(packaging.Files, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := packaging.Files.ReadFile(name)
		if err != nil {
			return err
		}
		mode := fs.FileMode(0o644)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		dest := strings.Replace(name, "embed/", "assets/", 1)
		dest = strings.TrimPrefix(dest, "linux/")
		return write(dest, data, mode)
	})
	if err == nil {
		err = os.WriteFile(filepath.Join(dir, "SHA256SUMS"), m.bytes(), 0o644)
	}
	if err == nil {
		err = verifyPayload(dir, m)
	}
	return m, err
}

func (i installer) installPayload(stage string, m manifest) error {
	root := i.paths.root
	old, err := readManifest(root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	names := make([]string, 0, len(m))
	for name := range m {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := checkParents(filepath.Dir(path)); err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err == nil {
			if !info.Mode().IsRegular() || old[name] == "" {
				return fmt.Errorf("unrelated installed file: %s", path)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if name != "pepe" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	// Record owned paths first so an interrupted first install can be retried.
	if err := os.Rename(filepath.Join(stage, "SHA256SUMS"), filepath.Join(root, "SHA256SUMS")); err != nil {
		return err
	}
	// Rename rather than truncate: the installed executable may be running this
	// command. Replace it last; there is no release history.
	names = append(names, "pepe")
	for _, name := range names {
		dest := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(stage, filepath.FromSlash(name)), dest); err != nil {
			return err
		}
	}
	return nil
}

func (i installer) createConfig() error {
	// An existing entry (including a dangling symlink) needs no directory writes.
	if _, err := os.Lstat(i.paths.config); err == nil {
		_, _ = fmt.Fprintf(i.out, "Configuration already exists; keeping %s\n", i.paths.config)
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := packaging.Files.ReadFile("linux/config.example.yaml")
	if err != nil {
		return err
	}
	player := strings.ReplaceAll(filepath.Join(i.paths.root, "mpv-auto.sh"), "'", "''")
	data = []byte(strings.Replace(string(data), "mpv_path: mpv\n", "mpv_path: '"+player+"'\n", 1))
	temp, err := temporaryFile(filepath.Dir(i.paths.config), data, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temp) }()
	// Publish a complete private config atomically, without replacing an existing
	// file or even a dangling symlink. A failed write leaves no partial config.
	if err := os.Link(temp, i.paths.config); !errors.Is(err, os.ErrExist) {
		return err
	}
	_, _ = fmt.Fprintf(i.out, "Configuration already exists; keeping %s\n", i.paths.config)
	return nil
}

func acquireLock(root string) (func(), error) {
	// Keep the lock outside the removable tree: uninstall must stay serialized
	// until after removing the ownership marker and root directory.
	path := root + ".lock"
	if err := os.Mkdir(path, 0o700); err != nil {
		return nil, fmt.Errorf("cannot acquire installer lock (another installer or a stale .lock): %w", err)
	}
	locked := true
	return func() {
		if locked {
			_ = os.Remove(path)
			locked = false
		}
	}, nil
}

func temporaryFile(dir string, data []byte, mode fs.FileMode) (string, error) {
	file, err := os.CreateTemp(dir, ".pepe-")
	if err != nil {
		return "", err
	}
	_, writeErr := file.Write(data)
	err = errors.Join(writeErr, file.Chmod(mode), file.Close())
	if err != nil {
		_ = os.Remove(file.Name())
	}
	return file.Name(), err
}

func temporaryLink(dir, target string) (string, error) {
	file, err := os.CreateTemp(dir, ".pepe-")
	if err != nil {
		return "", err
	}
	if err := errors.Join(file.Close(), os.Remove(file.Name())); err != nil {
		return "", err
	}
	return file.Name(), os.Symlink(target, file.Name())
}

func (i installer) refreshDesktop() {
	if command, err := exec.LookPath("update-desktop-database"); err == nil {
		if err := exec.Command(command, filepath.Dir(i.paths.desktop)).Run(); err != nil {
			_, _ = fmt.Fprintf(i.out, "Warning: desktop database refresh failed: %v\n", err)
		}
	}
}

func (i installer) uninstall() error {
	p := i.paths
	if err := p.validate(); err != nil {
		return err
	}
	if !regularContents(filepath.Join(p.root, ".pepe-managed"), ownership) {
		_, _ = fmt.Fprintln(i.out, "No installed pépé found.")
		return nil
	}
	unlock, err := acquireLock(p.root)
	if err != nil {
		return err
	}
	defer unlock()
	if err := p.validate(); err != nil {
		return err
	}
	if _, err := os.Stat(p.root); errors.Is(err, os.ErrNotExist) {
		return nil // Another uninstall completed while waiting to acquire the lock.
	} else if err != nil {
		return err
	}
	for _, path := range []string{p.bin, p.desktop} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := i.removePayload(p.root); err != nil {
		_, _ = fmt.Fprintf(i.out, "Leaving installed files for inspection: %s: %v\n", p.root, err)
	}
	i.refreshDesktop()
	_, _ = fmt.Fprintln(i.out, "Removed installed pépé. Configuration, mpv and Steam shortcuts are untouched.")
	return nil
}

func (i installer) removePayload(dir string) error {
	m, err := readManifest(dir)
	if err != nil {
		return err
	}
	for name := range m {
		if err := checkParents(filepath.Dir(filepath.Join(dir, filepath.FromSlash(name)))); err != nil {
			return err
		}
	}
	for name, digest := range m {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if path == i.paths.config {
			continue
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		actual, err := fileHash(path)
		if err != nil {
			return err
		}
		if actual != digest {
			_, _ = fmt.Fprintf(i.out, "Leaving modified file: %s\n", path)
			continue
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	var directories []string
	if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && entry.IsDir() && path != dir {
			directories = append(directories, path)
		}
		return err
	}); err != nil {
		return err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(directories)))
	for _, path := range directories {
		_ = os.Remove(path)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) == 2 && entries[0].Name() == ".pepe-managed" && entries[1].Name() == "SHA256SUMS" {
		for _, name := range []string{"SHA256SUMS", ".pepe-managed"} {
			if err := os.Remove(filepath.Join(dir, name)); err != nil {
				return err
			}
		}
		return os.Remove(dir)
	}
	return errors.New("modified or unknown files remain")
}
