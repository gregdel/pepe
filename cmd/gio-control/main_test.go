package main

import (
	"path/filepath"
	"testing"

	"github.com/gregdel/pepe/internal/driver"
)

func TestMockPlaybackUIUsesOfflineCatalog(t *testing.T) {
	t.Setenv("GIO_CONTROL_MOCK_MPV", "1")
	t.Setenv("GIO_CONTROL_CONFIG", filepath.Join(t.TempDir(), "missing.yaml"))
	t.Setenv("POLOCHON_URL", "http://127.0.0.1:1")
	t.Setenv("POLOCHON_TOKEN", "unrelated-real-configuration")
	ui, cleanup, err := newUI()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	drv, err := driver.New(driver.Config{Width: 1024, Height: 768, PxPerDp: 1, PxPerSp: 1}, ui.Layout)
	if err != nil {
		t.Fatal(err)
	}
	defer drv.Close()
	if !hasLabel(drv.Tree(), "2 movies · 0 shows") {
		t.Fatal("mock UI did not load the offline catalog")
	}
	if err := drv.Key("RightArrow", nil); err != nil {
		t.Fatal(err)
	}
	if !hasLabel(drv.Tree(), "First Movie (2025)") || !hasLabel(drv.Tree(), "Second Movie (2026)") {
		t.Fatal("mock UI missing selectable movies")
	}
	if err := drv.Key("RightArrow", nil); err != nil {
		t.Fatal(err)
	}
	if !hasLabel(drv.Tree(), "Play movie") {
		t.Fatal("mock UI could not open movie detail")
	}
}

func hasLabel(nodes []driver.Node, label string) bool {
	for _, node := range nodes {
		if node.Label == label || hasLabel(node.Children, label) {
			return true
		}
	}
	return false
}
