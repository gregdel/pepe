package main

import (
	"bytes"
	"encoding/json"
	"image"
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
		{[]string{"key", "RightArrow"}, `{"method":"key","params":{"name":"RightArrow","modifiers":[]}}`, outputJSON},
		{[]string{"key", "Tab", "Shift"}, `{"method":"key","params":{"name":"Tab","modifiers":["Shift"]}}`, outputJSON},
		{[]string{"click", "Play movie"}, mustJSON(t, callRequest{Method: "click", Params: clickParams{Label: "Play movie"}}), outputJSON},
		{[]string{"click", "--occurrence", "2", "Movies"}, mustJSON(t, callRequest{Method: "click", Params: clickParams{Label: "Movies", Occurrence: 2}}), outputJSON},
		{[]string{"click", "--id", "7"}, mustJSON(t, callRequest{Method: "click", Params: clickParams{ID: &id}}), outputJSON},
		{[]string{"click", "--xy", "10", "20.5"}, mustJSON(t, callRequest{Method: "click", Params: clickParams{X: &x, Y: &y}}), outputJSON},
		{[]string{"type", "hello world"}, `{"method":"type","params":{"text":"hello world"}}`, outputJSON},
		{[]string{"resize", "1920", "1080"}, `{"method":"resize","params":{"width":1920,"height":1080}}`, outputJSON},
		{[]string{"screenshot"}, `{"method":"screenshot"}`, outputScreenshotPath},
		{[]string{"shutdown"}, `{"method":"shutdown"}`, outputJSON},
		{[]string{"size"}, `{"method":"size"}`, outputJSON},
		{[]string{"raw", `{"method":"key","params":{"name":"Escape"}}`}, `{"method":"key","params":{"name":"Escape"}}`, outputJSON},
		// Arguments that look like options are taken literally.
		{[]string{"type", "-5"}, `{"method":"type","params":{"text":"-5"}}`, outputJSON},
		{[]string{"type", "--help"}, `{"method":"type","params":{"text":"--help"}}`, outputJSON},
		{[]string{"key", "-"}, `{"method":"key","params":{"name":"-","modifiers":[]}}`, outputJSON},
		{[]string{"resize", "-1", "100"}, `{"method":"resize","params":{"width":-1,"height":100}}`, outputJSON},
		{[]string{"click", "-label"}, mustJSON(t, callRequest{Method: "click", Params: clickParams{Label: "-label"}}), outputJSON},
		{[]string{"click", "--", "--xy"}, mustJSON(t, callRequest{Method: "click", Params: clickParams{Label: "--xy"}}), outputJSON},
		{[]string{"click", "--occurrence", "2", "-x"}, mustJSON(t, callRequest{Method: "click", Params: clickParams{Label: "-x", Occurrence: 2}}), outputJSON},
		{[]string{"click", "--xy", "-1", "10"}, mustJSON(t, callRequest{Method: "click", Params: clickParams{X: &negX, Y: &ten}}), outputJSON},
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
	if err := printCallResult(&out, outputScreenshotPath, json.RawMessage(`{"path":"a.png","absolutePath":"/abs/a.png"}`), image.Rectangle{}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "/abs/a.png\n" {
		t.Fatalf("screenshot output = %q", out.String())
	}
	out.Reset()
	if err := printCallResult(&out, outputJSON, json.RawMessage(`{"ok":true}`), image.Rectangle{}); err != nil || out.String() != "{\"ok\":true}\n" {
		t.Fatalf("JSON output = %q, %v", out.String(), err)
	}
	if err := printCallResult(&out, outputScreenshotPath, json.RawMessage(`{"path":"a.png"}`), image.Rectangle{}); err == nil {
		t.Fatal("screenshot without absolutePath succeeded")
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
	if got := formatCompactTree(nodes, image.Rect(0, 0, 200, 100)); got != want {
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
		"  #5 button \"Below\" [offscreen]\n" +
		"    #6 \"Below\" [offscreen]\n"
	if got := formatCompactTree(nodes, image.Rect(0, 0, 100, 100)); got != want {
		t.Fatalf("formatCompactTree() =\n%s\nwant\n%s", got, want)
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
