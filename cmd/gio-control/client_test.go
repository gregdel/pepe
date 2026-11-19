package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"io"
	"strings"
	"testing"

	"github.com/gregdel/pepe/internal/driver"
)

func TestBuildCallRequest(t *testing.T) {
	id := uint64(7)
	x, y := float32(10), float32(20.5)
	negX, ten := float32(-1), float32(10)
	tests := []struct {
		args   []string
		want   string
		output callOutput
	}{
		{[]string{"tree"}, `{"method":"tree"}`, outputJSON},
		{[]string{"tree", "--compact"}, `{"method":"tree"}`, outputCompactTree},
		{[]string{"tree", "--all"}, `{"method":"tree"}`, callOutput{kind: kindCompactTree, tree: treeOptions{all: true}}},
		{[]string{"tree", "--compact", "--selected"}, `{"method":"tree"}`, callOutput{kind: kindCompactTree, tree: treeOptions{selected: true}}},
		{[]string{"screenshot", "--crop", "0", "10", "400", "300", "--scale", "0.5"}, `{"method":"screenshot","params":{"crop":{"x":0,"y":10,"width":400,"height":300},"scale":0.5}}`, outputScreenshotPath},
		{[]string{"screenshot", "--scale", "1"}, `{"method":"screenshot","params":{"scale":1}}`, outputScreenshotPath},
		{[]string{"key", "RightArrow"}, `{"method":"key","params":{"name":"RightArrow","modifiers":[]}}`, outputSelection},
		{[]string{"key", "Tab", "Shift"}, `{"method":"key","params":{"name":"Tab","modifiers":["Shift"]}}`, outputSelection},
		{[]string{"click", "Play movie"}, mustJSON(t, callRequest{Method: "click", Params: clickParams{Label: "Play movie"}}), outputSelection},
		{[]string{"click", "--occurrence", "2", "Movies"}, mustJSON(t, callRequest{Method: "click", Params: clickParams{Label: "Movies", Occurrence: 2}}), outputSelection},
		{[]string{"click", "--id", "7"}, mustJSON(t, callRequest{Method: "click", Params: clickParams{ID: &id}}), outputSelection},
		{[]string{"click", "--xy", "10", "20.5"}, mustJSON(t, callRequest{Method: "click", Params: clickParams{X: &x, Y: &y}}), outputSelection},
		{[]string{"type", "hello world"}, `{"method":"type","params":{"text":"hello world"}}`, outputSelection},
		{[]string{"resize", "1920", "1080"}, `{"method":"resize","params":{"width":1920,"height":1080}}`, outputJSON},
		{[]string{"screenshot"}, `{"method":"screenshot"}`, outputScreenshotPath},
		{[]string{"screenshot", "--no-wait", "--scale", "0.5"}, `{"method":"screenshot","params":{"scale":0.5,"noWait":true}}`, outputScreenshotPath},
		{[]string{"wait"}, `{"method":"wait"}`, outputJSON},
		{[]string{"wait", "2.5"}, `{"method":"wait","params":{"seconds":2.5}}`, outputJSON},
		{[]string{"shutdown"}, `{"method":"shutdown"}`, outputJSON},
		{[]string{"size"}, `{"method":"size"}`, outputJSON},
		{[]string{"raw", `{"method":"key","params":{"name":"Escape"}}`}, `{"method":"key","params":{"name":"Escape"}}`, outputJSON},
		// Arguments that look like options are taken literally.
		{[]string{"type", "-5"}, `{"method":"type","params":{"text":"-5"}}`, outputSelection},
		{[]string{"type", "--help"}, `{"method":"type","params":{"text":"--help"}}`, outputSelection},
		{[]string{"key", "-"}, `{"method":"key","params":{"name":"-","modifiers":[]}}`, outputSelection},
		{[]string{"resize", "-1", "100"}, `{"method":"resize","params":{"width":-1,"height":100}}`, outputJSON},
		{[]string{"click", "-label"}, mustJSON(t, callRequest{Method: "click", Params: clickParams{Label: "-label"}}), outputSelection},
		{[]string{"click", "--", "--xy"}, mustJSON(t, callRequest{Method: "click", Params: clickParams{Label: "--xy"}}), outputSelection},
		{[]string{"click", "--occurrence", "2", "-x"}, mustJSON(t, callRequest{Method: "click", Params: clickParams{Label: "-x", Occurrence: 2}}), outputSelection},
		{[]string{"click", "--xy", "-1", "10"}, mustJSON(t, callRequest{Method: "click", Params: clickParams{X: &negX, Y: &ten}}), outputSelection},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			req, output, err := buildCallRequest(tt.args[0], tt.args[1:])
			if err != nil {
				t.Fatal(err)
			}
			if got := mustJSON(t, req); got != tt.want {
				t.Fatalf("request = %s, want %s", got, tt.want)
			}
			if output != tt.output {
				t.Fatalf("output = %v, want %v", output, tt.output)
			}
		})
	}
}

