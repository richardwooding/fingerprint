package fingerprint_test

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"testing"

	"github.com/richardwooding/fingerprint"
)

// The pixel values iscc-sdk's own tests publish for these two files — the only
// oracle there is for the normalisation, since ISO 24138's conformance vectors
// begin after it. See testdata/README.md.
var (
	isccDemoPNGHead = []byte{25, 18, 14, 15, 25, 80, 92, 92, 106, 68, 110, 100, 99, 93}
	isccDemoPNGTail = []byte{68, 65, 71, 60, 65, 65, 66, 65, 61, 65, 54, 62, 50, 52}
	isccDemoJPGHead = []byte{25, 18, 14, 15, 25, 79, 92, 92, 106, 68, 110, 101, 99, 93}
	isccDemoJPGTail = []byte{67, 65, 71, 59, 65, 65, 66, 65, 61, 66, 54, 62, 50, 52}
)

func loadImage(t testing.TB, name string) image.Image {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	defer func() { _ = f.Close() }()
	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return img
}

// TestISCCPixelsMatchesReferencePNG is the conformance test. Every one of the
// 28 published values must match byte for byte: this is a re-implementation of
// Pillow's arithmetic, and "close" is a different code.
func TestISCCPixelsMatchesReferencePNG(t *testing.T) {
	got := fingerprint.ISCCPixels(loadImage(t, "iscc_demo.png"))
	if len(got) != 1024 {
		t.Fatalf("got %d pixels, want 1024", len(got))
	}
	if !bytes.Equal(got[:14], isccDemoPNGHead) {
		t.Errorf("first 14 pixels = %v, reference says %v", got[:14], isccDemoPNGHead)
	}
	if !bytes.Equal(got[1010:], isccDemoPNGTail) {
		t.Errorf("last 14 pixels = %v, reference says %v", got[1010:], isccDemoPNGTail)
	}
}

// TestISCCPixelsJPEGDecoderDrift pins the one place we cannot be byte-exact,
// and bounds it. Go's image/jpeg and libjpeg round YCbCr→RGB differently, so a
// few normalised pixels land one off — a DECODER difference, not a
// normalisation one, which the PNG's exactness above proves. The bound is what
// matters: ±1, and only on a handful of pixels.
func TestISCCPixelsJPEGDecoderDrift(t *testing.T) {
	got := fingerprint.ISCCPixels(loadImage(t, "iscc_demo.jpg"))
	check := func(label string, got, want []byte) {
		var off int
		for i := range want {
			d := int(got[i]) - int(want[i])
			switch {
			case d > 1 || d < -1:
				t.Errorf("%s pixel %d = %d, reference says %d — more than the decoder's ±1", label, i, got[i], want[i])
			case d != 0:
				off++
			}
		}
		if off > 8 {
			t.Errorf("%s: %d of %d pixels differ; the drift should be a handful", label, off, len(want))
		}
		t.Logf("%s: %d of %d pixels differ by 1", label, off, len(want))
	}
	check("head", got[:14], isccDemoJPGHead)
	check("tail", got[1010:], isccDemoJPGTail)
}

// TestISCCPixelsSameSceneSameNeighbourhood is the property the whole exercise
// is for: two encodings of one photograph must normalise to nearly the same
// bytes, since that is what lets a re-encoded copy be recognised.
func TestISCCPixelsSameSceneSameNeighbourhood(t *testing.T) {
	fromPNG := fingerprint.ISCCPixels(loadImage(t, "iscc_demo.png"))
	fromJPG := fingerprint.ISCCPixels(loadImage(t, "iscc_demo.jpg"))
	var diff, worst int
	for i := range fromPNG {
		d := int(fromPNG[i]) - int(fromJPG[i])
		if d < 0 {
			d = -d
		}
		if d > 0 {
			diff++
		}
		if d > worst {
			worst = d
		}
	}
	t.Logf("%d of 1024 pixels differ, worst by %d", diff, worst)
	if worst > 4 {
		t.Errorf("worst per-pixel difference between encodings is %d; a perceptual hash needs them close", worst)
	}
}

// TestISCCPixelsGrid pins the output shape and that it is not accidentally
// uniform — a normaliser that returned 1024 identical bytes would pass a
// length check and produce a useless code.
func TestISCCPixelsGrid(t *testing.T) {
	got := fingerprint.ISCCPixels(loadImage(t, "iscc_demo.png"))
	if len(got) != 1024 {
		t.Fatalf("got %d pixels, want 1024", len(got))
	}
	seen := map[byte]bool{}
	for _, p := range got {
		seen[p] = true
	}
	if len(seen) < 16 {
		t.Errorf("only %d distinct values in the thumbnail; the resample looks broken", len(seen))
	}
}

