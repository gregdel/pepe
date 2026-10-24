package install

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Manifest filenames are relative payload paths, never arbitrary host paths.
type manifest map[string]string

func fileHash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (m manifest) bytes() []byte {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	var out strings.Builder
	for _, name := range names {
		fmt.Fprintf(&out, "%s  %s\n", m[name], name)
	}
	return []byte(out.String())
}

func readManifest(dir string) (manifest, error) {
	path := filepath.Join(dir, "SHA256SUMS")
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular manifest: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	m := make(manifest)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			return nil, errors.New("invalid checksum manifest line")
		}
		digest, name := fields[0], fields[1]
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != sha256.Size || digest != strings.ToLower(digest) || !validPayloadPath(name) || m[name] != "" {
			return nil, fmt.Errorf("invalid checksum manifest entry: %s", name)
		}
		m[name] = digest
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(m) == 0 {
		return nil, errors.New("empty checksum manifest")
	}
	return m, nil
}

func validPayloadPath(name string) bool {
	if !fs.ValidPath(name) || name == "SHA256SUMS" || name == ".pepe-managed" {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if !safeName(part) {
			return false
		}
	}
	return true
}

func noSymlinks(dir string) error {
	return filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing payload symlink: %s", path)
		}
		return nil
	})
}

func verifyPayload(dir string, want manifest) error {
	got, err := readManifest(dir)
	if err != nil {
		return err
	}
	if string(got.bytes()) != string(want.bytes()) {
		return errors.New("payload manifest differs from staged contents")
	}
	if err := noSymlinks(dir); err != nil {
		return err
	}
	for name, digest := range got {
		path := filepath.Join(dir, filepath.FromSlash(name))
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("not a regular payload file: %s", path)
		}
		if name == "pepe" || strings.HasSuffix(name, ".sh") {
			if info.Mode().Perm()&0o111 == 0 {
				return fmt.Errorf("not executable: %s", path)
			}
		}
		actual, err := fileHash(path)
		if err != nil {
			return err
		}
		if actual != digest {
			return fmt.Errorf("modified installed file: %s", path)
		}
	}
	return nil
}
