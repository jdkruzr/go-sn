package note

import (
	"image"
	"image/color"
	"math"
)

const (
	// pressureMin / pressureMax are the empirically observed raw pressure range.
	// Map this to stroke widths in pixels.
	pressureMin = 200
	pressureMax = 3000

	strokeWidthMin = 1.0
	strokeWidthMax = 4.0
)

// RenderOpts controls how strokes are rendered.
type RenderOpts struct {
	// Background color for the canvas. Defaults to white.
	Background color.Color
	// Ink color. Defaults to black.
	Ink color.Color
	// TextBoxColor is the outline color for text box bounding boxes.
	// Defaults to blue. Set to nil to suppress rendering.
	TextBoxColor color.Color
	// DigestColor is the outline color for digest bounding boxes.
	// Defaults to red. Set to nil to suppress rendering.
	DigestColor color.Color
}

var defaultOpts = RenderOpts{
	Background:   color.White,
	Ink:          color.Black,
	TextBoxColor: color.RGBA{R: 0, G: 0, B: 200, A: 255},
	DigestColor:  color.RGBA{R: 200, G: 0, B: 0, A: 255},
}

// Render draws strokes onto a new RGBA image of the given size.
// If opts is nil, defaults are used.
func Render(strokes []Stroke, w, h int, opts *RenderOpts) *image.RGBA {
	return RenderObjects(&PageObjects{Strokes: strokes}, w, h, opts)
}

// RenderObjects draws all page objects (strokes + non-stroke bounding boxes)
// onto a new RGBA image of the given size. If opts is nil, defaults are used.
// Individual nil color fields fall back to their defaults.
func RenderObjects(objs *PageObjects, w, h int, opts *RenderOpts) *image.RGBA {
	if opts == nil {
		opts = &defaultOpts
	}
	bg := opts.Background
	if bg == nil {
		bg = defaultOpts.Background
	}
	ink := opts.Ink
	if ink == nil {
		ink = defaultOpts.Ink
	}

	img := image.NewRGBA(image.Rect(0, 0, w, h))

	// Fill background
	bgRGBA := toRGBA(bg)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, bgRGBA)
		}
	}

	inkRGBA := toRGBA(ink)
	for _, s := range objs.Strokes {
		renderStroke(img, s, inkRGBA)
	}

	for _, ns := range objs.NonStrokes {
		var c color.Color
		switch ns.Type {
		case ObjectTypeTextBox:
			c = opts.TextBoxColor
		case ObjectTypeDigest:
			c = opts.DigestColor
		}
		if c != nil {
			drawRect(img, ns.Bounds, toRGBA(c))
		}
	}

	return img
}

func renderStroke(img *image.RGBA, s Stroke, ink color.RGBA) {
	if len(s.Points) == 0 {
		return
	}

	for i := 0; i < len(s.Points); i++ {
		pt := s.Points[i]

		// Stroke width from pressure (or default if no pressure data)
		width := strokeWidthMin + (strokeWidthMax-strokeWidthMin)*0.5 // default mid
		if i < len(s.Pressures) {
			p := float64(s.Pressures[i])
			t := (p - pressureMin) / (pressureMax - pressureMin)
			t = clamp01(t)
			width = strokeWidthMin + t*(strokeWidthMax-strokeWidthMin)
		}

		// Draw a filled circle at this point
		drawCircle(img, pt.X, pt.Y, width*0.5, ink)

		// Connect to next point with a thick line
		if i+1 < len(s.Points) {
			next := s.Points[i+1]
			nextWidth := width
			if i+1 < len(s.Pressures) {
				p := float64(s.Pressures[i+1])
				t := (p - pressureMin) / (pressureMax - pressureMin)
				t = clamp01(t)
				nextWidth = strokeWidthMin + t*(strokeWidthMax-strokeWidthMin)
			}
			drawThickLine(img, pt.X, pt.Y, next.X, next.Y, width*0.5, nextWidth*0.5, ink)
		}
	}
}