func TestBuildCallRequestRejectsBadArguments(t *testing.T) {
	for _, args := range [][]string{
		{"bogus"},
		{"tree", "extra"},
		{"key"},
		{"click"},
		{"click", "a", "b"},
		{"click", "--id", "1", "label"},
		{"click", "--xy", "1"},
		{"click", "--xy", "--id", "1", "1", "2"},
		{"click", "--occurrence", "-1", "a"},
		{"resize", "100"},
		{"resize", "wide", "100"},
		{"type"},
		{"screenshot", "now"},
		{"raw", "{}"},
		{"raw", "not json"},
		{"tree", "--compact", "extra"},
		{"click", "--id", "-1"},
		{"click", "--occurrence", "2"},
		{"click", "--", "a", "b"},
		{"type", "a", "b"},
		{"tree", "--all", "--selected"},
		{"screenshot", "--crop", "0", "0", "10"},
		{"screenshot", "--crop", "0", "0", "0", "10"},
		{"screenshot", "--crop", "0", "0", "x", "10"},
		{"screenshot", "--crop", "0", "0", "10", "10", "--crop", "0", "0", "10", "10"},
		{"screenshot", "--scale"},
		{"screenshot", "--scale", "0"},
		{"screenshot", "--scale", "1.5"},
		{"screenshot", "--scale", "NaN"},
		{"screenshot", "--scale", "half"},
		{"screenshot", "--no-wait", "--no-wait"},
		{"wait", "0"},
		{"wait", "26"},
		{"wait", "NaN"},
		{"wait", "1", "2"},
	} {
		if _, _, err := buildCallRequest(args[0], args[1:]); err == nil {
			t.Errorf("buildCallRequest(%q) succeeded, want error", args)
		}
	}
}

func TestParseServeArgs(t *testing.T) {
	t.Setenv("GIO_CONTROL_SOCKET", "")
	if got, err := parseServeArgs(nil); err != nil || got != defaultSocketPath {
		t.Fatalf("parseServeArgs(nil) = %q, %v", got, err)
	}
	if got, err := parseServeArgs([]string{"--socket", "/run/x.sock"}); err != nil || got != "/run/x.sock" {
		t.Fatalf("parseServeArgs(--socket) = %q, %v", got, err)
	}
	for _, args := range [][]string{{"--socket", ""}, {"extra"}, {"--bogus"}} {
		if _, err := parseServeArgs(args); err == nil {
			t.Errorf("parseServeArgs(%q) succeeded, want error", args)
		}
	}
}

func TestRunCallUsageErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runCall(nil, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "usage: gio-control call") {
		t.Fatalf("runCall(nil) = %d, stderr %q", code, stderr.String())
	}
	stderr.Reset()
	if code := runCall([]string{"--socket", "/nonexistent/s.sock", "tree"}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "no gio-control server") {
		t.Fatalf("runCall(missing server) = %d, stderr %q", code, stderr.String())
	}
}

