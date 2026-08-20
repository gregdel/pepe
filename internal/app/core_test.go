package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchAuthenticatedURLSendsToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Auth-Token"); got != "secret" {
			t.Errorf("auth token = %q, want secret", got)
		}
		_, _ = w.Write([]byte("payload"))
	}))
	defer server.Close()

	body, err := (&APIClient{token: "secret"}).fetchAuthenticatedURL(server.URL)
	if err != nil {
		t.Fatalf("fetchAuthenticatedURL: %v", err)
	}
	defer func() { _ = body.Close() }()

	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if string(got) != "payload" {
		t.Errorf("response body = %q, want payload", got)
	}
}

func TestFetchAuthenticatedURLRejectsUnexpectedStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	body, err := (&APIClient{}).fetchAuthenticatedURL(server.URL)
	if body != nil {
		t.Fatal("unexpected response body for failed request")
	}
	if err == nil || !strings.Contains(err.Error(), "401 Unauthorized") {
		t.Fatalf("error = %v, want 401 status", err)
	}
}
