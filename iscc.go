package fingerprint

import (
	"image"
	"image/color"
	"image/draw"
	"io"
	"math"
)

// ISO 24138 (ISCC) image normalisation — the step that has to happen before an
// ISCC Image-Code can be computed, and the one no Go library provides.
//
// The standard's conformance vectors begin at 1024 already-normalised pixels,
// so they pin the code generation and say nothing about how an image becomes
// those pixels. That normalisation is specified only by the reference
// implementation's behaviour (iscc-sdk, on Pillow), and getting it wrong yields
// a code that is silently non-conformant — the same bits, a different answer.
//
// So this is a deliberate re-implementation of Pillow's arithmetic rather than
// an approximation of it: Rec. 601 luma in the same fixed point, and Pillow's
// two-pass fixed-point bicubic resample, coefficient rounding and all. The
// tests check it against the pixel values the reference publishes for
// testdata/iscc_demo.png, byte for byte.
//
// What this package does NOT do is compute the code. Pair it with
// github.com/iscc/iscc-lib/packages/go, which is the official pure-Go
// implementation of ISO 24138 and needs exactly these bytes:
//
//	pixels, err := fingerprint.ISCCPixelsFromReader(f)
//	code, err := iscc.GenImageCodeV0(pixels, 64)

// isccGrid is the normalised thumbnail's side: ISO 24138 fixes it at 32×32,
// giving the 1024 pixels GenImageCodeV0 consumes.
const isccGrid = 32

// ISCCPixels normalises an already-decoded image to the 1024 grayscale bytes an
// ISO 24138 Image-Code is computed from, in row-major order.
//
// The steps are the standard's, in its order: composite any transparency onto
// white, trim a uniform border, convert to grayscale, and resample to 32×32.
//
// EXIF orientation is NOT applied here — an image.Image carries no EXIF, so
// there is nothing to read. The standard applies the orientation transpose
// FIRST, so a caller decoding a JPEG by hand must transpose before calling
// this, or use ISCCPixelsFromReader, which does it.
func ISCCPixels(img image.Image) []byte {
	flat := isccFlattenAlpha(img)
	trimmed := isccTrimBorder(flat)
	gray := isccGrayscale(trimmed)
	small := isccResample(gray, isccGrid, isccGrid)
	out := make([]byte, 0, isccGrid*isccGrid)
	for y := 0; y < isccGrid; y++ {
		for x := 0; x < isccGrid; x++ {
			out = append(out, small.GrayAt(x, y).Y)
		}
	}
	return out
}

// ISCCPixelsFromReader decodes an image and normalises it, applying the EXIF
// orientation transpose the standard asks for first. Returns an error when the
// image cannot be decoded; an unreadable or absent orientation is not an error,
// it simply means no transpose.
//
// Only the formats registered with image.Decode are supported — this package
// registers GIF, JPEG, PNG, BMP, TIFF and WebP, and a program that registers
// another decoder gets that format here too.
//
// A few inputs are REFUSED rather than fingerprinted, because their pixels
// would not mean what they appear to mean: see tiffRefusal. An animated WebP is
// refused by the decoder itself, so a code is never computed from one arbitrary
// frame.
func ISCCPixelsFromReader(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, &PHashError{msg: "reading image: " + err.Error()}
	}
	if why := tiffRefusal(data); why != "" {
		return nil, &PHashError{msg: "cannot fingerprint this image: " + why}
	}
	img, format, err := image.Decode(bytesReader(data))
	if err != nil {
		return nil, &PHashError{msg: "decoding image: " + err.Error()}
	}
	return ISCCPixels(isccTranspose(img, orientationFor(format, data))), nil
}

