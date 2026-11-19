package app

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"

	polochon "github.com/odwrtw/polochon/lib"
	index "github.com/odwrtw/polochon/lib/media_index"
	"github.com/odwrtw/polochon/lib/papi"
)

func TestAPIClientConstructorsInitializeEmptyRoots(t *testing.T) {
	configured, err := NewAPIClient("http://localhost", "token", nil)
	if err != nil {
		t.Fatal(err)
	}
	empty := NewEmptyAPIClient(nil)
	if configured.client == nil || configured.token != "token" || empty.client != nil || empty.token != "" {
		t.Fatal("configured and empty clients do not have their expected network state")
	}
	if err := empty.FetchLibrary(); err == nil {
		t.Fatal("empty client accepted a library fetch")
	}
	for _, client := range []*APIClient{configured, empty} {
		snapshot := client.getSnapshot()
		if snapshot.movieRadix == nil || snapshot.showRadix == nil || snapshot.movieRadix.Root() == nil || snapshot.showRadix.Root() == nil {
			t.Fatal("client constructor did not initialize empty movie and show roots")
		}
		if client.movies == nil || client.shows == nil || client.log == nil || client.artworkClient == nil || client.artworkClient.Timeout != artworkRequestTimeout {
			t.Fatal("client constructor did not initialize shared defaults")
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
	} else if !isTransientArtworkError(err) || artworkLogReason(err) != "artwork request timed out" {
		t.Fatalf("timeout error = %v (transient %v, reason %q), want a transient timeout", err, isTransientArtworkError(err), artworkLogReason(err))
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

func TestArtworkFailuresClassifiedAsTransient(t *testing.T) {
	for status, transient := range map[int]bool{
		http.StatusNotFound:            false,
		http.StatusUnauthorized:        false,
		http.StatusRequestTimeout:      true,
		http.StatusTooManyRequests:     true,
		http.StatusInternalServerError: true,
		http.StatusServiceUnavailable:  true,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		_, err := (&APIClient{}).fetchAuthenticatedURL(server.URL)
		server.Close()
		if err == nil || isTransientArtworkError(err) != transient {
			t.Errorf("status %d: error %v, transient = %v, want %v", status, err, isTransientArtworkError(err), transient)
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	deadURL := server.URL
	server.Close()
	if _, err := (&APIClient{}).fetchAuthenticatedURL(deadURL); !isTransientArtworkError(err) {
		t.Errorf("connection failure %v is not transient", err)
	}
	if _, err := decodeArtwork(iotest.ErrReader(errors.New("connection reset"))); !isTransientArtworkError(err) {
		t.Errorf("interrupted download %v is not transient", err)
	}
	if _, err := decodeArtwork(strings.NewReader("not an image")); err == nil || isTransientArtworkError(err) {
		t.Errorf("undecodable artwork error %v, want a permanent failure", err)
	}
	if isTransientArtworkError(&artworkError{"image file not set", errors.New("unset")}) {
		t.Error("missing image file is transient")
	}
}

func TestFetchAuthenticatedURLRejectsCrossHostRedirect(t *testing.T) {
	var destinationRequests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destinationRequests.Add(1)
		if r.Header.Get("X-Auth-Token") != "" {
			t.Error("redirect destination received the token")
		}
		_, _ = w.Write([]byte("payload"))
	}))
	defer destination.Close()

	// Give the destination a different hostname, routing it to loopback without
	// DNS or proxy dependencies. Two ports on 127.0.0.1 alone are not two hosts.
	destinationURL := strings.Replace(destination.URL, "127.0.0.1", "artwork.invalid", 1)
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if strings.HasPrefix(addr, "artwork.invalid:") {
				addr = destination.Listener.Addr().String()
			}
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}
	defer transport.CloseIdleConnections()

	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var sourceRequests atomic.Int32
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sourceRequests.Add(1)
				if r.Header.Get("X-Auth-Token") != "secret" {
					t.Error("source did not receive the token")
				}
				if r.URL.Path == "/" {
					http.Redirect(w, r, "/hop", status)
					return
				}
				http.Redirect(w, r, destinationURL, status)
			}))
			defer source.Close()

			client := &APIClient{token: "secret", artworkClient: &http.Client{Transport: transport, Timeout: artworkRequestTimeout}}
			body, err := client.fetchAuthenticatedURL(source.URL)
			if body != nil {
				_ = body.Close()
				t.Error("off-origin redirect returned a body")
			}
			if err == nil || !strings.Contains(err.Error(), "artwork redirect changes origin") {
				t.Errorf("error = %v, want origin rejection", err)
			}
			if got := sourceRequests.Load(); got != 2 {
				t.Errorf("source received %d requests, want two authenticated hops", got)
			}
			if got := destinationRequests.Load(); got != 0 {
				t.Errorf("destination received %d requests, want none", got)
			}
		})
	}
}

