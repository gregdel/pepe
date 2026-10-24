package packaging

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"strings"
	"testing"
)

func TestEmbeddedArtworkMatchesManifest(t *testing.T) {
	declared := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(Manifest))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 || !fs.ValidPath(fields[1]) || strings.Contains(fields[1], "/") {
			t.Fatalf("invalid artwork manifest line: %q", line)
		}
		digest, name := fields[0], "embed/"+fields[1]
		if declared[name] {
			t.Fatalf("duplicate artwork declaration: %s", name)
		}
		declared[name] = true
		data, err := Files.ReadFile(name)
		if err != nil {
			t.Errorf("declared artwork %s: %v", name, err)
			continue
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != digest {
			t.Errorf("%s: SHA256 = %s, want %s", name, got, digest)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(declared) == 0 {
		t.Fatal("artwork manifest is empty")
	}
	if err := fs.WalkDir(Files, "embed", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if !declared[name] {
			t.Errorf("undeclared embedded artwork: %s", name)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
