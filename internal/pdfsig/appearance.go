package pdfsig

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"

	_ "image/jpeg" // uploads from the pad may be JPEG

	"golang.org/x/image/draw"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"

	"github.com/brf-tech/filex-sign/internal/fontkit"
	"github.com/brf-tech/filex-sign/internal/geometry"
)

// ── Appearance ─────────────────────────────────────────────────────────
//
// The visible signature is ONE image: the signer's drawing fitted into the
// upper part of the field, the name and the date rasterised underneath.
// Rasterising the text (Go Regular, a Unicode font) side-steps the
// core-14 font problem — "Şahin" comes out as "Şahin", not as "?ahin" —
// at the cost of the words not being selectable text in the PDF. The
// layout is pure arithmetic (Layout) so it can be checked without drawing.

// Pixels per PDF point for the composed image. 4 keeps a 150 × 50 pt field
// crisp on print (600 × 200 px) without bloating the file.
const scalePxPerPt = 4.0

// maxCanvasPx caps the longer side of the composed image.
const maxCanvasPx = 1600

// Layout is where each part of the appearance lands, in canvas pixels.
type Layout struct {
	CanvasW, CanvasH int
	// Image is the drawing's box (empty when there is no drawing).
	Image image.Rectangle
	// Lines are the text lines under it, top to bottom: a font size in px
	// and a baseline (y, from the top).
	Lines []LineBox
	// TextLeft is the x where text starts (a small inset).
	TextLeft int
}

// LineBox is one line of text in the band under the drawing.
type LineBox struct {
	Size float64
	Base int
}

// bandShare is how much of the height the text takes under a drawing, by
// the number of lines. ⚠ Two lines keep exactly the third they always had
// (the name and the date, today's default), so a signature made with the
// default choice looks the way every earlier one did; more lines take more
// of the box, never more than 60 %, so the drawing is always the larger
// part of a signature.
func bandShare(n int) float64 {
	switch {
	case n <= 0:
		return 0
	case n == 1:
		return 0.24
	case n == 2:
		return 0.34
	case n == 3:
		return 0.44
	case n == 4:
		return 0.52
	}
	return 0.60
}

// LayoutFor computes the appearance geometry for a field rectW × rectH points
// holding a drawing of imgW × imgH pixels (0 × 0 = no drawing) and n lines
// of text, at the default scale.
func LayoutFor(rectW, rectH float64, imgW, imgH, n int) Layout {
	return layoutAt(rectW, rectH, imgW, imgH, n, scalePxPerPt)
}

func layoutAt(rectW, rectH float64, imgW, imgH, n int, scale float64) Layout {
	if rectW <= 0 || rectH <= 0 {
		return Layout{}
	}
	if scale <= 0 {
		scale = scalePxPerPt
	}
	if longer := math.Max(rectW, rectH) * scale; longer > maxCanvasPx {
		scale = maxCanvasPx / math.Max(rectW, rectH)
	}
	cw := int(math.Round(rectW * scale))
	ch := int(math.Round(rectH * scale))
	if cw < 8 {
		cw = 8
	}
	if ch < 8 {
		ch = 8
	}
	l := Layout{CanvasW: cw, CanvasH: ch}
	pad := int(math.Round(float64(ch) * 0.04))
	l.TextLeft = pad * 2
	hasImage := imgW > 0 && imgH > 0
	bandTop := 0
	if hasImage {
		bandTop = int(math.Round(float64(ch) * (1 - bandShare(n))))
	}
	band := float64(ch - bandTop)
	if n > 0 && band > 0 {
		// The first line is a little larger when there is more than one:
		// it is the name by default, and a name is what a reader looks for.
		weights := make([]float64, n)
		total := 0.0
		for i := range weights {
			weights[i] = 1
			if i == 0 && n > 1 {
				weights[i] = 1.3
			}
			total += weights[i]
		}
		top := float64(bandTop)
		for i := range weights {
			h := band * weights[i] / total
			l.Lines = append(l.Lines, LineBox{Size: h * 0.78, Base: int(math.Round(top + h*0.80))})
			top += h
		}
	}
	if hasImage {
		// Fit the drawing into the area above the band, aspect preserved.
		areaW := float64(cw - 2*pad)
		areaH := float64(bandTop - 2*pad)
		if areaW > 0 && areaH > 0 {
			s := math.Min(areaW/float64(imgW), areaH/float64(imgH))
			w := int(math.Round(float64(imgW) * s))
			h := int(math.Round(float64(imgH) * s))
			x := (cw - w) / 2
			y := pad + int(math.Round((areaH-float64(h))/2))
			l.Image = image.Rect(x, y, x+w, y+h)
		}
	}
	return l
}

