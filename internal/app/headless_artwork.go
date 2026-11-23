package app

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"net/http"
	"path"
	"sync"
	"time"
)

// ── Generated artwork for the offline catalog ─────────────────────────────────
//
// The mock catalog's artwork is drawn on request and served by an in-process
// transport, so it goes through the real download, decode, resize and cache
// path. Each scenario targets a rendering or loading case.

// mockPattern colours pixel x, y of a w×h image.
type mockPattern func(x, y, w, h int) color.NRGBA

// mockArtwork describes one generated image file and how it is served.
type mockArtwork struct {
	pattern   mockPattern
	size      image.Point
	delay     time.Duration // before each response
	transient int           // 503 responses before the image is served
	corrupt   bool          // truncated PNG
}

var (
	mockFanartSize = image.Pt(1920, 1080)
	mockPosterSize = image.Pt(680, 1000)
)

func mockFanart(p mockPattern) *mockArtwork { return &mockArtwork{pattern: p, size: mockFanartSize} }
func mockPoster(p mockPattern) *mockArtwork { return &mockArtwork{pattern: p, size: mockPosterSize} }

func renderMockArtwork(art *mockArtwork) []byte {
	w, h := art.size.X, art.size.Y
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, art.pattern(x, y, w, h))
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err) // encoding an in-memory NRGBA image cannot fail
	}
	data := buf.Bytes()
	if art.corrupt {
		data = data[:min(len(data), 64)]
	}
	return data
}

// mockArtworkTransport serves the files in arts by name, whatever the host
// and directory.
type mockArtworkTransport struct {
	arts    map[string]*mockArtwork
	encoded map[string]func() []byte // renders each file once, on first success
	link    *mockLink                // nil serves bodies at once

	mu       sync.Mutex
	attempts map[string]int
	sent     map[string]int64 // body bytes read, by file
	complete map[string]int   // bodies read to the end, by file
}

func newMockArtworkTransport(arts map[string]*mockArtwork) *mockArtworkTransport {
	encoded := make(map[string]func() []byte, len(arts))
	for name, art := range arts {
		encoded[name] = sync.OnceValue(func() []byte { return renderMockArtwork(art) })
	}
	return &mockArtworkTransport{arts: arts, encoded: encoded, attempts: make(map[string]int)}
}

func (t *mockArtworkTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		_ = req.Body.Close()
	}
	name := path.Base(req.URL.Path)
	art, ok := t.arts[name]
	if !ok {
		return mockResponse(req, http.StatusNotFound, nil), nil
	}
	if art.delay > 0 {
		timer := time.NewTimer(art.delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}

	t.mu.Lock()
	attempt := t.attempts[name]
	t.attempts[name]++
	t.mu.Unlock()
	if attempt < art.transient {
		return mockResponse(req, http.StatusServiceUnavailable, nil), nil
	}

	resp := mockResponse(req, http.StatusOK, t.encoded[name]())
	if t.link != nil {
		if err := t.link.wait(req, t.link.latency); err != nil {
			return nil, err
		}
	}
	resp.Body = &mockLinkBody{t: t, name: name, req: req, r: resp.Body}
	return resp, nil
}

// mockLink models the network between pépé and Polochon: a round-trip
// latency before each response, and one bandwidth shared by every body in
// flight, as on a home uplink or Wi-Fi.
type mockLink struct {
	latency     time.Duration
	bytesPerSec float64

	mu   sync.Mutex
	free time.Time // when the link has sent everything queued so far
}

// mockLinkChunk is how much a body sends per turn, so concurrent bodies
// interleave on the link.
const mockLinkChunk = 16 << 10

// reserve queues n bytes on the link and returns when they have arrived.
func (l *mockLink) reserve(n int) time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	start := l.free
	if start.Before(now) {
		start = now
	}
	l.free = start.Add(time.Duration(float64(n) / l.bytesPerSec * float64(time.Second)))
	return l.free
}

func (l *mockLink) wait(req *http.Request, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-req.Context().Done():
		return req.Context().Err()
	}
}

// mockLinkBody sends a response body over the transport's link, if any, and
// counts what was read.
type mockLinkBody struct {
	t    *mockArtworkTransport
	name string
	req  *http.Request
	r    io.ReadCloser
}

func (b *mockLinkBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p[:min(len(p), mockLinkChunk)])
	if link := b.t.link; n > 0 && link != nil {
		if werr := link.wait(b.req, time.Until(link.reserve(n))); werr != nil {
			return 0, werr
		}
	}
	b.t.mu.Lock()
	if b.t.sent == nil {
		b.t.sent, b.t.complete = make(map[string]int64), make(map[string]int)
	}
	b.t.sent[b.name] += int64(n)
	if err == io.EOF {
		b.t.complete[b.name]++
	}
	b.t.mu.Unlock()
	return n, err
}

func (b *mockLinkBody) Close() error { return b.r.Close() }

func mockResponse(req *http.Request, status int, body []byte) *http.Response {
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		StatusCode:    status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{"Content-Type": {"image/png"}},
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

// ── Patterns ──────────────────────────────────────────────────────────────────

func uv(x, y, w, h int) (float64, float64) {
	return (float64(x) + 0.5) / float64(w), (float64(y) + 0.5) / float64(h)
}

func mixNRGBA(a, b color.NRGBA, t float64) color.NRGBA {
	t = min(max(t, 0), 1)
	lerp := func(a, b uint8) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*t + 0.5) }
	return color.NRGBA{R: lerp(a.R, b.R), G: lerp(a.G, b.G), B: lerp(a.B, b.B), A: 0xff}
}

