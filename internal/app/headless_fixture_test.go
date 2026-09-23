package app

import "testing"

func TestHeadlessPlaybackClient(t *testing.T) {
	client, err := NewHeadlessPlaybackClient(nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := client.getSnapshot()
	if snapshot.movieCount != 2 || snapshot.showCount != 0 {
		t.Fatalf("fixture counts = (%d, %d)", snapshot.movieCount, snapshot.showCount)
	}
	for _, item := range []struct{ id, title string }{
		{"demo-one", "First Movie (2025)"},
		{"demo-two", "Second Movie (2026)"},
	} {
		movie, ok := client.getMovie(item.id)
		if !ok {
			t.Fatalf("movie %q missing", item.id)
		}
		if _, err := client.getDownloadURL(movie); err != nil {
			t.Fatalf("movie %q download URL: %v", item.id, err)
		}
		node := snapshot.movieRadix.Lookup(item.title)
		if node == nil || node.Media == nil || node.Media.ID != item.id {
			t.Fatalf("movie %q terminal = %+v", item.id, node)
		}
	}
}
