package app

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAPIClientConstructorsInitializeEmptyRoots(t *testing.T) {
	configured, err := NewAPIClient("http://localhost", "token", nil)
	if err != nil {
		t.Fatal(err)
	}
	clients := []*APIClient{configured, NewEmptyAPIClient(nil)}
	for _, client := range clients {
		snapshot := client.getSnapshot()
		if snapshot.movieRadix == nil || snapshot.showRadix == nil || snapshot.movieRadix.Root() == nil || snapshot.showRadix.Root() == nil {
			t.Fatal("client constructor did not initialize empty movie and show roots")
		}
	}
}

func TestCatalogRequestsTimeoutAndRecover(t *testing.T) {
	var movieRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/movies" && movieRequests.Add(1) == 1 {
			time.Sleep(80 * time.Millisecond)
		}
		_, _ = w.Write([]byte("{}"))
	}))
	defer server.Close()

	client, err := NewAPIClient(server.URL, "token", nil)
	if err != nil {
		t.Fatal(err)
	}
	client.client.SetTimeout(20 * time.Millisecond)
	if err := client.FetchLibrary(); err == nil {
		t.Fatal("stalled catalog request did not time out")
	}
	if err := client.FetchLibrary(); err != nil {
		t.Fatalf("catalog request did not recover after timeout: %v", err)
	}
}

func TestArtworkRequestTimeoutAndRecovery(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			time.Sleep(80 * time.Millisecond)
		}
		_, _ = w.Write([]byte("payload"))
	}))
	defer server.Close()
	client := &APIClient{token: "token", artworkClient: &http.Client{Timeout: 20 * time.Millisecond}}

	if body, err := client.fetchAuthenticatedURL(server.URL); err == nil {
		_ = body.Close()
		t.Fatal("stalled artwork request did not time out")
	}
	body, err := client.fetchAuthenticatedURL(server.URL)
	if err != nil {
		t.Fatalf("artwork request did not recover after timeout: %v", err)
	}
	defer func() { _ = body.Close() }()
	got, err := io.ReadAll(body)
	if err != nil || string(got) != "payload" {
		t.Fatalf("recovered artwork body = %q, err=%v", got, err)
	}
}

func TestDecodeArtworkRejectsOversizedPayloadAndDimensions(t *testing.T) {
	if _, err := decodeArtwork(bytes.NewReader(make([]byte, artworkPayloadLimit+1))); err == nil {
		t.Fatal("oversized artwork payload was accepted")
	}

	smallImage := image.NewRGBA(image.Rect(0, 0, 1, 1))
	smallImage.Set(0, 0, color.White)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, smallImage); err != nil {
		t.Fatal(err)
	}
	pngBytes := encoded.Bytes()
	binary.BigEndian.PutUint32(pngBytes[16:20], artworkMaxDimension+1)
	binary.BigEndian.PutUint32(pngBytes[20:24], 1)
	binary.BigEndian.PutUint32(pngBytes[29:33], crc32.ChecksumIEEE(pngBytes[12:29]))
	if _, err := decodeArtwork(bytes.NewReader(pngBytes)); err == nil {
		t.Fatal("artwork with oversized dimensions was accepted")
	}

	encoded.Reset()
	if err := png.Encode(&encoded, smallImage); err != nil {
		t.Fatal(err)
	}
	if img, err := decodeArtwork(bytes.NewReader(encoded.Bytes())); err != nil || img.Bounds().Dx() != 1 {
		t.Fatalf("valid artwork decode = (%v, %v)", img, err)
	}
}

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