// TestISCCPixelsFlattensTransparencyToWhite covers the step the photographs
// cannot: a transparent image must normalise as a viewer sees it, on white.
func TestISCCPixelsFlattensTransparencyToWhite(t *testing.T) {
	// Fully transparent, with black in the undefined colour channels: composited
	// onto white this is white, and read naively it would be black.
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{R: 0, G: 0, B: 0, A: 0})
		}
	}
	got := fingerprint.ISCCPixels(img)
	for i, p := range got {
		if p != 255 {
			t.Fatalf("pixel %d = %d, want 255: transparency must composite onto white", i, p)
		}
	}
}

// TestISCCPixelsTrimsUniformBorder covers the other step the photographs miss.
// The same content, one copy framed in a wide border, must normalise to nearly
// the same bytes — that is what trimming is for.
func TestISCCPixelsTrimsUniformBorder(t *testing.T) {
	content := func() *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, 64, 64))
		for y := 0; y < 64; y++ {
			for x := 0; x < 64; x++ {
				img.Set(x, y, color.RGBA{R: uint8(x * 4), G: uint8(y * 4), B: 128, A: 255})
			}
		}
		return img
	}
	bare := content()

	framed := image.NewRGBA(image.Rect(0, 0, 96, 96))
	for y := 0; y < 96; y++ {
		for x := 0; x < 96; x++ {
			framed.Set(x, y, color.RGBA{R: 7, G: 7, B: 7, A: 255})
		}
	}
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			framed.Set(16+x, 16+y, bare.At(x, y))
		}
	}

	a, b := fingerprint.ISCCPixels(bare), fingerprint.ISCCPixels(framed)
	var worst int
	for i := range a {
		d := int(a[i]) - int(b[i])
		if d < 0 {
			d = -d
		}
		if d > worst {
			worst = d
		}
	}
	t.Logf("worst per-pixel difference after trimming a 16px border: %d", worst)
	if worst > 8 {
		t.Errorf("a uniform border changed the normalised image by %d; it should be trimmed away", worst)
	}
}

// TestISCCPixelsUniformImageSurvivesTrim pins that an image which is entirely
// one colour — whose difference bounding box is empty — is left alone rather
// than cropped to nothing.
func TestISCCPixelsUniformImageSurvivesTrim(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 40, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 40; x++ {
			img.Set(x, y, color.RGBA{R: 9, G: 9, B: 9, A: 255})
		}
	}
	got := fingerprint.ISCCPixels(img)
	if len(got) != 1024 {
		t.Fatalf("got %d pixels, want 1024", len(got))
	}
	for i, p := range got {
		if p != got[0] {
			t.Fatalf("pixel %d = %d but pixel 0 = %d; a uniform image must stay uniform", i, p, got[0])
		}
	}
}

// TestISCCPixelsFromReaderMatchesDecoded pins that the reader entry point
// agrees with the decoded one on an image with no EXIF orientation.
func TestISCCPixelsFromReaderMatchesDecoded(t *testing.T) {
	data, err := os.ReadFile("testdata/iscc_demo.png")
	if err != nil {
		t.Fatal(err)
	}
	fromReader, err := fingerprint.ISCCPixelsFromReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ISCCPixelsFromReader: %v", err)
	}
	if !bytes.Equal(fromReader, fingerprint.ISCCPixels(loadImage(t, "iscc_demo.png"))) {
		t.Error("the reader and the image entry points disagree")
	}
}

// TestISCCPixelsFromReaderRejectsRubbish pins the error path: an undecodable
// input is an error, not a panic and not 1024 zeroes.
func TestISCCPixelsFromReaderRejectsRubbish(t *testing.T) {
	for _, in := range [][]byte{nil, []byte("not an image"), {0xFF, 0xD8, 0xFF}} {
		if _, err := fingerprint.ISCCPixelsFromReader(bytes.NewReader(in)); err == nil {
			t.Errorf("%q decoded", in)
		}
	}
}

// TestISCCPixelsNeverPanics feeds the pipeline the awkward shapes: one pixel,
// one row, one column, and an empty image.
func TestISCCPixelsNeverPanics(t *testing.T) {
	for _, r := range []image.Rectangle{
		image.Rect(0, 0, 1, 1),
		image.Rect(0, 0, 1, 64),
		image.Rect(0, 0, 64, 1),
		image.Rect(0, 0, 0, 0),
		image.Rect(0, 0, 3, 5),
	} {
		img := image.NewRGBA(r)
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 200, A: 255})
			}
		}
		got := fingerprint.ISCCPixels(img)
		if r.Dx() > 0 && r.Dy() > 0 && len(got) != 1024 {
			t.Errorf("%v gave %d pixels, want 1024", r, len(got))
		}
	}
}