func TestSameArtworkOrigin(t *testing.T) {
	for _, test := range []struct {
		name, initial, next string
		want                bool
	}{
		{"path and query", "https://example.com/a", "https://example.com/b?q=1", true},
		{"hostname case", "https://EXAMPLE.com/a", "https://example.COM/b", true},
		{"http default port", "http://example.com", "http://example.com:80", true},
		{"https default port", "https://example.com:443", "https://example.com", true},
		{"ipv6 default port", "http://[::1]", "http://[::1]:80/a", true},
		{"host", "https://example.com", "https://other.example", false},
		{"subdomain", "https://example.com", "https://art.example.com", false},
		{"host suffix", "https://example.com", "https://example.com.evil", false},
		{"port", "https://example.com:443", "https://example.com:8443", false},
		{"downgrade", "https://example.com:443", "http://example.com:443", false},
		{"upgrade", "http://example.com:80", "https://example.com:80", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			initial, err := url.Parse(test.initial)
			if err != nil {
				t.Fatal(err)
			}
			next, err := url.Parse(test.next)
			if err != nil {
				t.Fatal(err)
			}
			if got := sameArtworkOrigin(initial, next); got != test.want {
				t.Errorf("sameArtworkOrigin(%q, %q) = %v, want %v", test.initial, test.next, got, test.want)
			}
		})
	}
}

func TestFetchAuthenticatedURLRejectsCrossPortRedirect(t *testing.T) {
	var destinationRequests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		destinationRequests.Add(1)
		_, _ = w.Write([]byte("payload"))
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth-Token") != "secret" {
			t.Error("source did not receive the token")
		}
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer source.Close()

	configured, err := NewAPIClient(source.URL, "secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	empty := NewEmptyAPIClient(nil)
	empty.token = "secret"
	for name, client := range map[string]*APIClient{
		"configured": configured,
		"empty":      empty,
		"fallback":   {token: "secret"},
		"injected":   {token: "secret", artworkClient: &http.Client{Timeout: artworkRequestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}},
	} {
		t.Run(name, func(t *testing.T) {
			body, err := client.fetchAuthenticatedURL(source.URL)
			if body != nil {
				_ = body.Close()
				t.Error("cross-port redirect returned a body")
			}
			if err == nil || !strings.Contains(err.Error(), "artwork redirect changes origin") {
				t.Errorf("error = %v, want origin rejection", err)
			}
			if got := destinationRequests.Load(); got != 0 {
				t.Errorf("destination received %d requests, want none", got)
			}
		})
	}
}

func TestFetchAuthenticatedURLTLSRedirects(t *testing.T) {
	var destinationRequests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		destinationRequests.Add(1)
		_, _ = w.Write([]byte("payload"))
	}))
	defer destination.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth-Token") != "secret" {
			t.Error("TLS source did not receive the token")
		}
		switch r.URL.Path {
		case "/same":
			http.Redirect(w, r, "/payload", http.StatusFound)
		case "/downgrade":
			http.Redirect(w, r, destination.URL, http.StatusFound)
		default:
			_, _ = w.Write([]byte("payload"))
		}
	}))
	defer source.Close()
	client := &APIClient{token: "secret", artworkClient: source.Client()}
	client.artworkClient.Timeout = artworkRequestTimeout

	body, err := client.fetchAuthenticatedURL(source.URL + "/same")
	if err != nil {
		t.Fatalf("same-origin TLS redirect: %v", err)
	}
	payload, readErr := io.ReadAll(body)
	_ = body.Close()
	if readErr != nil || string(payload) != "payload" {
		t.Fatalf("same-origin TLS payload = %q, error = %v", payload, readErr)
	}
	body, err = client.fetchAuthenticatedURL(source.URL + "/downgrade")
	if body != nil {
		_ = body.Close()
		t.Error("TLS downgrade returned a body")
	}
	if err == nil || !strings.Contains(err.Error(), "artwork redirect changes origin") {
		t.Errorf("error = %v, want downgrade rejection", err)
	}
	if got := destinationRequests.Load(); got != 0 {
		t.Errorf("HTTP destination received %d requests, want none", got)
	}
}

