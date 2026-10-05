package driver

import (
	"path/filepath"
	"testing"
	"time"

	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget"
)

func TestHeadlessUI(t *testing.T) {
	var button widget.Clickable
	clicked := false
	d, err := New(Config{Width: 320, Height: 200, PxPerDp: 1, PxPerSp: 1}, func(gtx layout.Context) layout.Dimensions {
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
	defer d.Close()

	if len(d.Tree()) == 0 {
		t.Fatal("initial semantic tree is empty")
	}
	if err := d.Click(ClickTarget{Label: "Apply", Occurrence: 1}); err != nil {
		t.Fatal(err)
	}
	if !clicked {
		t.Fatal("semantic click did not reach widget")
	}
}

func TestScreenshotHonorsScheduledFrames(t *testing.T) {
	var started time.Time
	finished := false
	d, err := New(Config{Width: 320, Height: 200, PxPerDp: 1, PxPerSp: 1}, func(gtx layout.Context) layout.Dimensions {
		if started.IsZero() {
			started = gtx.Now
		}
		finished = gtx.Now.Sub(started) >= 60*time.Millisecond
		if !finished {
			gtx.Execute(op.InvalidateCmd{At: started.Add(60 * time.Millisecond)})
		}
		return layout.Dimensions{Size: gtx.Constraints.Max}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if finished {
		t.Fatal("fixture animation finished in first frame")
	}
	initialFrame := d.FrameNumber()
	if err := d.Screenshot(filepath.Join(t.TempDir(), "animation.png")); err != nil {
		t.Fatal(err)
	}
	if !finished || d.FrameNumber() <= initialFrame {
		t.Fatal("screenshot did not advance scheduled animation without input")
	}
}

func TestRefreshBoundsContinuousInvalidation(t *testing.T) {
	d, err := New(Config{Width: 100, Height: 100, PxPerDp: 1, PxPerSp: 1}, func(gtx layout.Context) layout.Dimensions {
		gtx.Execute(op.InvalidateCmd{})
		return layout.Dimensions{Size: gtx.Constraints.Max}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	started := time.Now()
	if err := d.Refresh(); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("Refresh did not bound a continuously invalidating UI")
	}
}

func TestConfigValidation(t *testing.T) {
	if _, err := New(Config{Width: 0, Height: 1, PxPerDp: 1, PxPerSp: 1}, func(layout.Context) layout.Dimensions {
		return layout.Dimensions{}
	}); err == nil {
		t.Fatal("expected invalid width error")
	}
}