// encodePNG is a helper for the EXIF tests, which need real file bytes.
func encodePNG(t testing.TB, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestISCCPixelsFromReaderPNGHasNoOrientation pins that a PNG is not searched
// for an orientation it cannot carry.
func TestISCCPixelsFromReaderPNGHasNoOrientation(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 32, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 32; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 8), A: 255})
		}
	}
	data := encodePNG(t, img)
	got, err := fingerprint.ISCCPixelsFromReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, fingerprint.ISCCPixels(img)) {
		t.Error("a PNG was transposed")
	}
}

// exifJPEG encodes img as a JPEG carrying an EXIF orientation tag, by splicing
// a minimal APP1 segment in after SOI — which is where a camera puts it.
func exifJPEG(t testing.TB, img image.Image, orientation uint16) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	base := buf.Bytes()

	// A TIFF header (little-endian) plus one IFD entry: tag 0x0112
	// (Orientation), type 3 (SHORT), count 1, value.
	tiff := []byte{'I', 'I', 42, 0, 8, 0, 0, 0, 1, 0}
	entry := []byte{0x12, 0x01, 3, 0, 1, 0, 0, 0, 0, 0, 0, 0}
	binary.LittleEndian.PutUint16(entry[8:], orientation)
	tiff = append(tiff, entry...)
	tiff = append(tiff, 0, 0, 0, 0) // no next IFD

	payload := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	seg = append(seg, payload...)

	out := append([]byte{}, base[:2]...) // SOI
	out = append(out, seg...)
	return append(out, base[2:]...)
}

// TestISCCPixelsFromReaderAppliesOrientation pins the step the standard puts
// FIRST: a photograph stored rotated with an orientation flag must normalise
// the way it is meant to be seen. Orientation 6 is the common one — a phone
// held upright, stored landscape.
func TestISCCPixelsFromReaderAppliesOrientation(t *testing.T) {
	// Asymmetric in both axes, so a wrong rotation cannot look right.
	src := image.NewRGBA(image.Rect(0, 0, 64, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 64; x++ {
			src.Set(x, y, color.RGBA{R: uint8(x * 3), G: uint8(y * 6), B: 40, A: 255})
		}
	}

	// What the viewer should see for orientation 6: the source rotated 90° CW.
	rotated := image.NewRGBA(image.Rect(0, 0, 40, 64))
	for y := 0; y < 40; y++ {
		for x := 0; x < 64; x++ {
			rotated.Set(40-1-y, x, src.At(x, y))
		}
	}

	upright, err := fingerprint.ISCCPixelsFromReader(bytes.NewReader(exifJPEG(t, src, 6)))
	if err != nil {
		t.Fatal(err)
	}
	ignored, err := fingerprint.ISCCPixelsFromReader(bytes.NewReader(exifJPEG(t, src, 1)))
	if err != nil {
		t.Fatal(err)
	}

	// The transposed result must be much closer to the rotated reference than
	// the untransposed one is. Exact equality is not available: the JPEG round
	// trip perturbs the pixels either way.
	want := fingerprint.ISCCPixels(rotated)
	near := func(a, b []byte) int {
		var worst int
		for i := range a {
			d := int(a[i]) - int(b[i])
			if d < 0 {
				d = -d
			}
			if d > worst {
				worst = d
			}
		}
		return worst
	}
	withTranspose, without := near(upright, want), near(ignored, want)
	t.Logf("worst difference from the rotated reference: transposed %d, untransposed %d", withTranspose, without)
	if withTranspose > 6 {
		t.Errorf("orientation 6 was not applied: still %d off the rotated reference", withTranspose)
	}
	if without <= withTranspose {
		t.Errorf("transposing made no difference (%d vs %d) — the tag is being ignored", withTranspose, without)
	}
}

// TestJPEGOrientationTolerance pins that a malformed or absent EXIF is not an
// error: the image still normalises, just untransposed.
func TestJPEGOrientationTolerance(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 32, 24))
	for y := 0; y < 24; y++ {
		for x := 0; x < 32; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 7), B: uint8(y * 9), A: 255})
		}
	}
	plain := exifJPEG(t, img, 1)
	want, err := fingerprint.ISCCPixelsFromReader(bytes.NewReader(plain))
	if err != nil {
		t.Fatal(err)
	}

	// An orientation out of range, and a truncated APP1 payload.
	for name, data := range map[string][]byte{
		"orientation 9":  exifJPEG(t, img, 9),
		"orientation 0":  exifJPEG(t, img, 0),
		"truncated exif": append(append([]byte{}, plain[:2]...), append([]byte{0xFF, 0xE1, 0x00, 0x08, 'E', 'x', 'i', 'f', 0x00, 0x00}, plain[2:]...)...),
	} {
		got, err := fingerprint.ISCCPixelsFromReader(bytes.NewReader(data))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s changed the normalised image; it should be ignored", name)
		}
	}
}
