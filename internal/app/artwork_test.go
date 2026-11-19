package app

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/unit"
	index "github.com/odwrtw/polochon/lib/media_index"
	"github.com/odwrtw/polochon/lib/papi"
)

func waitForArtwork(t *testing.T, a *App, completed <-chan struct{}) {
	t.Helper()
	// Wait for onDone, not merely the cached result: logging and the UI wakeup
	// must both finish before assertions read the output buffer.
	waitForCache(t, func() bool { return len(completed) > 0 })
	<-completed
	a.images.drain()
}

func artworkContext() C {
	return C{
		Ops:         new(op.Ops),
		Constraints: layout.Exact(image.Pt(300, 450)),
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
	}
}

func TestArtworkClientAndRendering(t *testing.T) {
	var pngData, jpegData bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	if err := png.Encode(&pngData, img); err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(&jpegData, img, nil); err != nil {
		t.Fatal(err)
	}
	for _, art := range []struct {
		key    string
		render func(*App, C) D
	}{
		{"thumb:tt001", func(a *App, gtx C) D { return a.movieThumb(gtx, "tt001", 300, 0) }},
		{"poster:tt001", func(a *App, gtx C) D { return a.showPoster(gtx, "tt001", 300, 0) }},
		{"fanart:tt001", func(a *App, gtx C) D { return a.movieFanart(gtx, "tt001") }},
		{"fanart:tt002", func(a *App, gtx C) D { return a.showFanart(gtx, "tt002") }},
	} {
		for _, tc := range []struct {
			name, want string
			status     int
			payload    []byte
			redirect   string
		}{
			{name: "missing media", want: "media not found"},
			{name: "missing metadata", want: "image file not set"},
			{name: "unauthorized", status: 401, want: "401 Unauthorized"},
			{name: "not found", status: 404, want: "404 Not Found"},
			{name: "unsupported", payload: []byte("not an image"), want: "unsupported image format"},
			{name: "corrupt", payload: pngData.Bytes()[:40], want: "artwork decode failed"},
			{name: "off-origin redirect", redirect: "https://user:password@example.test/art?token=private-token", want: "artwork redirect changes origin"},
			{name: "malformed redirect", redirect: "http://user:password@example.test/%zz?token=private-token", want: "artwork request failed"},
			{name: "PNG", payload: pngData.Bytes()},
			{name: "JPEG", payload: jpegData.Bytes()},
		} {
			t.Run(art.key+"/"+tc.name, func(t *testing.T) {
				var output bytes.Buffer
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("X-Auth-Token") != "private-token" {
						t.Error("artwork request did not receive the token")
					}
					if tc.redirect != "" {
						w.Header().Set("Location", tc.redirect)
						w.WriteHeader(http.StatusFound)
						return
					}
					if tc.status != 0 {
						w.WriteHeader(tc.status)
					}
					_, _ = w.Write(tc.payload)
				}))
				defer server.Close()
				// Use the empty constructor to avoid unrelated HTTP startup warnings.
				client := NewEmptyAPIClient(slog.New(slog.NewTextHandler(&output, nil)))
				api, err := papi.New(server.URL)
				if err != nil {
					t.Fatal(err)
				}
				client.client, client.token = api, "private-token"
				if tc.name != "missing media" {
					movie := testMovie("tt001", "Movie", 2026)
					show := testShow("tt001", "Show", 2026)
					show2 := testShow("tt002", "Show two", 2026)
					if tc.name != "missing metadata" {
						file := &index.File{Name: "art.png"}
						movie.ThumbFile, movie.FanartFile = papi.NewFile(file, movie), papi.NewFile(file, movie)
						show.PosterFile = papi.NewFile(file, show)
						show2.FanartFile = papi.NewFile(file, show2)
					}
					client.movies["tt001"] = movie
					client.shows["tt001"], client.shows["tt002"] = show, show2
				}
				a := &App{client: client, images: newAsyncCache[paint.ImageOp](1, 0)}
				completed := make(chan struct{}, 1)
				a.images.onDone = func() { completed <- struct{}{} }
				if dims := art.render(a, artworkContext()); dims.Size != image.Pt(300, 450) {
					t.Fatalf("loading artwork changed layout size: %v", dims.Size)
				}
				waitForArtwork(t, a, completed)
				cached, ok := a.images.get(art.key)
				wantSize := image.Pt(2, 3)
				if tc.want != "" {
					wantSize = image.Point{}
				}
				if !ok || cached.Size() != wantSize {
					t.Fatalf("cached image = (%v, %v), want %v", cached.Size(), ok, wantSize)
				}
				for range 3 {
					if dims := art.render(a, artworkContext()); dims.Size != image.Pt(300, 450) {
						t.Fatalf("resolved artwork changed layout size: %v", dims.Size)
					}
				}
				if a.images.active != 0 {
					t.Fatal("resolved artwork was fetched again")
				}
				if tc.want == "" {
					if output.Len() != 0 {
						t.Fatalf("successful artwork logged: %s", &output)
					}
					return
				}
				log := output.String()
				if strings.Count(log, "failed to load artwork") != 1 || !strings.Contains(log, "level=WARN") || !strings.Contains(log, "key="+art.key) || !strings.Contains(log, tc.want) {
					t.Fatalf("unexpected artwork warning: %s", log)
				}
				for _, secret := range []string{server.URL, "http://", "https://", "password", "private-token", "%zz"} {
					if strings.Contains(log, secret) {
						t.Errorf("request data leaked in log: %s", log)
					}
				}
			})
		}
	}
}