var inkColor = color.NRGBA{R: 0x1a, G: 0x23, B: 0x6e, A: 0xff} // ballpoint blue
var textColor = color.NRGBA{R: 0x22, G: 0x22, B: 0x22, A: 0xff}

// Compose draws the appearance for a field rect (points): the drawing
// (PNG/JPEG bytes, may be nil) with the given lines of text under it —
// stamp.Lines, already worded. It returns PNG bytes with a transparent
// background.
//
// ⚠ The SAME function draws the preview a signer approves and the picture
// that goes into the PDF, so the two cannot disagree about what is printed
// or where: a preview is only drawn at a smaller scale (ComposeScaled).
func Compose(rect geometry.Rect, drawing []byte, lines []string) ([]byte, error) {
	return ComposeScaled(rect, drawing, lines, scalePxPerPt)
}

// ComposeScaled is Compose at another resolution (pixels per point).
func ComposeScaled(rect geometry.Rect, drawing []byte, lines []string, pxPerPt float64) ([]byte, error) {
	var img image.Image
	if len(drawing) > 0 {
		decoded, _, err := image.Decode(bytes.NewReader(drawing))
		if err != nil {
			return nil, errors.New("the signature image is unreadable")
		}
		img = decoded
	}
	iw, ih := 0, 0
	if img != nil {
		iw, ih = img.Bounds().Dx(), img.Bounds().Dy()
	}
	var kept []string
	for _, ln := range lines {
		if ln != "" {
			kept = append(kept, ln)
		}
	}
	l := layoutAt(rect.W, rect.H, iw, ih, len(kept), pxPerPt)
	if l.CanvasW == 0 {
		return nil, errors.New("the signature field is too small")
	}
	canvas := image.NewNRGBA(image.Rect(0, 0, l.CanvasW, l.CanvasH))
	if img != nil && !l.Image.Empty() {
		draw.CatmullRom.Scale(canvas, l.Image, img, img.Bounds(), draw.Over, nil)
	}
	for i, ln := range kept {
		if i >= len(l.Lines) {
			break
		}
		if err := drawLine(canvas, ln, l.Lines[i].Size, l.TextLeft, l.Lines[i].Base, textColor); err != nil {
			return nil, err
		}
	}
	var out bytes.Buffer
	// ⚠⚠ DefaultCompression, not BestCompression. Profiled 2026-09-21: the
	// best level was 92% of a composition — 82 ms of it natively at the
	// approve step's resolution, ten times that inside the wasm runtime, per
	// signature box — and the signer's "Next" to the approve step went from
	// 2.5 s to 4.6 s when that step began to show the stamped picture. The
	// default level is 4–8× faster for a file ~4% larger; the picture is a
	// signature and three lines of text, not an archive.
	enc := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := enc.Encode(&out, canvas); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// drawLine rasterises one line, shrinking the size until it fits the
// canvas width (never below 6 px), and truncating with "…" as a last resort.
//
// ⚠⚠ Laid out by fontkit.LayoutLine, like the text the PDF itself carries:
// a signer's name in Japanese, Arabic or Hindi is drawn in the Noto face for
// its script (fetched on demand), shaped and in its own direction, instead
// of the letters Go Regular does not have coming out as nothing
// (2026-09-21). A right-to-left line starts at the right edge.
func drawLine(dst *image.NRGBA, text string, size float64, x, baseline int, c color.Color) error {
	if text == "" {
		return nil
	}
	maxW := float64(dst.Bounds().Dx() - x - x/2)
	line, err := fontkit.LayoutLine(fontkit.StampFace(), text)
	if err != nil {
		return err
	}
	w1 := line.Width(1) // width at 1 px: the line scales with its size
	for size > 6 && w1*size > maxW {
		size *= 0.85
	}
	if size < 6 {
		size = 6
	}
	for w1*size > maxW && len([]rune(text)) > 1 {
		r := []rune(text)
		text = string(r[:len(r)-2]) + "…"
		if line, err = fontkit.LayoutLine(fontkit.StampFace(), text); err != nil {
			return err
		}
		w1 = line.Width(1)
	}
	left := float64(x)
	if line.RTL {
		left = float64(dst.Bounds().Dx()-x) - w1*size
	}
	return rasterise(dst, line.Runs, size, left, float64(baseline), c)
}

// rasterise fills the glyph outlines of runs at size px, the first glyph's
// pen at (x, baseline).
func rasterise(dst *image.NRGBA, runs []fontkit.Run, size, x, baseline float64, c color.Color) error {
	b := dst.Bounds()
	var z vector.Rasterizer
	z.Reset(b.Dx(), b.Dy())
	z.DrawOp = draw.Over
	pen := x
	open := false
	for _, run := range runs {
		for _, g := range run.Glyphs {
			segs, err := run.Face.Segments(g.GID, size)
			if err != nil {
				return err
			}
			ox := float32(pen + g.XOffset*size/1000)
			oy := float32(baseline - g.YOffset*size/1000)
			pt := func(p fixed.Point26_6) (float32, float32) {
				return ox + float32(p.X)/64, oy + float32(p.Y)/64
			}
			for _, s := range segs {
				switch s.Op {
				case sfnt.SegmentOpMoveTo:
					if open {
						z.ClosePath()
					}
					z.MoveTo(pt(s.Args[0]))
					open = true
				case sfnt.SegmentOpLineTo:
					z.LineTo(pt(s.Args[0]))
				case sfnt.SegmentOpQuadTo:
					ax, ay := pt(s.Args[0])
					bx, by := pt(s.Args[1])
					z.QuadTo(ax, ay, bx, by)
				case sfnt.SegmentOpCubeTo:
					ax, ay := pt(s.Args[0])
					bx, by := pt(s.Args[1])
					cx, cy := pt(s.Args[2])
					z.CubeTo(ax, ay, bx, by, cx, cy)
				}
			}
			pen += g.Advance * size / 1000
		}
	}
	if open {
		z.ClosePath()
	}
	z.Draw(dst, b, image.NewUniform(c), image.Point{})
	return nil
}

// ── Pad image normalisation ────────────────────────────────────────────

// PadMaxW / PadMaxH are the signature-pad's own bounds (CSS px); anything
// larger is an upload and is fitted into that box.
const (
	PadMaxW = 600
	PadMaxH = 200
)

// Normalize decodes a pad image (PNG/JPEG), fits it into the pad's box and
// re-encodes it as PNG no larger than budget bytes (shrinking further when
// needed). It is what a view or page runs before handing the image to a
// job through params, which filex caps at 64 KiB.
func Normalize(raw []byte, budget int) ([]byte, error) {
	if len(raw) == 0 {
		return nil, errors.New("no signature image")
	}
	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("the signature image is unreadable")
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	if w == 0 || h == 0 {
		return nil, errors.New("the signature image is empty")
	}
	// ⚠⚠ A PNG that already fits — the pad's own drawing, nearly always —
	// is kept as it came. Re-encoding it was the approve step's whole cost:
	// this runs on EVERY event of the signer's screen, and profiled
	// (2026-09-21) it was 96% of the step that shows the stamped picture,
	// because the best compression level ran up to eight times per drawing
	// once two signature boxes shared the job's 64 KiB.
	if format == "png" && w <= PadMaxW && h <= PadMaxH && (budget <= 0 || len(raw) <= budget) {
		return raw, nil
	}
	scale := math.Min(1, math.Min(float64(PadMaxW)/float64(w), float64(PadMaxH)/float64(h)))
	for attempt := 0; attempt < 8; attempt++ {
		tw := int(math.Max(1, math.Round(float64(w)*scale)))
		th := int(math.Max(1, math.Round(float64(h)*scale)))
		var out image.Image = img
		if tw != w || th != h {
			dst := image.NewNRGBA(image.Rect(0, 0, tw, th))
			draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Src, nil)
			out = dst
		} else if _, ok := img.(*image.NRGBA); !ok {
			dst := image.NewNRGBA(img.Bounds())
			draw.Draw(dst, dst.Bounds(), img, img.Bounds().Min, draw.Src)
			out = dst
		}
		var buf bytes.Buffer
		// The default level: see ComposeScaled. The budget is checked on
		// what was actually written, so a few percent more at worst costs
		// one more, much cheaper, turn of this loop.
		enc := png.Encoder{CompressionLevel: png.DefaultCompression}
		if err := enc.Encode(&buf, out); err != nil {
			return nil, err
		}
		if budget <= 0 || buf.Len() <= budget || tw <= 120 {
			return buf.Bytes(), nil
		}
		scale *= 0.75
	}
	return nil, errors.New("the signature image is too large")
}