// orientationFor reads the EXIF orientation from wherever the format in hand
// keeps it, returning 1 (no transpose) for formats that carry none.
//
// format is image.Decode's own answer rather than a sniff of our own, so the
// orientation can never be read out of a different format from the one that
// produced the pixels.
func orientationFor(format string, data []byte) int {
	switch format {
	case "jpeg":
		return jpegOrientation(data) // an Exif APP1 segment
	case "tiff":
		// The file IS a TIFF header, so its own IFD0 holds the tag.
		if o := tiffOrientation(data); o != 0 {
			return o
		}
	case "webp":
		if o := webpOrientation(data); o != 0 {
			return o
		}
	}
	// PNG's eXIf chunk is a known gap, tracked separately; GIF and BMP have
	// nowhere to put an orientation.
	return 1
}

// isccFlattenAlpha composites an image with transparency onto a white
// background, as the standard requires: a transparent PNG must normalise the
// way a viewer would see it, not the way its undefined colour channels happen
// to be stored.
func isccFlattenAlpha(img image.Image) image.Image {
	if op, ok := img.(interface{ Opaque() bool }); ok && op.Opaque() {
		return img
	}
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(out, out.Bounds(), img, b.Min, draw.Over)
	return out
}

// isccTrimBorder crops a uniform border, the standard's rule being the corner
// pixel's colour: everything differing from the top-left pixel is content, and
// the crop is the bounding box of that difference. An image that is entirely
// one colour has no such box and is left alone.
func isccTrimBorder(img image.Image) image.Image {
	b := img.Bounds()
	bg := img.At(b.Min.X, b.Min.Y)
	br, bgc, bb, ba := bg.RGBA()

	minX, minY := b.Max.X, b.Max.Y
	maxX, maxY := b.Min.X, b.Min.Y
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			if r == br && g == bgc && bl == bb && a == ba {
				continue
			}
			if x < minX {
				minX = x
			}
			if y < minY {
				minY = y
			}
			if x >= maxX {
				maxX = x + 1
			}
			if y >= maxY {
				maxY = y + 1
			}
		}
	}
	if minX >= maxX || minY >= maxY {
		return img
	}
	if minX == b.Min.X && minY == b.Min.Y && maxX == b.Max.X && maxY == b.Max.Y {
		return img
	}
	return imageCrop(img, image.Rect(minX, minY, maxX, maxY))
}

// imageCrop returns the sub-image of r, copying when the concrete type cannot
// slice itself.
func imageCrop(img image.Image, r image.Rectangle) image.Image {
	if sub, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		return sub.SubImage(r)
	}
	out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(out, out.Bounds(), img, r.Min, draw.Src)
	return out
}

// isccGrayscale is Pillow's convert("L") exactly: Rec. 601 luma in 16-bit fixed
// point with round-half-up, NOT the float arithmetic that reads more naturally.
// The constants are Pillow's own (19595/38470/7471 ≈ .299/.587/.114 × 2¹⁶), and
// the +32768 is the rounding term. A float implementation lands one off on some
// pixels, which is one bit of a code away from non-conformance.
func isccGrayscale(img image.Image) *image.Gray {
	b := img.Bounds()
	out := image.NewGray(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			r16, g16, b16, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			r, g, bl := int(r16>>8), int(g16>>8), int(b16>>8)
			out.SetGray(x, y, color.Gray{Y: uint8((19595*r + 38470*g + 7471*bl + 32768) >> 16)})
		}
	}
	return out
}

// isccPrecisionBits is Pillow's PRECISION_BITS for 8-bit channels: 32 - 8 - 2,
// leaving room for the accumulator not to overflow an int32.
const isccPrecisionBits = 32 - 8 - 2

// isccBicubic is the filter Pillow's BICUBIC uses: the Keys cubic with
// a = -0.5, support 2 — the same kernel as x/image's CatmullRom
// (Mitchell-Netravali B=0, C=0.5). The kernel is not where implementations
// differ; the fixed-point accumulation below is.
func isccBicubic(x float64) float64 {
	const a = -0.5
	if x < 0 {
		x = -x
	}
	switch {
	case x < 1:
		return ((a+2)*x-(a+3))*x*x + 1
	case x < 2:
		return (((x-5)*x+8)*x - 4) * a
	}
	return 0
}