func TestPrintCallResult(t *testing.T) {
	var out bytes.Buffer
	if err := printCallResult(&out, io.Discard, outputScreenshotPath, json.RawMessage(`{"path":"a.png","absolutePath":"/abs/a.png"}`), image.Rectangle{}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "/abs/a.png\n" {
		t.Fatalf("screenshot output = %q", out.String())
	}
	out.Reset()
	if err := printCallResult(&out, io.Discard, outputJSON, json.RawMessage(`{"ok":true}`), image.Rectangle{}); err != nil || out.String() != "{\"ok\":true}\n" {
		t.Fatalf("JSON output = %q, %v", out.String(), err)
	}
	if err := printCallResult(&out, io.Discard, outputScreenshotPath, json.RawMessage(`{"path":"a.png"}`), image.Rectangle{}); err == nil {
		t.Fatal("screenshot without absolutePath succeeded")
	}
	out.Reset()
	var note bytes.Buffer
	if err := printCallResult(&out, &note, outputScreenshotPath, json.RawMessage(`{"absolutePath":"/abs/a.png","loading":true}`), image.Rectangle{}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "/abs/a.png\n" || !strings.Contains(note.String(), "still loading") {
		t.Fatalf("loading screenshot output = %q, note %q", out.String(), note.String())
	}
}

func TestFormatCompactTree(t *testing.T) {
	nodes := []driver.Node{{
		ID: 1, Class: "unknown", Bounds: driver.Bounds{MaxX: 100, MaxY: 50},
		Children: []driver.Node{
			{ID: 2, Class: "button", Label: "Play movie", Selected: true, Bounds: driver.Bounds{MinX: 10, MinY: 5, MaxX: 60, MaxY: 25}},
			{ID: 3, Label: `Say "hi"`, Description: "hint", Disabled: true, Bounds: driver.Bounds{MinX: 1, MinY: 2, MaxX: 3, MaxY: 4}},
		},
	}}
	want := "#1 @0,0 100x50\n" +
		"  #2 button \"Play movie\" [selected] @10,5 50x20\n" +
		"  #3 \"Say \\\"hi\\\"\" (hint) [disabled] @1,2 2x2\n"
	if got := formatCompactTree(nodes, image.Rect(0, 0, 200, 100), treeOptions{all: true}); got != want {
		t.Fatalf("formatCompactTree() =\n%s\nwant\n%s", got, want)
	}
}

func TestFormatCompactTreeClipsToViewportAndAncestors(t *testing.T) {
	nodes := []driver.Node{{
		ID: 1, Bounds: driver.Bounds{MinX: -1000000, MinY: -1000000, MaxX: 1000000, MaxY: 1000000},
		Children: []driver.Node{
			// Wider than the viewport.
			{ID: 2, Label: "header", Bounds: driver.Bounds{MinX: 90, MinY: 0, MaxX: 300, MaxY: 10}},
			{ID: 3, Class: "button", Label: "Movies", Bounds: driver.Bounds{MinX: 10, MinY: 20, MaxX: 60, MaxY: 40},
				Children: []driver.Node{
					// An unconstrained text box, cut to its button.
					{ID: 4, Label: "Movies", Bounds: driver.Bounds{MinX: 15, MinY: 25, MaxX: 55, MaxY: 1000000}},
				}},
			{ID: 5, Class: "button", Label: "Below", Bounds: driver.Bounds{MinX: 10, MinY: 120, MaxX: 60, MaxY: 140},
				Children: []driver.Node{{ID: 6, Label: "Below", Bounds: driver.Bounds{MinX: 10, MinY: 120, MaxX: 60, MaxY: 140}}}},
		},
	}}
	want := "#1 @0,0 100x100\n" +
		"  #2 \"header\" @90,0 10x10\n" +
		"  #3 button \"Movies\" @10,20 50x20\n" +
		"    #4 \"Movies\" @15,25 40x15\n" +
		"  #5 button \"Below\" [offscreen, 1 more]\n"
	if got := formatCompactTree(nodes, image.Rect(0, 0, 100, 100), treeOptions{all: true}); got != want {
		t.Fatalf("formatCompactTree() =\n%s\nwant\n%s", got, want)
	}
}

func TestFormatCompactTreeTrims(t *testing.T) {
	nodes := []driver.Node{{
		ID: 1, Bounds: driver.Bounds{MaxX: 100, MaxY: 100},
		Children: []driver.Node{
			{ID: 2, Label: "Title", Bounds: driver.Bounds{MaxX: 100, MaxY: 10}},
			// A scrollable wrapper is kept; a plain one is not.
			{ID: 3, Gestures: []string{"scroll"}, Bounds: driver.Bounds{MinY: 10, MaxX: 100, MaxY: 90}, Children: []driver.Node{
				{ID: 4, Class: "button", Label: "Movies", Bounds: driver.Bounds{MinY: 10, MaxX: 100, MaxY: 30}, Children: []driver.Node{
					{ID: 5, Selected: true, Bounds: driver.Bounds{MinY: 10, MaxX: 100, MaxY: 30}, Children: []driver.Node{
						{ID: 6, Label: "Movies", Bounds: driver.Bounds{MinY: 12, MaxX: 80, MaxY: 28}},
					}},
				}},
				{ID: 7, Class: "button", Label: "Shows", Bounds: driver.Bounds{MinY: 30, MaxX: 100, MaxY: 50}},
			}},
			{ID: 8, Bounds: driver.Bounds{MinY: 200, MaxX: 100, MaxY: 300}, Children: []driver.Node{
				{ID: 9, Label: "hidden"}, {ID: 10, Label: "hidden too"},
			}},
			// Offscreen plain wrappers are counted out, and dropped when
			// nothing under them would be printed.
			{ID: 11, Bounds: driver.Bounds{MinY: 200, MaxX: 100, MaxY: 300}, Children: []driver.Node{
				{ID: 12, Children: []driver.Node{{ID: 13, Label: "deep"}}},
			}},
			{ID: 14, Bounds: driver.Bounds{MinY: 200, MaxX: 100, MaxY: 300}, Children: []driver.Node{{ID: 15}}},
		},
	}}
	viewport := image.Rect(0, 0, 100, 100)

	want := "#2 \"Title\" @0,0 100x10\n" +
		"#3 @0,10 100x80\n" +
		"  #4 button \"Movies\" @0,10 100x20\n" +
		"    #5 [selected] @0,10 100x20\n" +
		"      #6 \"Movies\" @0,12 80x16\n" +
		"  #7 button \"Shows\" @0,30 100x20\n" +
		"#8 [offscreen, 2 more]\n" +
		"#11 [offscreen, 1 more]\n"
	if got := formatCompactTree(nodes, viewport, treeOptions{}); got != want {
		t.Fatalf("default =\n%s\nwant\n%s", got, want)
	}

	want = "#3 @0,10 100x80\n" +
		"  #4 button \"Movies\" @0,10 100x20\n" +
		"    #5 [selected] @0,10 100x20\n" +
		"      #6 \"Movies\" @0,12 80x16\n"
	if got := formatCompactTree(nodes, viewport, treeOptions{selected: true}); got != want {
		t.Fatalf("selected =\n%s\nwant\n%s", got, want)
	}

	nodes[0].Children[1].Children[0].Children[0].Selected = false
	if got := formatCompactTree(nodes, viewport, treeOptions{selected: true}); got != "no selection\n" {
		t.Fatalf("selected without a selection = %q", got)
	}
}

// TestFormatCompactTreeOffscreenSelection keeps the path to a selection that
// is scrolled out of view, instead of collapsing an ancestor before it.
func TestFormatCompactTreeOffscreenSelection(t *testing.T) {
	nodes := []driver.Node{{
		ID: 1, Bounds: driver.Bounds{MaxX: 100, MaxY: 100},
		Children: []driver.Node{
			{ID: 2, Gestures: []string{"scroll"}, Bounds: driver.Bounds{MinY: 200, MaxX: 100, MaxY: 400}, Children: []driver.Node{
				{ID: 3, Class: "button", Label: "Other", Bounds: driver.Bounds{MinY: 200, MaxX: 100, MaxY: 220}},
				{ID: 4, Bounds: driver.Bounds{MinY: 220, MaxX: 100, MaxY: 240}, Children: []driver.Node{
					{ID: 5, Class: "button", Label: "Episode", Selected: true, Bounds: driver.Bounds{MinY: 220, MaxX: 100, MaxY: 240}, Children: []driver.Node{
						{ID: 6, Label: "Episode"},
					}},
				}},
			}},
		},
	}}
	want := "#2 [offscreen]\n" +
		"  #5 button \"Episode\" [selected] [offscreen, 1 more]\n"
	if got := formatCompactTree(nodes, image.Rect(0, 0, 100, 100), treeOptions{selected: true}); got != want {
		t.Fatalf("selected =\n%s\nwant\n%s", got, want)
	}
	want = "#1 @0,0 100x100\n" +
		"  #2 [offscreen]\n" +
		"    #4 [offscreen]\n" +
		"      #5 button \"Episode\" [selected] [offscreen, 1 more]\n"
	if got := formatCompactTree(nodes, image.Rect(0, 0, 100, 100), treeOptions{selected: true, all: true}); got != want {
		t.Fatalf("selected all =\n%s\nwant\n%s", got, want)
	}
	want = "#1 @0,0 100x100\n" +
		"  #2 [offscreen]\n" +
		"    #3 button \"Other\" [offscreen]\n" +
		"    #4 [offscreen]\n" +
		"      #5 button \"Episode\" [selected] [offscreen, 1 more]\n"
	if got := formatCompactTree(nodes, image.Rect(0, 0, 100, 100), treeOptions{all: true}); got != want {
		t.Fatalf("all =\n%s\nwant\n%s", got, want)
	}
	want = "#2 [offscreen]\n" +
		"  #3 button \"Other\" [offscreen]\n" +
		"  #5 button \"Episode\" [selected] [offscreen, 1 more]\n"
	if got := formatCompactTree(nodes, image.Rect(0, 0, 100, 100), treeOptions{}); got != want {
		t.Fatalf("default =\n%s\nwant\n%s", got, want)
	}
}

// TestFormatCompactTreeNestedSelection keeps an off-screen selection nested
// in a visible selected node.
func TestFormatCompactTreeNestedSelection(t *testing.T) {
	nodes := []driver.Node{{
		ID: 1, Selected: true, Label: "Popup", Bounds: driver.Bounds{MaxX: 100, MaxY: 100},
		Children: []driver.Node{
			{ID: 2, Gestures: []string{"scroll"}, Bounds: driver.Bounds{MinY: 200, MaxX: 100, MaxY: 300}, Children: []driver.Node{
				{ID: 3, Label: "Hidden"},
				{ID: 4, Label: "Track 9", Selected: true, Bounds: driver.Bounds{MinY: 280, MaxX: 100, MaxY: 300}},
			}},
		},
	}}
	want := "#1 \"Popup\" [selected] @0,0 100x100\n" +
		"  #2 [offscreen]\n" +
		"    #3 \"Hidden\" [offscreen]\n" +
		"    #4 \"Track 9\" [selected] [offscreen]\n"
	for _, opts := range []treeOptions{{}, {selected: true}} {
		if got := formatCompactTree(nodes, image.Rect(0, 0, 100, 100), opts); got != want {
			t.Fatalf("%+v =\n%s\nwant\n%s", opts, got, want)
		}
	}
}

func TestFormatSelection(t *testing.T) {
	if got := formatSelection(nil); got != "no selection\n" {
		t.Fatalf("formatSelection(nil) = %q", got)
	}
	got := formatSelection([]driver.Selection{
		{ID: 5, Label: "Movies", Bounds: driver.Bounds{MinX: 1, MinY: 2, MaxX: 11, MaxY: 22}},
		{ID: 9},
	})
	if want := "#5 \"Movies\" @1,2 10x20\n#9 [offscreen]\n"; got != want {
		t.Fatalf("formatSelection() = %q, want %q", got, want)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPrintCallResultSelectionWarning(t *testing.T) {
	var b strings.Builder
	result := json.RawMessage(`{"ok":true,"frame":3,"warning":"action applied, but the UI did not settle: boom"}`)
	err := printCallResult(&b, io.Discard, outputSelection, result, image.Rectangle{})
	var applied appliedWarning
	if !errors.As(err, &applied) {
		t.Fatalf("printCallResult() error = %v, want appliedWarning", err)
	}
	if want := "warning: action applied, but the UI did not settle: boom"; err.Error() != want {
		t.Fatalf("printCallResult() error = %q, want %q", err.Error(), want)
	}
	if b.Len() != 0 {
		t.Fatalf("printCallResult() wrote %q, want nothing on stdout", b.String())
	}

	// An older server reports no selection field at all.
	err = printCallResult(&b, io.Discard, outputSelection, json.RawMessage(`{"ok":true,"frame":3}`), image.Rectangle{})
	if !errors.As(err, &applied) || b.Len() != 0 {
		t.Fatalf("printCallResult(no selected field) = %q, %v, want an appliedWarning", b.String(), err)
	}
	if err := printCallResult(&b, io.Discard, outputSelection, json.RawMessage(`{"ok":true,"selected":[]}`), image.Rectangle{}); err != nil || b.String() != "no selection\n" {
		t.Fatalf("printCallResult(empty selection) = %q, %v", b.String(), err)
	}
}