// hsv converts hue (turns), saturation and value in [0, 1] to a colour.
func hsv(hue, s, v float64) color.NRGBA {
	hue = (hue - math.Floor(hue)) * 6
	c := v * s
	x := c * (1 - math.Abs(math.Mod(hue, 2)-1))
	var r, g, b float64
	switch int(hue) {
	case 0:
		r, g = c, x
	case 1:
		r, g = x, c
	case 2:
		g, b = c, x
	case 3:
		g, b = x, c
	case 4:
		r, b = x, c
	default:
		r, b = c, x
	}
	m := v - c
	to8 := func(f float64) uint8 { return uint8(min(max((f+m)*255+0.5, 0), 255)) }
	return color.NRGBA{R: to8(r), G: to8(g), B: to8(b), A: 0xff}
}

func solidPattern(c color.NRGBA) mockPattern {
	return func(int, int, int, int) color.NRGBA { return c }
}

// sunsetPattern is a warm sky with a bright sun over a dark ground.
func sunsetPattern(x, y, w, h int) color.NRGBA {
	u, v := uv(x, y, w, h)
	sky := mixNRGBA(color.NRGBA{R: 0x3a, G: 0x1c, B: 0x5c}, color.NRGBA{R: 0xff, G: 0x8a, B: 0x3d}, v/0.72)
	if d := math.Hypot((u-0.68)*float64(w)/float64(h), v-0.56); d < 0.11 {
		sky = color.NRGBA{R: 0xff, G: 0xf0, B: 0xb0, A: 0xff}
	} else if d < 0.3 {
		sky = mixNRGBA(color.NRGBA{R: 0xff, G: 0xd0, B: 0x80}, sky, (d-0.11)/0.19)
	}
	if ground := 0.72 + 0.03*math.Sin(u*9); v > ground {
		return mixNRGBA(color.NRGBA{R: 0x2a, G: 0x14, B: 0x20}, color.NRGBA{R: 0x0c, G: 0x06, B: 0x0a}, (v-ground)/0.2)
	}
	return sky
}

// oceanPattern is a pale sky over deep water with lighter wave crests.
func oceanPattern(x, y, w, h int) color.NRGBA {
	u, v := uv(x, y, w, h)
	if v < 0.4 {
		return mixNRGBA(color.NRGBA{R: 0xd8, G: 0xf0, B: 0xff}, color.NRGBA{R: 0x7c, G: 0xc4, B: 0xe8}, v/0.4)
	}
	water := mixNRGBA(color.NRGBA{R: 0x1a, G: 0x6a, B: 0x9a}, color.NRGBA{R: 0x04, G: 0x18, B: 0x34}, (v-0.4)/0.6)
	crest := math.Sin(u*40+v*v*120) * math.Sin(u*7-v*30)
	return mixNRGBA(water, color.NRGBA{R: 0xc8, G: 0xe8, B: 0xf8}, (crest-0.6)*1.5)
}

// horizonPattern is dark at the top and bright at the bottom, where the
// browse preview's text sits.
func horizonPattern(x, y, w, h int) color.NRGBA {
	_, v := uv(x, y, w, h)
	return mixNRGBA(color.NRGBA{R: 0x10, G: 0x14, B: 0x20}, color.NRGBA{R: 0xff, G: 0xfa, B: 0xf0}, (v-0.45)/0.3)
}

func checkerPattern(cell int) mockPattern {
	return func(x, y, _, _ int) color.NRGBA {
		if (x/cell+y/cell)%2 == 0 {
			return color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
		}
		return color.NRGBA{A: 0xff}
	}
}

// hueStripesPattern draws vertical bars of saturated hues.
func hueStripesPattern(bars int) mockPattern {
	return func(x, y, w, h int) color.NRGBA {
		u, _ := uv(x, y, w, h)
		return hsv(math.Floor(u*float64(bars))/float64(bars), 0.9, 1)
	}
}

// gradientPattern is a slow diagonal ramp that shows any banding.
func gradientPattern(x, y, w, h int) color.NRGBA {
	u, v := uv(x, y, w, h)
	return mixNRGBA(color.NRGBA{R: 0x04, G: 0x08, B: 0x10}, color.NRGBA{R: 0x30, G: 0x80, B: 0x88}, (u+v)/2)
}

// neonPattern scatters bright magenta, cyan and violet glows on black.
func neonPattern(x, y, w, h int) color.NRGBA {
	u, v := uv(x, y, w, h)
	aspect := float64(w) / float64(h)
	c := color.NRGBA{R: 0x06, G: 0x02, B: 0x0c, A: 0xff}
	for _, glow := range []struct {
		u, v, r float64
		col     color.NRGBA
	}{
		{0.3, 0.35, 0.35, color.NRGBA{R: 0xff, G: 0x2e, B: 0xb8}},
		{0.75, 0.3, 0.3, color.NRGBA{R: 0x20, G: 0xe8, B: 0xff}},
		{0.6, 0.8, 0.28, color.NRGBA{R: 0x9a, G: 0x4c, B: 0xff}},
	} {
		d := math.Hypot((u-glow.u)*aspect, v-glow.v) / glow.r
		c = mixNRGBA(c, glow.col, 1-d*d)
	}
	return c
}

// nightPattern is a very dark sky with sparse stars.
func nightPattern(x, y, w, h int) color.NRGBA {
	_, v := uv(x, y, w, h)
	c := mixNRGBA(color.NRGBA{R: 0x02, G: 0x03, B: 0x0a}, color.NRGBA{R: 0x0c, G: 0x14, B: 0x2c}, v)
	if hash := (uint32(x)*73856093 ^ uint32(y)*19349663) % 4096; hash < 3 {
		return color.NRGBA{R: 0xe8, G: 0xec, B: 0xff, A: 0xff}
	}
	return c
}