// isccFilter is one axis' resampling plan: per output pixel, the input range it
// draws from and the fixed-point weights to draw with.
type isccFilter struct {
	weights [][]int32
	offset  []int
	count   []int
}

// isccCoefficients builds Pillow's coefficients for one axis. The support
// widens with the downscale factor (a 200→32 reduction reads 27 input pixels
// per output pixel), the weights are normalised to sum to one in float, and
// only then rounded to fixed point — rounding first would drift.
func isccCoefficients(inSize, outSize int) isccFilter {
	scale := float64(inSize) / float64(outSize)
	filterScale := math.Max(scale, 1)
	support := 2.0 * filterScale
	ksize := int(math.Ceil(support))*2 + 1

	f := isccFilter{
		weights: make([][]int32, outSize),
		offset:  make([]int, outSize),
		count:   make([]int, outSize),
	}
	for xx := 0; xx < outSize; xx++ {
		center := (float64(xx) + 0.5) * scale
		xmin := int(center - support + 0.5)
		if xmin < 0 {
			xmin = 0
		}
		xmax := int(center + support + 0.5)
		if xmax > inSize {
			xmax = inSize
		}
		xmax -= xmin

		row := make([]float64, ksize)
		var total float64
		for x := 0; x < xmax; x++ {
			w := isccBicubic((float64(x+xmin) - center + 0.5) / filterScale)
			row[x] = w
			total += w
		}
		if total != 0 {
			for x := 0; x < xmax; x++ {
				row[x] /= total
			}
		}
		fixed := make([]int32, ksize)
		for x := 0; x < ksize; x++ {
			v := row[x] * (1 << isccPrecisionBits)
			if v < 0 {
				fixed[x] = int32(v - 0.5)
			} else {
				fixed[x] = int32(v + 0.5)
			}
		}
		f.weights[xx], f.offset[xx], f.count[xx] = fixed, xmin, xmax
	}
	return f
}

// isccClip8 finishes one accumulated pixel: shift out the fixed-point scale and
// clamp, as Pillow's clip8 does.
func isccClip8(v int32) uint8 {
	v >>= isccPrecisionBits
	switch {
	case v <= 0:
		return 0
	case v >= 255:
		return 255
	}
	return uint8(v)
}

// isccResample is Pillow's resample: horizontal pass, then vertical, with the
// intermediate ROUNDED BACK TO BYTES between them. That intermediate rounding
// is load-bearing — accumulating both passes in floating point, as a natural Go
// implementation would, gives different bytes and so a different code.
func isccResample(src *image.Gray, outW, outH int) *image.Gray {
	inW, inH := src.Bounds().Dx(), src.Bounds().Dy()

	horizontal := isccCoefficients(inW, outW)
	mid := image.NewGray(image.Rect(0, 0, outW, inH))
	for y := 0; y < inH; y++ {
		for xx := 0; xx < outW; xx++ {
			acc := int32(1) << (isccPrecisionBits - 1)
			w, off, n := horizontal.weights[xx], horizontal.offset[xx], horizontal.count[xx]
			for x := 0; x < n; x++ {
				acc += int32(src.GrayAt(off+x, y).Y) * w[x]
			}
			mid.SetGray(xx, y, color.Gray{Y: isccClip8(acc)})
		}
	}

	vertical := isccCoefficients(inH, outH)
	out := image.NewGray(image.Rect(0, 0, outW, outH))
	for yy := 0; yy < outH; yy++ {
		w, off, n := vertical.weights[yy], vertical.offset[yy], vertical.count[yy]
		for xx := 0; xx < outW; xx++ {
			acc := int32(1) << (isccPrecisionBits - 1)
			for y := 0; y < n; y++ {
				acc += int32(mid.GrayAt(xx, off+y).Y) * w[y]
			}
			out.SetGray(xx, yy, color.Gray{Y: isccClip8(acc)})
		}
	}
	return out
}
