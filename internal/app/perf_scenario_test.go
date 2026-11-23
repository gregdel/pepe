package app

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	polochon "github.com/odwrtw/polochon/lib"
	"github.com/odwrtw/polochon/lib/papi"

	"github.com/gregdel/pepe/internal/driver"
)

// ── Realistic catalog ─────────────────────────────────────────────────────────
//
// Polochon serves TMDB "original" artwork. Sampled from a real server
// (TestPerfRealArtwork, 2207 movies): movie posters are 1000×1500 or
// 2000×3000 JPEGs (median 386 KiB), movie fanart 1920×1080 or 3840×2160 in
// about equal numbers, sometimes 1280×720 (median 323 KiB, 4K up to 2.3 MiB).
// A pool of distinct images in those proportions is served under per-title
// names, so every title is downloaded and decoded.

type perfArtPool struct{ fanart, poster [][]byte }

var perfPool = func() func(testing.TB) perfArtPool {
	var pool *perfArtPool
	return func(tb testing.TB) perfArtPool {
		if pool == nil {
			p := perfArtPool{}
			for i, size := range []image.Point{{1920, 1080}, {3840, 2160}, {1920, 1080}, {3840, 2160}, {1280, 720}} {
				p.fanart = append(p.fanart, photoJPEGSeed(tb, size.X, size.Y, uint64(i)))
			}
			for i, size := range []image.Point{{1000, 1500}, {2000, 3000}, {1000, 1500}, {2000, 3000}, {1000, 1500}} {
				p.poster = append(p.poster, photoJPEGSeed(tb, size.X, size.Y, uint64(10+i)))
			}
			pool = &p
		}
		return *pool
	}
}()

var perfWords = strings.Fields(`amber atlas autumn black blue broken burning city cold crimson dark dawn
deep desert distant dream eastern echo empire falling final forgotten frozen ghost glass golden
green hidden hollow iron last lost midnight mirror moon night northern ocean pale paper quiet red
river road salt secret shadow silent silver small stone storm summer sun tide twisted velvet
white wild winter wolf`)

var perfTransport *mockArtworkTransport

// newPerfClient builds a catalog of n movies whose artwork crosses link.
func newPerfClient(tb testing.TB, n int, link *mockLink) *APIClient {
	tb.Helper()
	pool := perfPool(tb)
	client, err := NewAPIClient("http://127.0.0.1", "perf", slog.New(slog.DiscardHandler))
	if err != nil {
		tb.Fatal(err)
	}
	arts := make(map[string]*mockArtwork)
	encoded := make(map[string]func() []byte)
	serve := func(id, kind string, data []byte, owner papi.Resource) *papi.File {
		encoded[id+"-"+kind+".png"] = func() []byte { return data }
		return mockFile(arts, id, kind, &mockArtwork{}, owner)
	}
	rng := rand.New(rand.NewPCG(7, 7))
	movies := make(map[string]*papi.Movie)
	var items []*listingItem
	for i := range n {
		id := fmt.Sprintf("tt%07d", i)
		title := perfWords[rng.IntN(len(perfWords))] + " " + perfWords[rng.IntN(len(perfWords))]
		title = strings.ToUpper(title[:1]) + title[1:]
		m := &papi.Movie{Movie: &polochon.Movie{
			BaseVideo: mockVideo(2_000_000_000, "1080p", mockDateAdded(i)),
			ImdbID:    id, Title: title, Year: 1980 + i%45, Plot: "A perf fixture title.", Runtime: 100,
		}}
		m.FanartFile = serve(id, "fanart", pool.fanart[rng.IntN(len(pool.fanart))], m)
		m.ThumbFile = serve(id, "poster", pool.poster[rng.IntN(len(pool.poster))], m)
		movies[id] = m
		items = append(items, newMovieListingItem(m))
	}
	perfTransport = &mockArtworkTransport{arts: arts, encoded: encoded, attempts: make(map[string]int), link: link}
	client.artworkClient = &http.Client{Timeout: artworkRequestTimeout, Transport: perfTransport}
	client.publishLibrary(movies, map[string]*papi.Show{}, items, nil, buildLibrarySnapshot(radixListingBuilder{}, items, nil))
	return client
}

// ── Scenario ──────────────────────────────────────────────────────────────────

type perfFrame struct {
	phase              string
	at                 time.Duration // since the scenario started
	late               time.Duration // start after its vsync tick
	layout, frame      time.Duration
	motion, fetching   bool
	key                bool
	heapMB             float64
	backdropKey, wantd string
	rows, rowsMissing  int     // visible title rows, and those still without a poster
	previewPoster      bool    // the selected title's preview poster is resolved
	uploadMB           float64 // full-size textures newly drawn this frame
}

