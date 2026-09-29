package chart

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

// A rendered chart must be a real PNG with real content: the test
// probes pixels so it fails if the renderer ever produces a blank or
// degenerate image.
func TestRenderBar(t *testing.T) {
	raw, err := Render(Spec{
		Title:  "Ventas por mes",
		Kind:   "bar",
		Labels: []string{"ene", "feb", "mar"},
		Values: []float64{3, 7, 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(raw, []byte("\x89PNG")) {
		t.Fatal("not a PNG")
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds() != image.Rect(0, 0, width, height) {
		t.Fatalf("size %v", img.Bounds())
	}
	// The tallest bar (value 7, middle slot) must paint pixels near the
	// plot top; the plot bottom-right of the last bar must stay
	// background.
	mid := img.At(marginL+plotDX()/2, marginT+10)
	r, g, b, _ := mid.RGBA()
	if uint8(r>>8) != bar.R || uint8(g>>8) != bar.G || uint8(b>>8) != bar.B {
		t.Fatalf("expected bar pixel at plot top middle, got %v", mid)
	}
	if !uniform(img, width-marginR-5, marginT+10) {
		t.Fatal("expected background at plot top right")
	}
}

func TestRenderLine(t *testing.T) {
	raw, err := Render(Spec{Kind: "line", Values: []float64{1, 4, 2, 8}})
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	// Some line-colored pixel must exist.
	found := false
	for yy := marginT; yy < height-marginB && !found; yy++ {
		for xx := marginL; xx < width-marginR; xx++ {
			r, g, b, _ := img.At(xx, yy).RGBA()
			if uint8(r>>8) == line.R && uint8(g>>8) == line.G && uint8(b>>8) == line.B {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatal("no line pixels found")
	}
}

func TestValidate(t *testing.T) {
	for _, s := range []Spec{
		{Kind: "pie", Values: []float64{1}},
		{Kind: "bar"},
		{Kind: "bar", Labels: []string{"a"}, Values: []float64{1, 2}},
	} {
		if err := s.Validate(); err == nil {
			t.Fatalf("expected error for %+v", s)
		}
	}
	many := make([]float64, maxPoints+1)
	if err := (Spec{Kind: "bar", Values: many}).Validate(); err == nil {
		t.Fatal("expected max-points error")
	}
}

func plotDX() int { return width - marginL - marginR }

func uniform(img image.Image, x, y int) bool {
	r, g, b, _ := img.At(x, y).RGBA()
	return uint8(r>>8) == bg.R && uint8(g>>8) == bg.G && uint8(b>>8) == bg.B
}