func TestArtworkUnknownErrorsNeverLogTheirText(t *testing.T) {
	secret := "https://user:password@example.test/art?token=private-token"
	requestErr := &url.Error{Op: "Get", URL: secret, Err: errors.New("failed fetching " + secret)}
	for _, err := range []error{
		errors.New(secret),
		fmt.Errorf("image fetch: %w", requestErr),
		&url.Error{Op: "Get", URL: secret, Err: requestErr},
		errors.Join(errors.New(secret), requestErr),
	} {
		var output bytes.Buffer
		a := &App{
			client: NewEmptyAPIClient(slog.New(slog.NewTextHandler(&output, nil))),
			images: newAsyncCache[paint.ImageOp](1, 0),
		}
		completed := make(chan struct{}, 1)
		a.images.onDone = func() { completed <- struct{}{} }
		a.fetchArtwork("thumb:tt001", func() (image.Image, error) { return nil, err }, posterMaxSize.X, posterMaxSize.Y)
		waitForArtwork(t, a, completed)
		if got := output.String(); !strings.Contains(got, `error="artwork load failed"`) || strings.Contains(got, "https://") || strings.Contains(got, "private-token") || strings.Contains(got, "password") {
			t.Fatalf("unsafe or missing fallback diagnostic: %s", got)
		}
	}
}

func TestArtworkRetriesOnlyAfterSuccessfulRefresh(t *testing.T) {
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 3))); err != nil {
		t.Fatal(err)
	}
	requests := make(chan struct{}, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests <- struct{}{}
		if len(requests) == 1 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(data.Bytes())
	}))
	defer server.Close()
	var output bytes.Buffer
	client := NewEmptyAPIClient(slog.New(slog.NewTextHandler(&output, nil)))
	api, err := papi.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client.client = api
	movie := testMovie("tt001", "Movie", 2026)
	movie.ThumbFile = papi.NewFile(&index.File{Name: "art.png"}, movie)
	client.movies["tt001"] = movie
	a := New(client, nil, nil, nil, nil)
	completed := make(chan struct{}, 1)
	a.images.onDone = func() { completed <- struct{}{} }
	a.movieThumb(artworkContext(), "tt001", 300, 0)
	waitForArtwork(t, a, completed)
	for _, refreshErr := range []error{errors.New("offline"), nil} {
		a.refreshDone = make(chan refreshResult, 1)
		a.refreshDone <- refreshResult{snapshot: client.getSnapshot(), err: refreshErr}
		a.drainRefresh()
		a.movieThumb(artworkContext(), "tt001", 300, 0)
		if refreshErr != nil {
			if a.images.active != 0 || len(requests) != 1 {
				t.Fatal("failed library refresh retried artwork")
			}
			continue
		}
		waitForArtwork(t, a, completed)
		cached, ok := a.images.get("thumb:tt001")
		if !ok || cached.Size() != image.Pt(2, 3) || len(requests) != 2 {
			t.Fatal("successful library refresh did not recover artwork")
		}
	}
	if strings.Count(output.String(), "failed to load artwork") != 1 {
		t.Fatalf("artwork was logged again after recovery: %s", &output)
	}
}
