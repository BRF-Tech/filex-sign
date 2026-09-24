// Package geometry converts a `pdf-fields` box — fractions of the page as
// pdf.js renders it (0..1, origin top-left, CropBox and /Rotate already
// applied) — into a PDF user-space rectangle (points, origin bottom-left of
// the UNROTATED page, CropBox offset included), the /Rect a signature
// widget needs. It is the Go twin of filex's `lib/pdfFieldsGeom.fracToPdf`
// and follows the same rotation maps: 90 is the page turned clockwise on
// screen (the unrotated top-left corner lands at the rendered top-right),
// 270 counter-clockwise.
//
// Pure arithmetic, no PDF parsing: the caller hands in the page's box and
// rotation (see pdfsig.Inspect) and gets numbers back.
package geometry

import "math"

// Frac is a box as fractions of the rendered page (0..1, origin top-left).
type Frac struct {
	X, Y, W, H float64
}

// Rect is a box in PDF user space: points, origin bottom-left, unrotated.
type Rect struct {
	X, Y, W, H float64
}

// LLX, LLY, URX, URY are the /Rect corners.
func (r Rect) LLX() float64 { return r.X }
func (r Rect) LLY() float64 { return r.Y }
func (r Rect) URX() float64 { return r.X + r.W }
func (r Rect) URY() float64 { return r.Y + r.H }

// Box is a page's visible area (CropBox, or MediaBox when there is none) as
// stored in the PDF, plus its effective /Rotate. The rendered viewport is
// derived from it.
type Box struct {
	X0, Y0, X1, Y1 float64
	Rotate         int
}

// Normalized returns the box with ordered corners and a rotation in
// {0, 90, 180, 270}.
func (b Box) Normalized() Box {
	if b.X1 < b.X0 {
		b.X0, b.X1 = b.X1, b.X0
	}
	if b.Y1 < b.Y0 {
		b.Y0, b.Y1 = b.Y1, b.Y0
	}
	b.Rotate = NormRotation(b.Rotate)
	return b
}

// Width and Height of the unrotated visible area, in points.
func (b Box) Width() float64  { return math.Abs(b.X1 - b.X0) }
func (b Box) Height() float64 { return math.Abs(b.Y1 - b.Y0) }

// Viewport is the rendered size after rotation, in points (what pdf.js
// draws at scale 1), with the rotation that produced it.
type Viewport struct {
	Width, Height float64
	Rotation      int
}

// Viewport returns the box's rendered viewport.
func (b Box) Viewport() Viewport {
	b = b.Normalized()
	if b.Rotate == 90 || b.Rotate == 270 {
		return Viewport{Width: b.Height(), Height: b.Width(), Rotation: b.Rotate}
	}
	return Viewport{Width: b.Width(), Height: b.Height(), Rotation: b.Rotate}
}

// NormRotation folds any degree count onto one of the four the PDF spec
// allows; anything that is not a multiple of 90 rounds to the nearest.
func NormRotation(r int) int {
	m := ((int(math.Round(float64(r)/90))*90)%360 + 360) % 360
	return m
}

// unrotatedSize is the page's size in points before /Rotate.
func unrotatedSize(v Viewport) (w, h float64) {
	if v.Rotation == 90 || v.Rotation == 270 {
		return v.Height, v.Width
	}
	return v.Width, v.Height
}

// renderedToPage maps a rendered point (origin top-left, points) to the
// unrotated page's top-left system.
func renderedToPage(X, Y float64, v Viewport) (x, y float64) {
	W, H := v.Width, v.Height
	switch v.Rotation {
	case 90:
		return Y, W - X
	case 180:
		return W - X, H - Y
	case 270:
		return H - Y, X
	default:
		return X, Y
	}
}

// pageToRendered is the inverse of renderedToPage.
func pageToRendered(x, y float64, v Viewport) (X, Y float64) {
	W, H := v.Width, v.Height
	switch v.Rotation {
	case 90:
		return W - y, x
	case 180:
		return W - x, H - y
	case 270:
		return y, H - x
	default:
		return x, y
	}
}

// FracToPDF converts fractions of the rendered page into a rectangle in
// the unrotated page's own coordinates (origin at the visible area's
// bottom-left, points). Use ToRect for a /Rect that includes the CropBox
// offset.
func FracToPDF(f Frac, v Viewport) Rect {
	ax, ay := renderedToPage(f.X*v.Width, f.Y*v.Height, v)
	bx, by := renderedToPage((f.X+f.W)*v.Width, (f.Y+f.H)*v.Height, v)
	_, ph := unrotatedSize(v)
	left, right := math.Min(ax, bx), math.Max(ax, bx)
	top, bottom := math.Min(ay, by), math.Max(ay, by)
	return round(Rect{X: left, Y: ph - bottom, W: right - left, H: bottom - top})
}

// PDFToFrac is the inverse of FracToPDF (page-local rectangle in).
func PDFToFrac(r Rect, v Viewport) Frac {
	_, ph := unrotatedSize(v)
	tlx, tly := r.X, ph-(r.Y+r.H)
	brx, bry := r.X+r.W, ph-r.Y
	aX, aY := pageToRendered(tlx, tly, v)
	bX, bY := pageToRendered(brx, bry, v)
	W, H := v.Width, v.Height
	if W == 0 {
		W = 1
	}
	if H == 0 {
		H = 1
	}
	return roundFrac(Frac{
		X: math.Min(aX, bX) / W, Y: math.Min(aY, bY) / H,
		W: math.Abs(aX-bX) / W, H: math.Abs(aY-bY) / H,
	})
}

// ToRect converts a field's fractions into the /Rect a widget on that page
// needs: FracToPDF plus the visible area's origin, so a CropBox that does
// not start at 0,0 still lands where the person put the box.
func ToRect(f Frac, b Box) Rect {
	b = b.Normalized()
	r := FracToPDF(f, b.Viewport())
	r.X += b.X0
	r.Y += b.Y0
	return round(r)
}

// FromRect is the inverse of ToRect.
func FromRect(r Rect, b Box) Frac {
	b = b.Normalized()
	r.X -= b.X0
	r.Y -= b.Y0
	return PDFToFrac(r, b.Viewport())
}

// Clamp keeps a fraction box inside the page and at least the editor's
// minimum size (2 % × 1 %), mirroring the frontend's clampFrac.
func Clamp(f Frac) Frac {
	const minW, minH = 0.02, 0.01
	w := math.Min(1, math.Max(minW, orZero(f.W)))
	h := math.Min(1, math.Max(minH, orZero(f.H)))
	x := math.Min(1-w, clamp01(orZero(f.X)))
	y := math.Min(1-h, clamp01(orZero(f.Y)))
	return Frac{X: x, Y: y, W: w, H: h}
}

func orZero(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

func clamp01(v float64) float64 { return math.Min(1, math.Max(0, v)) }

func round(r Rect) Rect {
	return Rect{X: r6(r.X), Y: r6(r.Y), W: r6(r.W), H: r6(r.H)}
}

func roundFrac(f Frac) Frac {
	return Frac{X: r6(f.X), Y: r6(f.Y), W: r6(f.W), H: r6(f.H)}
}

func r6(v float64) float64 { return math.Round(v*1e6) / 1e6 }
