// Package chart renders simple bar and line charts as PNG images using
// only the Go standard library plus golang.org/x/image's built-in 7x13
// bitmap font - no cgo, no font files, no external services, so the
// agent binary stays a single pure-Go artifact. Stage 25e: code-made
// images the agent can send as native chat media.
package chart

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

var (
	bg     = color.RGBA{255, 255, 255, 255}
	ink    = color.RGBA{30, 30, 30, 255}
	bar    = color.RGBA{37, 99, 235, 255}
	line   = color.RGBA{220, 38, 38, 255}
	grid   = color.RGBA{229, 231, 235, 255}
	axisLn = color.RGBA{120, 120, 120, 255}
)

const (
	width, height    = 640, 400
	marginL, marginR = 56, 20
	marginT, marginB = 44, 46
	maxPoints        = 40
)

// Spec describes one chart.
type Spec struct {
	Title  string
	Kind   string // "bar" or "line"
	Labels []string
	Values []float64
}

// Validate checks the spec before rendering.
func (s Spec) Validate() error {
	if s.Kind != "bar" && s.Kind != "line" {
		return fmt.Errorf("chart: kind must be \"bar\" or \"line\", got %q", s.Kind)
	}
	if len(s.Values) == 0 {
		return fmt.Errorf("chart: at least one value is required")
	}
	if len(s.Values) > maxPoints {
		return fmt.Errorf("chart: at most %d points, got %d", maxPoints, len(s.Values))
	}
	if len(s.Labels) != 0 && len(s.Labels) != len(s.Values) {
		return fmt.Errorf("chart: %d labels for %d values", len(s.Labels), len(s.Values))
	}
	return nil
}

// Render draws the chart and returns PNG bytes.
func Render(s Spec) ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	fill(img, img.Bounds(), bg)

	plot := image.Rect(marginL, marginT, width-marginR, height-marginB)
	lo, hi := bounds(s.Values)
	if lo > 0 {
		lo = 0
	}
	if hi <= lo {
		hi = lo + 1
	}
	y := func(v float64) int {
		return plot.Max.Y - int((v-lo)/(hi-lo)*float64(plot.Dy()))
	}

	// Horizontal grid lines with min/mid/max labels.
	for i := 0; i <= 4; i++ {
		gy := plot.Min.Y + i*plot.Dy()/4
		hline(img, plot.Min.X, plot.Max.X, gy, grid)
		gv := hi - float64(i)*(hi-lo)/4
		label(img, 4, gy-4, fmt.Sprintf("%.4g", gv), ink)
	}
	vline(img, plot.Min.X, plot.Min.Y, plot.Max.Y, axisLn)
	hline(img, plot.Min.X, plot.Max.X, plot.Max.Y, axisLn)

	n := len(s.Values)
	if s.Kind == "bar" {
		slot := plot.Dx() / n
		bw := slot * 7 / 10
		if bw < 2 {
			bw = 2
		}
		for i, v := range s.Values {
			x0 := plot.Min.X + i*slot + (slot-bw)/2
			rect(img, image.Rect(x0, y(v), x0+bw, y(0)), bar)
		}
	} else {
		step := float64(plot.Dx()) / float64(max(n-1, 1))
		prev := image.Point{}
		for i, v := range s.Values {
			p := image.Point{X: plot.Min.X + int(float64(i)*step), Y: y(v)}
			dot(img, p, 3, line)
			if i > 0 {
				seg(img, prev, p, line)
			}
			prev = p
		}
	}

	// X labels (thinned when crowded) and the title.
	stride := 1
	for n/stride > 8 {
		stride++
	}
	for i := 0; i < n; i += stride {
		lb := ""
		if len(s.Labels) > 0 {
			lb = s.Labels[i]
		} else {
			lb = fmt.Sprintf("%d", i+1)
		}
		if len(lb) > 10 {
			lb = lb[:10]
		}
		var cx int
		if s.Kind == "bar" {
			cx = plot.Min.X + i*(plot.Dx()/n) + (plot.Dx()/n)/2
		} else {
			cx = plot.Min.X + int(float64(i)*float64(plot.Dx())/float64(max(n-1, 1)))
		}
		label(img, cx-len(lb)*3, plot.Max.Y+8, lb, ink)
	}
	if s.Title != "" {
		label(img, marginL, 18, s.Title, ink)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func bounds(vs []float64) (lo, hi float64) {
	lo, hi = vs[0], vs[0]
	for _, v := range vs[1:] {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	return lo, hi
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func fill(img *image.RGBA, r image.Rectangle, c color.Color) {
	for yy := r.Min.Y; yy < r.Max.Y; yy++ {
		for xx := r.Min.X; xx < r.Max.X; xx++ {
			img.Set(xx, yy, c)
		}
	}
}

func rect(img *image.RGBA, r image.Rectangle, c color.Color) { fill(img, r, c) }

func hline(img *image.RGBA, x0, x1, yy int, c color.Color) {
	for xx := x0; xx < x1; xx++ {
		img.Set(xx, yy, c)
	}
}

func vline(img *image.RGBA, xx, y0, y1 int, c color.Color) {
	for yy := y0; yy < y1; yy++ {
		img.Set(xx, yy, c)
	}
}

func dot(img *image.RGBA, p image.Point, r int, c color.Color) {
	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			if dx*dx+dy*dy <= r*r {
				img.Set(p.X+dx, p.Y+dy, c)
			}
		}
	}
}

// seg draws a line with Bresenham's algorithm.
func seg(img *image.RGBA, a, b image.Point, c color.Color) {
	dx, dy := abs(b.X-a.X), -abs(b.Y-a.Y)
	sx, sy := sign(b.X-a.X), sign(b.Y-a.Y)
	err := dx + dy
	for {
		img.Set(a.X, a.Y, c)
		img.Set(a.X+1, a.Y, c)
		if a.X == b.X && a.Y == b.Y {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			a.X += sx
		}
		if e2 <= dx {
			err += dx
			a.Y += sy
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func sign(v int) int {
	switch {
	case v < 0:
		return -1
	case v > 0:
		return 1
	}
	return 0
}

func label(img *image.RGBA, x, y int, s string, c color.Color) {
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(c),
		Face: basicfont.Face7x13,
		Dot:  fixed.P(x, y+10),
	}
	d.DrawString(s)
}
