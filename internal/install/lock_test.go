package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLockSurvivesRootRemoval(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	unlock, err := acquireLock(root)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := os.Remove(root); err != nil {
		t.Fatalf("cannot remove installation while holding lock: %v", err)
	}
	if release, err := acquireLock(root); err == nil {
		release()
		t.Fatal("removing the root released the installer lock")
	}
	unlock()
	nextUnlock, err := acquireLock(root)
	if err != nil {
		t.Fatal(err)
	}
	defer nextUnlock()
	unlock() // Deferred cleanup must not remove a successor's lock.
	if _, err := os.Stat(root + ".lock"); err != nil {
		t.Fatalf("previous cleanup removed successor lock: %v", err)
	}
	nextUnlock()
	checkAbsent(t, root+".lock")
}