// perfVisiblePosters counts the title rows on screen and those whose row
// poster is not resolved yet.
func perfVisiblePosters(a *App) (rows, missing int, preview bool) {
	node := a.browse.nav.getCurrentNode()
	if a.browse.nav.isAtRoot() || node == nil {
		return 0, 0, false
	}
	pos := a.leftList.Position
	for i := pos.First; i < pos.First+pos.Count && i < len(node.Children); i++ {
		media := a.mediaForNode(node.Children[i])
		if media == nil {
			continue
		}
		rows++
		if _, ok := a.thumbs.cache["rowthumb:"+media.ID]; !ok {
			missing++
		}
	}
	if media := a.selectedBrowseMedia(); media != nil {
		_, preview = a.images.cache["thumb:"+media.ID]
	}
	return rows, missing, preview
}

type perfStep struct {
	phase string
	dur   time.Duration
	keys  []string      // pressed in turn, one per every interval
	every time.Duration // 0: no keys
}

// TestPerfScenario drives the real UI at 60 Hz against a slow network and
// reports frame costs per phase. It is opt-in: PEPE_PERF=1. PEPE_PERF_MBPS
// and PEPE_PERF_LATENCY_MS set the link (default 400 Mbit/s, 50 ms, as
// measured against a home server by TestPerfRealArtwork);
// PEPE_PERF_CSV writes every frame.
func TestPerfScenario(t *testing.T) {
	if os.Getenv("PEPE_PERF") == "" {
		t.Skip("set PEPE_PERF=1")
	}
	mbps := envFloat("PEPE_PERF_MBPS", 400)
	latency := time.Duration(envFloat("PEPE_PERF_LATENCY_MS", 50)) * time.Millisecond
	link := &mockLink{latency: latency, bytesPerSec: mbps * 1e6 / 8}
	client := newPerfClient(t, 400, link)
	a := New(client, nil, &Config{MPVPath: "mpv"}, nil, nil)
	defer a.Close()

	var layoutDur time.Duration
	d, err := driver.New(driver.Config{Width: 1920, Height: 1080, PxPerDp: 1, PxPerSp: 1}, func(gtx C) D {
		start := time.Now()
		dims := a.Layout(gtx)
		layoutDur = time.Since(start)
		return dims
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// Home → mode rail → Last added → content: a flat, newest-first list of
	// titles, each with a row poster and sharp fanart.
	for _, k := range []string{"LeftArrow", "DownArrow", "RightArrow", "RightArrow"} {
		if err := d.Key(k, nil); err != nil {
			t.Fatal(err)
		}
	}

	steps := []perfStep{
		{phase: "1-initial-load", dur: 4 * time.Second},
		{phase: "2-hold-down-20hz", dur: 3 * time.Second, keys: []string{"DownArrow"}, every: 50 * time.Millisecond},
		{phase: "3-settle", dur: 5 * time.Second},
		{phase: "4-step-down-2.5hz", dur: 6 * time.Second, keys: []string{"DownArrow"}, every: 400 * time.Millisecond},
		{phase: "5-settle", dur: 4 * time.Second},
		{phase: "6-hold-up-20hz", dur: 3 * time.Second, keys: []string{"UpArrow"}, every: 50 * time.Millisecond},
		{phase: "7-settle", dur: 4 * time.Second},
	}

	// Gio frees a texture not drawn in the previous frame, so a newly drawn
	// backdrop or preview poster is uploaded again even when cached.
	var lastBackdrop, lastPoster string
	uploads := func() float64 {
		mb := 0.0
		if st := a.browse.backdrop; st.cur.key != lastBackdrop && st.cur.key != st.prev.key {
			if sz := st.curArt.sharp.Size(); st.cur.sharp && sz.X > 0 {
				mb += float64(sz.X*sz.Y*4) / (1 << 20)
			}
		}
		lastBackdrop = a.browse.backdrop.cur.key
		poster := ""
		if m := a.selectedBrowseMedia(); m != nil {
			if img, ok := a.images.cache["thumb:"+m.ID]; ok && img.Size().X > 0 {
				poster = m.ID
				if poster != lastPoster {
					mb += float64(img.Size().X*img.Size().Y*4) / (1 << 20)
				}
			}
		}
		lastPoster = poster
		return mb
	}

	const vsync = time.Second / 60
	var frames []perfFrame
	var mem runtime.MemStats
	runtime.GC()
	gcBefore := readGCPauses()
	begin := time.Now()
	tick := begin
	for _, step := range steps {
		end := tick.Add(step.dur)
		nextKey, ki := tick, 0
		for tick.Before(end) {
			if wait := time.Until(tick); wait > 0 {
				time.Sleep(wait)
			}
			start := time.Now()
			pressed := false
			if step.every > 0 && !start.Before(nextKey) {
				pressed = true
				nextKey = nextKey.Add(step.every)
				err = d.Key(step.keys[ki%len(step.keys)], nil)
				ki++
			} else {
				err = d.Frame()
			}
			if err != nil {
				t.Fatal(err)
			}
			total := time.Since(start)
			runtime.ReadMemStats(&mem)
			want := ""
			if m, _ := a.wantedBackdrop(); m != nil {
				want = "fanart:" + m.ID
			}
			rows, missing, preview := perfVisiblePosters(a)
			frames = append(frames, perfFrame{
				rows: rows, rowsMissing: missing, previewPoster: preview, uploadMB: uploads(),
				phase: step.phase, at: start.Sub(begin), late: start.Sub(tick),
				layout: layoutDur, frame: total, motion: a.inMotion,
				fetching: a.images.busy() || a.backdrops.busy(), key: pressed,
				heapMB: float64(mem.HeapAlloc) / (1 << 20), backdropKey: a.browse.backdrop.cur.key, wantd: want,
			})
			// The next tick is the first vsync after this frame, as a
			// blocking swap would give.
			for !tick.After(time.Now()) {
				tick = tick.Add(vsync)
			}
		}
	}
	gcAfter := readGCPauses()
	perfTransport.mu.Lock()
	// Per kind: files requested, requests, bytes read, files read to the end
	// more than once, and bytes read for bodies never completed (cancelled).
	type kindStats struct {
		files, requests, completeTwice int
		read, wasted                   int64
	}
	stats := map[string]*kindStats{}
	for name, n := range perfTransport.attempts {
		kind := "poster"
		if strings.Contains(name, "fanart") {
			kind = "fanart"
		}
		k := stats[kind]
		if k == nil {
			k = &kindStats{}
			stats[kind] = k
		}
		k.files++
		k.requests += n
		read := perfTransport.sent[name]
		k.read += read
		done := perfTransport.complete[name]
		if done > 1 {
			k.completeTwice++
		}
		if size := int64(len(perfTransport.encoded[name]())); read > int64(done)*size {
			k.wasted += read - int64(done)*size
		}
	}
	var total int64
	for _, kind := range []string{"fanart", "poster"} {
		if k := stats[kind]; k != nil {
			total += k.read
			t.Logf("  %s: %d files, %d requests, %d downloaded more than once, %.1f MB read, %.1f MB cancelled", kind, k.files, k.requests, k.completeTwice, float64(k.read)/1e6, float64(k.wasted)/1e6)
		}
	}
	t.Logf("network: %.1f MB read", float64(total)/1e6)
	perfTransport.mu.Unlock()
	if dir := os.Getenv("PEPE_PERF_SHOT"); dir != "" {
		if err := d.Screenshot(filepath.Join(dir, "end.png")); err != nil {
			t.Fatal(err)
		}
	}

	t.Logf("link %.0f Mbit/s, %v latency, GOMAXPROCS %d", mbps, latency, runtime.GOMAXPROCS(0))
	t.Logf("%-20s %6s %6s %6s %6s | %7s %7s %7s | %6s %6s %5s", "phase", "frames", "lay50", "lay99", "layMax", "frm50", "frm99", "frmMax", ">16ms", ">33ms", "late>8")
	byPhase := map[string][]perfFrame{}
	var order []string
	for _, f := range frames {
		if _, ok := byPhase[f.phase]; !ok {
			order = append(order, f.phase)
		}
		byPhase[f.phase] = append(byPhase[f.phase], f)
	}
	order = append(order, "all")
	byPhase["all"] = frames
	for _, ph := range order {
		fs := byPhase[ph]
		lay := durs(fs, func(f perfFrame) time.Duration { return f.layout })
		frm := durs(fs, func(f perfFrame) time.Duration { return f.frame })
		over16, over33, late := 0, 0, 0
		for _, f := range fs {
			if f.frame > vsync {
				over16++
			}
			if f.frame > 2*vsync {
				over33++
			}
			if f.late > 8*time.Millisecond {
				late++
			}
		}
		t.Logf("%-20s %6d %6s %6s %6s | %7s %7s %7s | %6d %6d %5d", ph, len(fs),
			ms1(pct(lay, 50)), ms1(pct(lay, 99)), ms1(pct(lay, 100)),
			ms1(pct(frm, 50)), ms1(pct(frm, 99)), ms1(pct(frm, 100)), over16, over33, late)
	}
	t.Logf("GC: %d cycles, pause total %v max %v", gcAfter.n-gcBefore.n, gcAfter.total-gcBefore.total, gcAfter.max)

	// Artwork latency: for each stretch where the cursor rests on one title,
	// how long until its backdrop shows, if it shows before the cursor moves.
	var waits []time.Duration
	stretches, shown := 0, 0
	for i := 0; i < len(frames); {
		j, want := i, frames[i].wantd
		for j < len(frames) && frames[j].wantd == want {
			j++
		}
		if want != "" && frames[j-1].at-frames[i].at >= 300*time.Millisecond {
			stretches++
			for k := i; k < j; k++ {
				if frames[k].backdropKey == want {
					shown++
					waits = append(waits, frames[k].at-frames[i].at)
					break
				}
			}
		}
		i = j
	}
	slices.Sort(waits)
	t.Logf("backdrop: shown in %d of %d rests ≥300ms; latency p50=%v p90=%v max=%v", shown, stretches,
		pct(waits, 50).Round(time.Millisecond), pct(waits, 90).Round(time.Millisecond), pct(waits, 100).Round(time.Millisecond))
	var rowFrames, rowMissing, previewMissing, terminalFrames int
	for _, f := range frames {
		rowFrames += f.rows
		rowMissing += f.rowsMissing
		if f.wantd != "" {
			terminalFrames++
			if !f.previewPoster {
				previewMissing++
			}
		}
	}
	if rowFrames > 0 && terminalFrames > 0 {
		t.Logf("posters: row placeholders %.0f%% of visible row-frames; preview poster missing %.0f%% of frames", 100*float64(rowMissing)/float64(rowFrames), 100*float64(previewMissing)/float64(terminalFrames))
	}
	for _, ph := range order[:len(order)-1] {
		fs := byPhase[ph]
		up, peak := 0.0, 0.0
		for _, f := range fs {
			up += f.uploadMB
			peak = max(peak, f.uploadMB)
		}
		if secs := float64(fs[len(fs)-1].at-fs[0].at) / float64(time.Second); secs > 0 {
			t.Logf("  %-20s texture uploads %5.0f MB/s, peak %4.1f MB in one frame", ph, up/secs, peak)
		}
	}
	for _, ph := range order[:len(order)-1] {
		var r, m int
		for _, f := range byPhase[ph] {
			r += f.rows
			m += f.rowsMissing
		}
		if r > 0 {
			t.Logf("  %-20s row placeholders %3.0f%%", ph, 100*float64(m)/float64(r))
		}
	}
	if path := os.Getenv("PEPE_PERF_CSV"); path != "" {
		var b strings.Builder
		b.WriteString("phase,at_ms,late_ms,layout_ms,frame_ms,motion,fetching,key,heap_mb,rows,rows_missing\n")
		for _, f := range frames {
			fmt.Fprintf(&b, "%s,%.1f,%.2f,%.3f,%.3f,%t,%t,%t,%.1f,%d,%d\n", f.phase, ms(f.at), ms(f.late), ms(f.layout), ms(f.frame), f.motion, f.fetching, f.key, f.heapMB, f.rows, f.rowsMissing)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

type gcPauses struct {
	n          uint64
	total, max time.Duration
}

func readGCPauses() gcPauses {
	s := []metrics.Sample{{Name: "/gc/cycles/total:gc-cycles"}, {Name: "/sched/pauses/total/gc:seconds"}}
	metrics.Read(s)
	g := gcPauses{n: s[0].Value.Uint64()}
	h := s[1].Value.Float64Histogram()
	for i, c := range h.Counts {
		if c == 0 {
			continue
		}
		mid := h.Buckets[i+1]
		if mid > 1 {
			mid = h.Buckets[i]
		}
		g.total += time.Duration(float64(c) * mid * float64(time.Second))
		g.max = max(g.max, time.Duration(mid*float64(time.Second)))
	}
	return g
}

func durs(fs []perfFrame, f func(perfFrame) time.Duration) []time.Duration {
	out := make([]time.Duration, len(fs))
	for i, x := range fs {
		out[i] = f(x)
	}
	slices.Sort(out)
	return out
}

func pct(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := (len(sorted) - 1) * p / 100
	return sorted[i]
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
func ms1(d time.Duration) string { return strconv.FormatFloat(ms(d), 'f', 1, 64) }

func envFloat(name string, def float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(name), 64); err == nil {
		return v
	}
	return def
}

func TestPerfPoolSizes(t *testing.T) {
	if os.Getenv("PEPE_PERF") == "" {
		t.Skip("set PEPE_PERF=1")
	}
	p := perfPool(t)
	for i := range p.fanart {
		t.Logf("fanart %d: %d KiB, poster %d: %d KiB", i, len(p.fanart[i])>>10, i, len(p.poster[i])>>10)
	}
}

// TestPerfRealArtwork samples artwork from the Polochon server in
// POLOCHON_URL/POLOCHON_TOKEN, so the perf fixtures can match it. Opt-in:
// PEPE_PERF_REAL=1. It logs sizes and timings, never URLs.
func TestPerfRealArtwork(t *testing.T) {
	if os.Getenv("PEPE_PERF_REAL") == "" {
		t.Skip("set PEPE_PERF_REAL=1")
	}
	client, err := NewAPIClient(os.Getenv("POLOCHON_URL"), os.Getenv("POLOCHON_TOKEN"), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if err := client.FetchLibrary(); err != nil {
		t.Fatal(err)
	}
	n := int(envFloat("PEPE_PERF_REAL_N", 30))
	rng := rand.New(rand.NewPCG(1, 1))
	type sample struct {
		kind       string
		bytes      int
		w, h       int
		format     string
		ttfb, read time.Duration
	}
	var samples []sample
	probe := func(kind string, f *papi.File) {
		if f == nil {
			return
		}
		start := time.Now()
		body, err := client.downloadFile(f)
		if err != nil {
			t.Logf("%s: %s", kind, artworkLogReason(err))
			return
		}
		ttfb := time.Since(start)
		data, err := io.ReadAll(body)
		_ = body.Close()
		if err != nil {
			t.Logf("%s: read failed", kind)
			return
		}
		cfg, format, _ := image.DecodeConfig(bytes.NewReader(data))
		samples = append(samples, sample{kind, len(data), cfg.Width, cfg.Height, format, ttfb, time.Since(start) - ttfb})
	}
	var movies []*papi.Movie
	for _, m := range client.movies {
		movies = append(movies, m)
	}
	slices.SortFunc(movies, func(a, b *papi.Movie) int { return strings.Compare(a.ImdbID, b.ImdbID) })
	for range min(n, len(movies)) {
		m := movies[rng.IntN(len(movies))]
		probe("movie poster", m.ThumbFile)
		probe("movie fanart", m.FanartFile)
	}
	var shows []*papi.Show
	for _, s := range client.shows {
		shows = append(shows, s)
	}
	slices.SortFunc(shows, func(a, b *papi.Show) int { return strings.Compare(a.ImdbID, b.ImdbID) })
	for range min(n/3, len(shows)) {
		s := shows[rng.IntN(len(shows))]
		probe("show poster", s.PosterFile)
		probe("show fanart", s.FanartFile)
	}
	t.Logf("catalog: %d movies, %d shows", len(client.movies), len(client.shows))
	for _, kind := range []string{"movie poster", "movie fanart", "show poster", "show fanart"} {
		var size, ttfb, read []time.Duration // size in bytes, as a Duration for pct
		dims := map[string]int{}
		formats := map[string]int{}
		for _, s := range samples {
			if s.kind != kind {
				continue
			}
			size = append(size, time.Duration(s.bytes))
			ttfb = append(ttfb, s.ttfb)
			read = append(read, s.read)
			dims[fmt.Sprintf("%dx%d", s.w, s.h)]++
			formats[s.format]++
		}
		if len(size) == 0 {
			continue
		}
		slices.Sort(size)
		slices.Sort(ttfb)
		slices.Sort(read)
		t.Logf("%s: n=%d KiB p10/p50/p90/max=%d/%d/%d/%d  ttfb p50/p90=%v/%v  read p50/p90=%v/%v  formats=%v",
			kind, len(size), pct(size, 10)>>10, pct(size, 50)>>10, pct(size, 90)>>10, pct(size, 100)>>10,
			pct(ttfb, 50).Round(time.Millisecond), pct(ttfb, 90).Round(time.Millisecond),
			pct(read, 50).Round(time.Millisecond), pct(read, 90).Round(time.Millisecond), formats)
		t.Logf("  dimensions: %v", dims)
	}
}

// BenchmarkCatalogFrame splits the cost of one settled catalog frame (Last
// added, all artwork loaded, no fade or motion) at 1920×1080. Each part's ops
// are recorded once and replayed on a separate headless window, so textures
// stay cached as in a steady frame and layout is excluded; one pixel is read
// back per frame so queued GPU work completes. "gio-ms" is the Frame call
// (Gio's CPU side and synchronous driver work), ns/op adds GPU completion.
// "layout" is a.Layout alone. Set LIBGL_ALWAYS_SOFTWARE=0 for the GPU;
// PEPE_PERF_SHOT=DIR saves each part as DIR/<part>.png.
func BenchmarkCatalogFrame(b *testing.B) {
	const w, h = 1920, 1080
	link := &mockLink{latency: time.Millisecond, bytesPerSec: 1e9}
	client := newPerfClient(b, 400, link)
	a := New(client, nil, &Config{MPVPath: "mpv"}, nil, nil)
	defer a.Close()
	d, err := driver.New(driver.Config{Width: w, Height: h, PxPerDp: 1, PxPerSp: 1, Loading: a.Loading}, a.Layout)
	if err != nil {
		b.Skipf("headless driver: %v", err)
	}
	defer d.Close()
	for _, k := range []string{"LeftArrow", "DownArrow", "RightArrow", "RightArrow", "DownArrow", "DownArrow"} {
		if err := d.Key(k, nil); err != nil {
			b.Fatal(err)
		}
	}
	if idle, err := d.WaitIdle(30 * time.Second); err != nil || !idle {
		b.Fatalf("catalog did not settle: idle=%t err=%v", idle, err)
	}
	time.Sleep(2 * backdropFade)
	if _, err := d.WaitIdle(5 * time.Second); err != nil {
		b.Fatal(err)
	}
	rows, missing, preview := perfVisiblePosters(a)
	st := a.browse.backdrop
	if !st.cur.sharp || st.curArt.sharp.Size().X == 0 || missing > 0 || !preview || a.inMotion {
		b.Fatalf("not settled: sharp=%t rows=%d missing=%d preview=%t motion=%t", st.cur.sharp, rows, missing, preview, a.inMotion)
	}
	b.Logf("settled: backdrop %v sharp, %d row posters, preview poster shown", st.curArt.sharp.Size(), rows)

	window, err := headless.NewWindow(w, h)
	if err != nil {
		b.Skipf("headless window: %v", err)
	}
	defer window.Release()
	pixel := image.NewRGBA(image.Rect(0, 0, 1, 1))
	gtxFor := func(ops *op.Ops) C {
		return layout.Context{Ops: ops, Now: time.Now(), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Constraints: layout.Exact(image.Pt(w, h))}
	}
	// The catalog's own geometry inside the viewport insets, as layoutCatalog.
	inner := func(gtx C, f func(gtx C)) {
		in := gtx.Dp(dp24)
		defer op.Offset(image.Pt(in, in)).Push(gtx.Ops).Pop()
		gtx.Constraints = layout.Exact(image.Pt(w-2*in, h-in-gtx.Dp(dp48)))
		f(gtx)
	}
	left := func(gtx C) int {
		return int(catalogLeftWidth(float32(gtx.Constraints.Max.X), float32(gtx.Dp(a.modeRailWidth()))))
	}
	withoutBackdrop := func(f func()) {
		cache, state := a.backdrops, a.browse.backdrop
		a.backdrops = newWeightedAsyncCache[backdrop](1, 0, backdropCacheBudget, backdropWeight)
		a.browse.backdrop = backdropState{}
		defer func() { a.backdrops, a.browse.backdrop = cache, state }()
		f()
	}
	textTop := a.browse.previewTextTop
	parts := []struct {
		name string
		draw func(gtx C)
	}{
		{"empty", func(gtx C) {}},
		{"backdrop", func(gtx C) { a.layoutBackdrop(gtx) }},
		{"backdropSharpBox", func(gtx C) {
			drawImage(gtx, st.curArt.sharp, image.Rect(int(backdropBoxLeft*w), 0, w, h), true)
		}},
		{"backdropSharpFull", func(gtx C) { drawImage(gtx, st.curArt.sharp, image.Rect(0, 0, w, h), false) }},
		{"backdropVeil", func(gtx C) { drawImage(gtx, st.curArt.veils[0], image.Rect(0, 0, w, h), false) }},
		{"backdropAmbient", func(gtx C) { drawImage(gtx, st.curArt.ambient, image.Rect(0, 0, w, h), false) }},
		{"translucentFill", func(gtx C) { paint.Fill(gtx.Ops, color.NRGBA{R: 0x80, A: 0x80}) }},
		{"veilSynth160x90", func(gtx C) { drawImage(gtx, synthVeil(160, 90, false), image.Rect(0, 0, w, h), false) }},
		{"veilSynth160x90nearest", func(gtx C) { drawImage(gtx, synthVeil(160, 90, true), image.Rect(0, 0, w, h), false) }},
		{"veilSynth256x128", func(gtx C) { drawImage(gtx, synthVeil(256, 128, false), image.Rect(0, 0, w, h), false) }},
		{"veilSynth480x270", func(gtx C) { drawImage(gtx, synthVeil(480, 270, false), image.Rect(0, 0, w, h), false) }},
		{"veilSynth960x540", func(gtx C) { drawImage(gtx, synthVeil(960, 540, false), image.Rect(0, 0, w, h), false) }},
		{"veilSynth1920x1080", func(gtx C) { drawImage(gtx, synthVeil(1920, 1080, false), image.Rect(0, 0, w, h), false) }},
		{"veilSynth1920x1080up1.5", func(gtx C) {
			drawImage(gtx, synthVeil(1920, 1080, false), image.Rect(0, 0, w*3/2, h*3/2), false)
		}},
		{"veilSynth160x90halfScreen", func(gtx C) { drawImage(gtx, synthVeil(160, 90, false), image.Rect(0, 0, w/2, h), false) }},
		{"veilSynth160x90twice", func(gtx C) {
			v := synthVeil(160, 90, false)
			drawImage(gtx, v, image.Rect(0, 0, w, h), false)
			drawImage(gtx, v, image.Rect(0, 0, w, h), false)
		}},
		{"linearGradient", func(gtx C) {
			paint.LinearGradientOp{Stop1: f32.Pt(0, 0), Color1: color.NRGBA{R: 0x20, A: 0xff}, Stop2: f32.Pt(w, 0), Color2: color.NRGBA{B: 0x20}}.Add(gtx.Ops)
			defer clip.Rect(image.Rect(0, 0, w, h)).Push(gtx.Ops).Pop()
			paint.PaintOp{}.Add(gtx.Ops)
		}},
		{"scrim", func(gtx C) { scrim(gtx, textTop+gtx.Dp(dp24)) }},
		{"leftColumn", func(gtx C) {
			inner(gtx, func(gtx C) {
				gtx.Constraints = layout.Exact(image.Pt(left(gtx), gtx.Constraints.Max.Y))
				layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(a.layoutHeader),
					layout.Flexed(1, func(gtx C) D {
						return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
							layout.Rigid(a.layoutModeRail),
							layout.Flexed(1, func(gtx C) D { return layout.Inset{Top: dp8}.Layout(gtx, a.layoutNavList) }),
						)
					}),
				)
			})
		}},
		{"preview", func(gtx C) {
			inner(gtx, func(gtx C) {
				l := left(gtx)
				defer op.Offset(image.Pt(l, 0)).Push(gtx.Ops).Pop()
				gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X-l, gtx.Constraints.Max.Y))
				a.layoutPreview(gtx)
			})
		}},
		{"helpBar", func(gtx C) {
			defer op.Offset(image.Pt(0, h-gtx.Dp(dp48))).Push(gtx.Ops).Pop()
			gtx.Constraints = layout.Constraints{Max: image.Pt(w, gtx.Dp(dp48))}
			a.layoutHelp(gtx)
		}},
		{"helpBg", func(gtx C) {
			defer op.Offset(image.Pt(0, h-gtx.Dp(dp48))).Push(gtx.Ops).Pop()
			defer clip.Rect{Max: image.Pt(w, gtx.Dp(dp48))}.Push(gtx.Ops).Pop()
			paint.Fill(gtx.Ops, colorBg)
			hairline(gtx)
		}},
		{"helpKeycapShapes", func(gtx C) {
			defer op.Offset(image.Pt(gtx.Dp(dp24), h-gtx.Dp(dp32))).Push(gtx.Ops).Pop()
			for range 8 {
				size := image.Pt(gtx.Dp(36), gtx.Dp(22))
				fillRounded(gtx, size, dp6, colorSurface2)
				strokeRounded(gtx, size, dp6, colorOutline)
				op.Offset(image.Pt(gtx.Dp(140), 0)).Add(gtx.Ops)
			}
		}},
		{"helpKeycapFillsOnly", func(gtx C) {
			defer op.Offset(image.Pt(gtx.Dp(dp24), h-gtx.Dp(dp32))).Push(gtx.Ops).Pop()
			for range 8 {
				fillRounded(gtx, image.Pt(gtx.Dp(36), gtx.Dp(22)), dp6, colorSurface2)
				op.Offset(image.Pt(gtx.Dp(140), 0)).Add(gtx.Ops)
			}
		}},
		{"helpLabels", func(gtx C) {
			defer op.Offset(image.Pt(gtx.Dp(dp24), h-gtx.Dp(dp32))).Push(gtx.Ops).Pop()
			gtx.Constraints = layout.Constraints{Max: image.Pt(gtx.Dp(120), gtx.Dp(dp32))}
			for range 8 {
				a.label(gtx, spSmall, "Navigate", colorSubtle, 0)
				op.Offset(image.Pt(gtx.Dp(140), 0)).Add(gtx.Ops)
			}
		}},
		{"leftHeader", func(gtx C) {
			inner(gtx, func(gtx C) {
				gtx.Constraints = layout.Constraints{Max: image.Pt(left(gtx), gtx.Constraints.Max.Y)}
				a.layoutHeader(gtx)
			})
		}},
		{"leftModeRail", func(gtx C) {
			inner(gtx, func(gtx C) {
				gtx.Constraints = layout.Constraints{Max: image.Pt(left(gtx), gtx.Constraints.Max.Y)}
				a.layoutModeRail(gtx)
			})
		}},
		{"leftNavList", func(gtx C) {
			inner(gtx, func(gtx C) {
				rail := gtx.Dp(a.modeRailWidth())
				defer op.Offset(image.Pt(rail, gtx.Dp(dp48))).Push(gtx.Ops).Pop()
				gtx.Constraints = layout.Exact(image.Pt(left(gtx)-rail, gtx.Constraints.Max.Y-gtx.Dp(dp48)))
				a.layoutNavList(gtx)
			})
		}},
		{"navSelection", func(gtx C) {
			defer op.Offset(image.Pt(120, 238)).Push(gtx.Ops).Pop()
			paintCatalogSelection(gtx, image.Pt(640, 82), a.browse.selectionReach)
		}},
		{"navSelectionFlat", func(gtx C) {
			defer op.Offset(image.Pt(120, 238)).Push(gtx.Ops).Pop()
			fillRounded(gtx, image.Pt(640+a.browse.selectionReach, 78), radiusRow, colorSelectBg)
		}},
		{"navThumbsRounded", func(gtx C) { navThumbs(gtx, synthVeil(96, 144, false), gtx.Dp(radiusThumb)) }},
		{"navThumbsSquare", func(gtx C) { navThumbs(gtx, synthVeil(96, 144, false), 0) }},
		{"navLabels", func(gtx C) {
			gtx.Constraints = layout.Constraints{Max: image.Pt(600, 60)}
			for i := range 12 {
				stack := op.Offset(image.Pt(180, 90+82*i)).Push(gtx.Ops)
				a.label(gtx, spBody, fmt.Sprintf("Distant empire (%d)", 1980+i), colorFg, 0)
				stack.Pop()
			}
		}},
		// The backdrop cross-fade: the incoming view is drawn through an
		// opacity layer over the outgoing one.
		{"opacityLayerSharpBox", func(gtx C) {
			defer paint.PushOpacity(gtx.Ops, 0.5).Pop()
			drawImage(gtx, st.curArt.sharp, image.Rect(int(backdropBoxLeft*w), 0, w, h), true)
		}},
		{"opacityLayerFill", func(gtx C) {
			defer paint.PushOpacity(gtx.Ops, 0.5).Pop()
			paint.Fill(gtx.Ops, color.NRGBA{R: 0x80, A: 0xff})
		}},
		{"backdropMidFade", func(gtx C) {
			size := image.Pt(w, h)
			a.drawBackdrop(gtx, st.cur, st.curArt, size, 1)
			a.drawBackdrop(gtx, st.cur, st.curArt, size, 0.5)
		}},
		{"backdropMidFadeAmbient", func(gtx C) {
			size, amb := image.Pt(w, h), st.cur
			amb.sharp = false
			a.drawBackdrop(gtx, amb, st.curArt, size, 1)
			a.drawBackdrop(gtx, amb, st.curArt, size, 0.5)
		}},
		{"uiNoBackdrop", func(gtx C) { withoutBackdrop(func() { a.Layout(gtx) }) }},
		{"full", func(gtx C) { a.Layout(gtx) }},
		{"fullMidFade", func(gtx C) {
			saved := a.browse.backdrop
			defer func() { a.browse.backdrop = saved }()
			bs := &a.browse.backdrop
			bs.prev, bs.prevArt = bs.cur, bs.curArt
			bs.since = gtx.Now.Add(-backdropFade / 4)
			a.Layout(gtx)
		}},
	}
	for _, p := range parts {
		var ops op.Ops
		gtx := gtxFor(&ops)
		paint.Fill(gtx.Ops, colorBg)
		p.draw(gtx)
		if dir := os.Getenv("PEPE_PERF_SHOT"); dir != "" {
			shot := image.NewRGBA(image.Rect(0, 0, w, h))
			if err := window.Frame(&ops); err != nil {
				b.Fatal(err)
			}
			if err := window.Screenshot(shot); err != nil {
				b.Fatal(err)
			}
			var buf bytes.Buffer
			if err := png.Encode(&buf, shot); err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, p.name+".png"), buf.Bytes(), 0o644); err != nil {
				b.Fatal(err)
			}
		}
		b.Run(p.name, func(b *testing.B) {
			var gio time.Duration
			for b.Loop() {
				start := time.Now()
				if err := window.Frame(&ops); err != nil {
					b.Fatal(err)
				}
				gio += time.Since(start)
				if err := window.Screenshot(pixel); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(ms(gio)/float64(b.N), "gio-ms")
		})
	}
	b.Run("layout", func(b *testing.B) {
		var ops op.Ops
		for b.Loop() {
			ops.Reset()
			a.Layout(gtxFor(&ops))
		}
	})
	if st := a.browse.backdrop; !st.cur.sharp || st.curArt.sharp.Size().X == 0 {
		b.Error("backdrop changed during the benchmark")
	}
}

// synthVeil is a w×h stand-in for a veil texture: a colour with an alpha
// ramp, so its GPU cost can be compared across texture sizes and filters.
func synthVeil(w, h int, nearest bool) paint.ImageOp {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 255 / w), G: 0x30, B: uint8(y * 255 / h), A: uint8(255 - x*255/w)})
		}
	}
	op := paint.NewImageOp(img)
	if nearest {
		op.Filter = paint.FilterNearest
	}
	return op
}

// navThumbs draws img as 12 row thumbnails of 44×66 px, clipped to rounded
// corners of radius r like posterImage.
func navThumbs(gtx C, img paint.ImageOp, r int) {
	for i := range 12 {
		rect := image.Rect(124, 80+82*i, 124+44, 80+82*i+66)
		stack := clip.RRect{Rect: rect, NE: r, NW: r, SE: r, SW: r}.Push(gtx.Ops)
		drawImage(gtx, img, rect, true)
		stack.Pop()
	}
}