func TestFetchAuthenticatedURLPreservesRedirectPolicy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/payload", http.StatusFound)
	}))
	defer server.Close()
	policyError := errors.New("custom redirect rejection")
	client := &APIClient{artworkClient: &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return policyError },
		Timeout:       artworkRequestTimeout,
	}}
	body, err := client.fetchAuthenticatedURL(server.URL)
	if body != nil {
		_ = body.Close()
		t.Error("rejected redirect returned a body")
	}
	if !errors.Is(err, policyError) {
		t.Fatalf("error = %v, want custom rejection", err)
	}
}

func TestFetchAuthenticatedURLChecksCallbackModifiedOrigin(t *testing.T) {
	var destinationRequests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		destinationRequests.Add(1)
		_, _ = w.Write([]byte("payload"))
	}))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/payload", http.StatusFound)
	}))
	defer server.Close()
	client := &APIClient{token: "secret", artworkClient: &http.Client{
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			target, err := url.Parse(destination.URL)
			if err != nil {
				return err
			}
			next.URL = target
			via[0].URL = target
			return nil
		},
		Timeout: artworkRequestTimeout,
	}}
	body, err := client.fetchAuthenticatedURL(server.URL)
	if body != nil {
		_ = body.Close()
		t.Error("callback-modified redirect returned a body")
	}
	if err == nil || !strings.Contains(err.Error(), "artwork redirect changes origin") {
		t.Errorf("error = %v, want origin rejection", err)
	}
	if got := destinationRequests.Load(); got != 0 {
		t.Errorf("destination received %d requests, want none", got)
	}
}

func TestFetchAuthenticatedURLLimitsRedirects(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, "/", http.StatusFound)
	}))
	defer server.Close()
	body, err := (&APIClient{}).fetchAuthenticatedURL(server.URL)
	if body != nil {
		_ = body.Close()
		t.Error("redirect loop returned a body")
	}
	if err == nil || !strings.Contains(err.Error(), "stopped after 10 redirects") {
		t.Errorf("error = %v, want redirect limit", err)
	}
	if got := requests.Load(); got != 10 {
		t.Errorf("requests = %d, want 10", got)
	}
}

func TestFetchAuthenticatedURLConcurrentRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth-Token") != "secret" {
			t.Error("same-origin request did not receive the token")
		}
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/payload", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("payload"))
	}))
	defer server.Close()
	client := &APIClient{token: "secret", artworkClient: &http.Client{Timeout: artworkRequestTimeout}}
	results := make(chan error, 8)
	for range cap(results) {
		go func() {
			body, err := client.fetchAuthenticatedURL(server.URL)
			if err == nil {
				var payload []byte
				payload, err = io.ReadAll(body)
				_ = body.Close()
				if err == nil && string(payload) != "payload" {
					err = fmt.Errorf("payload = %q, want payload", payload)
				}
			}
			results <- err
		}()
	}
	for range cap(results) {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	if client.artworkClient.CheckRedirect != nil || client.artworkClient.Timeout != artworkRequestTimeout {
		t.Error("shared client configuration was modified")
	}
}

func TestFetchImageFileRedirects(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	var destinationRequests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		destinationRequests.Add(1)
		_, _ = w.Write(encoded.Bytes())
	}))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth-Token") != "secret" {
			t.Error("artwork request did not receive the token")
		}
		switch r.URL.Path {
		case "/movies/tt001/files/same.png":
			http.Redirect(w, r, "/image.png", http.StatusFound)
		case "/movies/tt001/files/off.png":
			http.Redirect(w, r, destination.URL, http.StatusFound)
		default:
			_, _ = w.Write(encoded.Bytes())
		}
	}))
	defer server.Close()
	client, err := NewAPIClient(server.URL, "secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	movie := &papi.Movie{Movie: &polochon.Movie{ImdbID: "tt001"}}
	for _, name := range []string{"direct.png", "same.png", "off.png"} {
		t.Run(name, func(t *testing.T) {
			file := papi.NewFile(&index.File{Name: name}, movie)
			img, err := client.fetchImageFile(file)
			if name == "off.png" {
				if img != nil || err == nil || !strings.Contains(err.Error(), "image fetch:") || !strings.Contains(err.Error(), "artwork redirect changes origin") {
					t.Fatalf("off-origin image = %v, error = %v, want fetch rejection", img, err)
				}
				return
			}
			if err != nil || img == nil || img.Bounds().Size() != image.Pt(1, 1) {
				t.Fatalf("image = %v, error = %v, want decoded 1x1 PNG", img, err)
			}
		})
	}
	if got := destinationRequests.Load(); got != 0 {
		t.Errorf("destination received %d requests, want none", got)
	}
}
