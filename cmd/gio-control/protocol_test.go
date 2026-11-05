package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/widget"
	"github.com/gregdel/pepe/internal/driver"
)

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestServeTreeAndClick(t *testing.T) {
	var button widget.Clickable
	clicked := false
	drv, err := driver.New(driver.Config{Width: 320, Height: 200, PxPerDp: 1, PxPerSp: 1}, func(gtx layout.Context) layout.Dimensions {
		if button.Clicked(gtx) {
			clicked = true
		}
		return button.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			semantic.Button.Add(gtx.Ops)
			semantic.LabelOp("Apply").Add(gtx.Ops)
			return layout.Dimensions{Size: gtx.Constraints.Max}
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	defer drv.Close()

	requests := strings.NewReader("{\"id\":1,\"method\":\"tree\"}\n{\"id\":2,\"method\":\"click\",\"params\":{\"label\":\"Apply\"}}\n{\"id\":3,\"method\":\"shutdown\"}\n")
	var output bytes.Buffer
	if err := Serve(context.Background(), requests, &output, drv, "."); err != nil {
		t.Fatal(err)
	}
	type wireResponse struct {
		ID     uint64          `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	var responses []wireResponse
	scanner := bufio.NewScanner(&output)
	for scanner.Scan() {
		var response wireResponse
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		responses = append(responses, response)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(responses) != 3 {
		t.Fatalf("got %d responses, want 3", len(responses))
	}
	for i, response := range responses {
		if response.ID != uint64(i+1) || response.Error != "" {
			t.Fatalf("response %d = %+v", i+1, response)
		}
	}
	var tree []driver.Node
	if err := json.Unmarshal(responses[0].Result, &tree); err != nil {
		t.Fatal(err)
	}
	if len(tree) != 1 || len(tree[0].Children) == 0 || !hasLabel(tree, "Apply") {
		t.Fatalf("tree response = %+v, want one root containing Apply", tree)
	}
	if !clicked {
		t.Fatal("unique label click did not reach the button")
	}
}

func TestServeReturnsResponseWriteError(t *testing.T) {
	want := errors.New("write failed")
	err := Serve(context.Background(), strings.NewReader("{invalid}\n"), failingWriter{want}, nil, "")
	if !errors.Is(err, want) {
		t.Fatalf("Serve() error = %v, want %v", err, want)
	}
}
