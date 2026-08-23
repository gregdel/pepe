package app

import (
	"image"
	"testing"

	"gioui.org/layout"
	"gioui.org/unit"
)

func TestScaleContext(t *testing.T) {
	tests := []struct {
		name      string
		width     int
		pxPerDp   float32
		wantScale float32
	}{
		{name: "reference", width: 1920, pxPerDp: 1, wantScale: 1},
		{name: "half width", width: 960, pxPerDp: 1, wantScale: 0.5},
		{name: "density independent", width: 1920, pxPerDp: 2, wantScale: 0.5},
		{name: "minimum", width: 320, pxPerDp: 1, wantScale: 0.4},
		{name: "maximum", width: 7680, pxPerDp: 1, wantScale: 2},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gtx := layout.Context{
				Metric:      unit.Metric{PxPerDp: test.pxPerDp, PxPerSp: test.pxPerDp},
				Constraints: layout.Exact(image.Pt(test.width, 1080)),
			}
			got := scaleContext(gtx)
			if want := test.pxPerDp * test.wantScale; got.Metric.PxPerDp != want || got.Metric.PxPerSp != want {
				t.Fatalf("scaled metric = (%v, %v), want (%v, %v)", got.Metric.PxPerDp, got.Metric.PxPerSp, want, want)
			}
		})
	}
}
