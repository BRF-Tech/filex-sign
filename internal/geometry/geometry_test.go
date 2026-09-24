package geometry

import (
	"math"
	"testing"
)

const letterW, letterH = 612.0, 792.0

func near(a, b float64) bool { return math.Abs(a-b) < 1e-4 }

func eqRect(t *testing.T, got, want Rect) {
	t.Helper()
	if !near(got.X, want.X) || !near(got.Y, want.Y) || !near(got.W, want.W) || !near(got.H, want.H) {
		t.Fatalf("rect = %+v, want %+v", got, want)
	}
}

func eqFrac(t *testing.T, got, want Frac) {
	t.Helper()
	if !near(got.X, want.X) || !near(got.Y, want.Y) || !near(got.W, want.W) || !near(got.H, want.H) {
		t.Fatalf("frac = %+v, want %+v", got, want)
	}
}

// Hand-computed: a box at the centre-right of an unrotated Letter page.
func TestFracToPDF_Rotate0(t *testing.T) {
	v := Viewport{Width: letterW, Height: letterH, Rotation: 0}
	f := Frac{X: 0.5, Y: 0.5, W: 0.25, H: 0.125}
	// x = 306, top = 396, bottom = 495 → y = 792 - 495 = 297, w = 153, h = 99
	eqRect(t, FracToPDF(f, v), Rect{X: 306, Y: 297, W: 153, H: 99})
}

// Rotate 90: the page is drawn 792 wide × 612 tall. A box in the rendered
// top-left corner is the unrotated page's bottom-left corner, with width
// and height swapped.
func TestFracToPDF_Rotate90(t *testing.T) {
	v := Viewport{Width: letterH, Height: letterW, Rotation: 90}
	f := Frac{X: 0, Y: 0, W: 0.1, H: 0.1}
	// rendered box: 79.2 wide, 61.2 tall → unrotated: 61.2 wide, 79.2 tall at (0,0)
	eqRect(t, FracToPDF(f, v), Rect{X: 0, Y: 0, W: 61.2, H: 79.2})

	// The rendered top-right corner is the unrotated top-left.
	f = Frac{X: 0.9, Y: 0, W: 0.1, H: 0.1}
	eqRect(t, FracToPDF(f, v), Rect{X: 0, Y: letterH - 79.2, W: 61.2, H: 79.2})
}

// Rotate 180: everything mirrors through the centre; a rendered top-left
// box is the unrotated bottom-right one.
func TestFracToPDF_Rotate180(t *testing.T) {
	v := Viewport{Width: letterW, Height: letterH, Rotation: 180}
	f := Frac{X: 0, Y: 0, W: 0.1, H: 0.1}
	eqRect(t, FracToPDF(f, v), Rect{X: letterW - 61.2, Y: 0, W: 61.2, H: 79.2})
}

// Rotate 270: the rendered top-left is the unrotated top-right.
func TestFracToPDF_Rotate270(t *testing.T) {
	v := Viewport{Width: letterH, Height: letterW, Rotation: 270}
	f := Frac{X: 0, Y: 0, W: 0.1, H: 0.1}
	eqRect(t, FracToPDF(f, v), Rect{X: letterW - 61.2, Y: letterH - 79.2, W: 61.2, H: 79.2})
}

// A CropBox that does not start at the origin shifts the /Rect by its
// lower-left corner, and only the visible area counts for the fractions.
func TestToRect_CropBoxOffset(t *testing.T) {
	b := Box{X0: 50, Y0: 100, X1: 350, Y1: 500, Rotate: 0} // 300 × 400 visible
	f := Frac{X: 0.5, Y: 0.25, W: 0.5, H: 0.25}
	// local: x = 150, top = 100, bottom = 200 → y = 400 - 200 = 200, w = 150, h = 100
	eqRect(t, ToRect(f, b), Rect{X: 200, Y: 300, W: 150, H: 100})
	eqFrac(t, FromRect(Rect{X: 200, Y: 300, W: 150, H: 100}, b), f)
}

func TestToRect_CropBoxOffsetRotated(t *testing.T) {
	b := Box{X0: 50, Y0: 100, X1: 350, Y1: 500, Rotate: 90} // rendered 400 × 300
	f := Frac{X: 0, Y: 0, W: 0.1, H: 0.1}                   // rendered top-left = unrotated bottom-left
	// local unrotated: (0,0) w = 30, h = 40 → plus offset
	eqRect(t, ToRect(f, b), Rect{X: 50, Y: 100, W: 30, H: 40})
}

func TestRoundTrip_AllRotations(t *testing.T) {
	boxes := []Box{
		{0, 0, letterW, letterH, 0}, {0, 0, letterW, letterH, 90},
		{0, 0, letterW, letterH, 180}, {0, 0, letterW, letterH, 270},
		{20, 30, 400, 700, 90}, {20, 30, 400, 700, 270},
	}
	fracs := []Frac{{0.1, 0.2, 0.3, 0.1}, {0.6, 0.7, 0.25, 0.2}, {0, 0, 1, 1}}
	for _, b := range boxes {
		for _, f := range fracs {
			back := FromRect(ToRect(f, b), b)
			eqFrac(t, back, f)
		}
	}
}

func TestViewport(t *testing.T) {
	v := Box{0, 0, letterW, letterH, 90}.Viewport()
	if v.Width != letterH || v.Height != letterW || v.Rotation != 90 {
		t.Fatalf("viewport = %+v", v)
	}
	v = Box{letterW, letterH, 0, 0, -90}.Viewport() // reversed corners, negative rotation
	if v.Width != letterH || v.Height != letterW || v.Rotation != 270 {
		t.Fatalf("viewport = %+v", v)
	}
}

func TestNormRotation(t *testing.T) {
	cases := map[int]int{0: 0, 90: 90, 180: 180, 270: 270, 360: 0, -90: 270, 450: 90, 89: 90, 44: 0}
	for in, want := range cases {
		if got := NormRotation(in); got != want {
			t.Errorf("NormRotation(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestClamp(t *testing.T) {
	f := Clamp(Frac{X: 0.99, Y: 0.99, W: 0.5, H: 0.5})
	if f.X+f.W > 1+1e-9 || f.Y+f.H > 1+1e-9 {
		t.Fatalf("clamp left the page: %+v", f)
	}
	f = Clamp(Frac{X: -1, Y: math.NaN(), W: 0, H: 0})
	if f.X != 0 || f.Y != 0 || f.W != 0.02 || f.H != 0.01 {
		t.Fatalf("clamp minimums: %+v", f)
	}
}