// drawCircle draws a filled anti-aliased circle.
func drawCircle(img *image.RGBA, cx, cy, r float64, c color.RGBA) {
	b := img.Bounds()
	x0 := int(math.Floor(cx - r - 1))
	x1 := int(math.Ceil(cx + r + 1))
	y0 := int(math.Floor(cy - r - 1))
	y1 := int(math.Ceil(cy + r + 1))

	for py := y0; py <= y1; py++ {
		for px := x0; px <= x1; px++ {
			if px < b.Min.X || px >= b.Max.X || py < b.Min.Y || py >= b.Max.Y {
				continue
			}
			dx := float64(px) + 0.5 - cx
			dy := float64(py) + 0.5 - cy
			dist := math.Sqrt(dx*dx+dy*dy) - r
			alpha := clamp01(1.0 - dist)
			if alpha > 0 {
				blendPixel(img, px, py, c, alpha)
			}
		}
	}
}

// drawThickLine draws an anti-aliased line with variable radius at each end.
func drawThickLine(img *image.RGBA, x0, y0, x1, y1, r0, r1 float64, c color.RGBA) {
	dx := x1 - x0
	dy := y1 - y0
	length := math.Sqrt(dx*dx + dy*dy)
	if length < 0.5 {
		return
	}

	// Bounding box
	maxR := math.Max(r0, r1) + 1
	b := img.Bounds()
	bx0 := int(math.Floor(math.Min(x0, x1) - maxR))
	bx1 := int(math.Ceil(math.Max(x0, x1) + maxR))
	by0 := int(math.Floor(math.Min(y0, y1) - maxR))
	by1 := int(math.Ceil(math.Max(y0, y1) + maxR))

	for py := by0; py <= by1; py++ {
		for px := bx0; px <= bx1; px++ {
			if px < b.Min.X || px >= b.Max.X || py < b.Min.Y || py >= b.Max.Y {
				continue
			}
			// Project pixel center onto the line segment
			pcx := float64(px) + 0.5
			pcy := float64(py) + 0.5
			t := ((pcx-x0)*dx + (pcy-y0)*dy) / (length * length)
			t = clamp01(t)
			// Interpolated radius at this t
			r := r0 + t*(r1-r0)
			// Distance from pixel to the closest point on the segment
			cx := x0 + t*dx
			cy := y0 + t*dy
			d := math.Sqrt((pcx-cx)*(pcx-cx) + (pcy-cy)*(pcy-cy))
			dist := d - r
			alpha := clamp01(1.0 - dist)
			if alpha > 0 {
				blendPixel(img, px, py, c, alpha)
			}
		}
	}
}

// drawRect draws a 1-pixel-wide rectangle outline for the given Rect.
func drawRect(img *image.RGBA, r Rect, c color.RGBA) {
	x0 := int(math.Round(r.MinX))
	y0 := int(math.Round(r.MinY))
	x1 := int(math.Round(r.MaxX))
	y1 := int(math.Round(r.MaxY))
	b := img.Bounds()

	setPixel := func(x, y int) {
		if x >= b.Min.X && x < b.Max.X && y >= b.Min.Y && y < b.Max.Y {
			img.SetRGBA(x, y, c)
		}
	}

	for x := x0; x <= x1; x++ {
		setPixel(x, y0)
		setPixel(x, y1)
	}
	for y := y0; y <= y1; y++ {
		setPixel(x0, y)
		setPixel(x1, y)
	}
}

// blendPixel alpha-composites color c over the existing pixel at (x,y).
func blendPixel(img *image.RGBA, x, y int, c color.RGBA, alpha float64) {
	idx := img.PixOffset(x, y)
	pix := img.Pix
	a := uint8(float64(c.A) * alpha)
	if a == 0 {
		return
	}
	// Porter-Duff "src over dst"
	fa := float64(a) / 255.0
	fb := float64(255-a) / 255.0
	pix[idx+0] = uint8(float64(c.R)*fa + float64(pix[idx+0])*fb)
	pix[idx+1] = uint8(float64(c.G)*fa + float64(pix[idx+1])*fb)
	pix[idx+2] = uint8(float64(c.B)*fa + float64(pix[idx+2])*fb)
	pix[idx+3] = uint8(math.Min(float64(pix[idx+3])+float64(a), 255))
}

func toRGBA(c color.Color) color.RGBA {
	r, g, b, a := c.RGBA()
	return color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: uint8(a >> 8)}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
