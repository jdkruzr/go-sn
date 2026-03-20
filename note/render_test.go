package note

import (
	"image"
	"image/color"
	"testing"
	"time"
)

// TestRenderObjects_OutOfBoundsPoints verifies that strokes with wildly
// out-of-bounds coordinates render without hanging. Before the fix,
// drawThickLine would iterate a bounding box of millions of pixels.
func TestRenderObjects_OutOfBoundsPoints(t *testing.T) {
	objs := &PageObjects{
		Strokes: []Stroke{
			{
				Points: []Point{
					{X: 100, Y: 200},        // normal
					{X: -14_000_000, Y: 200}, // wildly out of bounds
					{X: 200, Y: 14_000_000},  // wildly out of bounds
					{X: 300, Y: 400},         // normal
				},
				Pressures: []uint16{1000, 1000, 1000, 1000},
			},
		},
	}

	done := make(chan *image.RGBA, 1)
	go func() {
		done <- RenderObjects(objs, 1404, 1872, nil)
	}()

	select {
	case img := <-done:
		if img == nil {
			t.Fatal("expected non-nil image")
		}
		if img.Bounds() != image.Rect(0, 0, 1404, 1872) {
			t.Errorf("bounds = %v, want (0,0)-(1404,1872)", img.Bounds())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RenderObjects hung on out-of-bounds coordinates (>5s)")
	}
}

// TestRenderObjects_NormalStroke verifies that normal in-bounds strokes
// still render correctly after the bounding-box clamp fix.
func TestRenderObjects_NormalStroke(t *testing.T) {
	objs := &PageObjects{
		Strokes: []Stroke{
			{
				Points: []Point{
					{X: 100, Y: 100},
					{X: 200, Y: 200},
					{X: 300, Y: 300},
				},
				Pressures: []uint16{1500, 1500, 1500},
			},
		},
	}

	img := RenderObjects(objs, 1404, 1872, nil)
	if img == nil {
		t.Fatal("expected non-nil image")
	}

	// The stroke path should have drawn black pixels somewhere in the region
	hasInk := false
	for y := 95; y <= 305; y++ {
		for x := 95; x <= 305; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r == 0 && g == 0 && b == 0 {
				hasInk = true
				break
			}
		}
		if hasInk {
			break
		}
	}
	if !hasInk {
		t.Error("expected ink pixels in the stroke region, got none")
	}
}

// TestDrawThickLine_ClampsBoundingBox verifies drawThickLine doesn't iterate
// beyond image bounds even with extreme coordinates.
func TestDrawThickLine_ClampsBoundingBox(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	ink := color.RGBA{R: 0, G: 0, B: 0, A: 255}

	done := make(chan struct{})
	go func() {
		drawThickLine(img, 50, 50, 10_000_000, 10_000_000, 2, 2, ink)
		close(done)
	}()

	select {
	case <-done:
		// completed without hanging
	case <-time.After(2 * time.Second):
		t.Fatal("drawThickLine hung on extreme coordinates (>2s)")
	}
}
