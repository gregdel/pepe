package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestServeReturnsResponseWriteError(t *testing.T) {
	want := errors.New("write failed")
	err := Serve(context.Background(), strings.NewReader("{invalid}\n"), failingWriter{want}, nil, "")
	if !errors.Is(err, want) {
		t.Fatalf("Serve() error = %v, want %v", err, want)
	}
}
