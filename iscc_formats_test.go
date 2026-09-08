package fingerprint_test

import (
	"bytes"
	"encoding/binary"
	"image"
	"os"
	"sort"
	"testing"

	"github.com/richardwooding/fingerprint"
	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
)

// The formats this package gained after PNG/JPEG/GIF, and the evidence that
// each one produces the RIGHT pixels rather than merely some pixels.
//
// An ISCC is a matching identifier: a code computed here has to equal the code
// computed by anyone else from the same content, or it is worse than useless.
// The oracle in iscc_test.go pins the PNG against iscc-sdk's published values,
// and the argument here rests on that: for a LOSSLESS encoding there is only
// one correct answer, so a lossless decoder must reproduce the PNG's pixels
// EXACTLY. Three independent codecs agreeing with the reference is stronger
// evidence than any single one of them.

// TestISCCPixelsWebPLosslessMatchesReference is the conformance test for WebP,
// and the strongest single piece of evidence in this file: libwebp 1.6.0
// encoded testdata/iscc_demo.lossless.webp from the very PNG whose normalised
// pixels iscc-sdk publishes, x/image decoded it, and the result matches those
// published values byte for byte.
func TestISCCPixelsWebPLosslessMatchesReference(t *testing.T) {
	got := isccPixelsOf(t, "iscc_demo.lossless.webp")
	want := isccPixelsOf(t, "iscc_demo.png")
	if !bytes.Equal(got, want) {
		t.Fatalf("lossless WebP differs from the PNG oracle:\n got %v\nwant %v", got[:14], want[:14])
	}
	// Belt and braces: the same 28 published values the PNG test asserts.
	if !bytes.Equal(got[:14], isccDemoPNGHead) || !bytes.Equal(got[1010:], isccDemoPNGTail) {
		t.Error("lossless WebP does not match the published reference values")
	}
}

// TestISCCPixelsLosslessEncodingsAgree round-trips the reference image through
// Go's own TIFF and BMP encoders. Both are lossless, so anything but an exact
// match is a decoder bug — and unlike a checked-in fixture, this cannot drift
// away from the oracle behind our backs.
func TestISCCPixelsLosslessEncodingsAgree(t *testing.T) {
	want := isccPixelsOf(t, "iscc_demo.png")
	src := loadImage(t, "iscc_demo.png")

	encoders := map[string]func(*bytes.Buffer) error{
		"tiff uncompressed": func(b *bytes.Buffer) error { return tiff.Encode(b, src, nil) },
		"tiff deflate": func(b *bytes.Buffer) error {
			return tiff.Encode(b, src, &tiff.Options{Compression: tiff.Deflate})
		},
		"tiff deflate + predictor": func(b *bytes.Buffer) error {
			return tiff.Encode(b, src, &tiff.Options{Compression: tiff.Deflate, Predictor: true})
		},
		"bmp": func(b *bytes.Buffer) error { return bmp.Encode(b, src) },
	}
	for name, encode := range encoders {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := encode(&buf); err != nil {
				t.Fatalf("encode: %v", err)
			}
			got, err := fingerprint.ISCCPixelsFromReader(bytes.NewReader(buf.Bytes()))
			if err != nil {
				t.Fatalf("ISCCPixelsFromReader: %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s round-trip is not pixel-identical to the PNG", name)
			}
		})
	}
}

// TestISCCPixelsRealWorldTIFF covers the LZW-plus-horizontal-predictor path
// that Go's own encoder cannot write, using upstream's Photoshop TIFF of the
// same photograph.
//
// It is NOT held to exactness, and the reason is worth recording: upstream
// produced demo.tif from demo.jpg rather than from the lossless master, so its
// stored pixels are the JPEG's. Measured when written: 187 of 1024 pixels
// differ from the PNG by exactly 1, and 169 differ from the JPEG by 1. What
// this test is really guarding is the predictor undo — get that wrong and the
// image comes back as streaks, nowhere near a bound of 2.
func TestISCCPixelsRealWorldTIFF(t *testing.T) {
	got := isccPixelsOf(t, "iscc_demo.tif")
	ref := isccPixelsOf(t, "iscc_demo.png")
	off, worst := compareBytes(got, ref)
	t.Logf("real-world LZW TIFF: %d of %d pixels differ, worst %d", off, len(ref), worst)
	if worst > 2 {
		t.Errorf("worst difference %d; a decode this far off is a bug, not a re-render", worst)
	}
}

// TestISCCPixelsLossyWebPSurvivesCompression is the demonstration, not a
// conformance check. testdata/iscc_demo.lossy.webp is 5 KB where the JPEG of
// the same photograph is 35 KB — seven times smaller — and nearly every
// normalised pixel moves as a result. What must NOT move is the perceptual
// neighbourhood, which is the entire premise of a soft binding.
//
// The pixel bound here is a canary for a decode bug, not a conformance claim;
// the assertion that the ISCC itself is unchanged lives in c2pa-mcp, which is
// where this package meets iscc-lib.
func TestISCCPixelsLossyWebPSurvivesCompression(t *testing.T) {
	lossy := isccPixelsOf(t, "iscc_demo.lossy.webp")
	ref := isccPixelsOf(t, "iscc_demo.png")
	off, worst := compareBytes(lossy, ref)
	t.Logf("lossy WebP: %d of %d pixels differ, worst %d", off, len(ref), worst)
	if worst > 48 {
		t.Errorf("worst difference %d is beyond lossy compression; suspect the decoder", worst)
	}

	// The perceptual hash is this package's own measure of "the same picture",
	// so it can make the robustness claim without reaching for iscc-lib.
	lossyHash, err := fingerprint.PHash(bytes.NewReader(readFixture(t, "iscc_demo.lossy.webp")))
	if err != nil {
		t.Fatal(err)
	}
	losslessHash, err := fingerprint.PHash(bytes.NewReader(readFixture(t, "iscc_demo.lossless.webp")))
	if err != nil {
		t.Fatal(err)
	}
	if d := fingerprint.Distance(lossyHash, losslessHash); d > 4 {
		t.Errorf("pHash distance between the lossy and lossless WebP is %d bits; they are the same picture", d)
	}
}

// TestISCCPixelsRefusesMisleadingTIFFs pins the refusals. Every one of these
// DECODES — x/image returns pixels and no error — and every one would yield a
// code describing something other than the photograph. A wrong ISCC is a claim
// that this content is some other content, so these must be errors.
func TestISCCPixelsRefusesMisleadingTIFFs(t *testing.T) {
	cases := map[string]struct {
		extra []tiffTag
		want  string
	}{
		"planar configuration": {
			extra: []tiffTag{{tag: 0x011C, typ: 3, count: 1, value: 2}},
			want:  "colour planes separately",
		},
		"reduced-resolution preview": {
			extra: []tiffTag{{tag: 0x00FE, typ: 4, count: 1, value: 1}},
			want:  "reduced-resolution preview",
		},
		"dng": {
			extra: []tiffTag{{tag: 0xC612, typ: 1, count: 4, value: 0x00000401}},
			want:  "DNG",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			data := tinyTIFF(t, 24, 16, tc.extra...)
			// The point of the test: the file is perfectly decodable.
			if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
				t.Fatalf("fixture should decode, so that the refusal is ours: %v", err)
			}
			_, err := fingerprint.ISCCPixelsFromReader(bytes.NewReader(data))
			if err == nil {
				t.Fatal("expected a refusal, got a code computed from misleading pixels")
			}
			if !bytes.Contains([]byte(err.Error()), []byte(tc.want)) {
				t.Errorf("error %q should say why (%q)", err, tc.want)
			}
		})
	}

	// A plain chunky full-resolution TIFF is accepted, so the guard is not
	// simply refusing everything.
	if _, err := fingerprint.ISCCPixelsFromReader(bytes.NewReader(tinyTIFF(t, 24, 16))); err != nil {
		t.Errorf("an ordinary TIFF must still be accepted: %v", err)
	}
}

// TestISCCPixelsAppliesTIFFOrientation: a TIFF keeps its orientation in its own
// IFD0, where x/image never looks. ISO 24138 transposes FIRST, so missing this
// would silently produce a code for a rotation of the image.
func TestISCCPixelsAppliesTIFFOrientation(t *testing.T) {
	upright := tinyTIFF(t, 24, 16)
	rotated := tinyTIFF(t, 24, 16, tiffTag{tag: 0x0112, typ: 3, count: 1, value: 6})

	plain, err := fingerprint.ISCCPixelsFromReader(bytes.NewReader(upright))
	if err != nil {
		t.Fatal(err)
	}
	transposed, err := fingerprint.ISCCPixelsFromReader(bytes.NewReader(rotated))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(plain, transposed) {
		t.Error("orientation 6 was ignored; the TIFF normalised as if it were upright")
	}

	// And an orientation that means nothing is ignored rather than fatal —
	// the best-effort contract the JPEG path already holds to.
	for _, bad := range []uint32{0, 9, 65535} {
		got, err := fingerprint.ISCCPixelsFromReader(bytes.NewReader(
			tinyTIFF(t, 24, 16, tiffTag{tag: 0x0112, typ: 3, count: 1, value: bad})))
		if err != nil {
			t.Fatalf("orientation %d should be ignored, not an error: %v", bad, err)
		}
		if !bytes.Equal(got, plain) {
			t.Errorf("orientation %d changed the normalised image", bad)
		}
	}
}

// TestISCCPixelsAppliesWebPOrientation: a WebP keeps EXIF in a RIFF chunk that
// x/image only counts a flag bit for.
func TestISCCPixelsAppliesWebPOrientation(t *testing.T) {
	base := readFixture(t, "iscc_demo.lossless.webp")
	plain, err := fingerprint.ISCCPixelsFromReader(bytes.NewReader(base))
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := fingerprint.ISCCPixelsFromReader(bytes.NewReader(webpWithEXIF(t, base, 6)))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(plain, rotated) {
		t.Error("the WebP's EXIF orientation was ignored")
	}

	// The transposed result must match the reference image rotated the same
	// way, which is what proves we transposed rather than merely differed.
	want := fingerprint.ISCCPixels(rotate90(loadImage(t, "iscc_demo.png")))
	if off, worst := compareBytes(rotated, want); worst > 2 {
		t.Errorf("transposed WebP is not the rotated reference: %d pixels differ, worst %d", off, worst)
	}
}

// TestISCCPixelsRefusesAnimatedWebP: an animated WebP has no single image, and
// x/image refuses it outright rather than handing back one arbitrary frame.
// Asserted here because "we compute a code for the first frame" would be a
// perfectly plausible thing for a decoder to do, and it must not happen.
func TestISCCPixelsRefusesAnimatedWebP(t *testing.T) {
	if _, err := fingerprint.ISCCPixelsFromReader(bytes.NewReader(animatedWebP(t))); err == nil {
		t.Fatal("an animated WebP must not yield a code")
	}
}

// --- helpers ---

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func isccPixelsOf(t *testing.T, name string) []byte {
	t.Helper()
	got, err := fingerprint.ISCCPixelsFromReader(bytes.NewReader(readFixture(t, name)))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return got
}

// compareBytes reports how many bytes differ and by how much at worst.
func compareBytes(got, want []byte) (differing, worst int) {
	if len(got) != len(want) {
		return len(want), 255
	}
	for i := range want {
		d := int(got[i]) - int(want[i])
		if d < 0 {
			d = -d
		}
		if d != 0 {
			differing++
		}
		if d > worst {
			worst = d
		}
	}
	return differing, worst
}

// rotate90 turns an image a quarter turn clockwise — EXIF orientation 6.
func rotate90(img image.Image) image.Image {
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dy(), b.Dx()))
	for y := range b.Dy() {
		for x := range b.Dx() {
			out.Set(b.Dy()-1-y, x, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return out
}

// tiffTag is one IFD entry for tinyTIFF: an inline value only, which covers
// every tag these tests need.
type tiffTag struct {
	tag, typ uint16
	count    uint32
	value    uint32
}

// tinyTIFF assembles a valid little-endian uncompressed RGB TIFF carrying a
// deterministic pattern, plus any extra IFD entries named by the caller.
// Entries are emitted in ascending tag order, which TIFF requires and x/image
// enforces. Building the file by hand is the point: these tests are about
// directory entries no encoder will write for us.
func tinyTIFF(t *testing.T, w, h int, extra ...tiffTag) []byte {
	t.Helper()

	pixels := make([]byte, 0, w*h*3)
	for y := range h {
		for x := range w {
			pixels = append(pixels, uint8(x*9), uint8(y*13), uint8(x*y))
		}
	}

	// BitsPerSample is three SHORTs, which do not fit in an entry's four value
	// bytes, so it is the one tag needing an out-of-line value.
	entries := append([]tiffTag{
		{tag: 0x0100, typ: 3, count: 1, value: uint32(w)},           // ImageWidth
		{tag: 0x0101, typ: 3, count: 1, value: uint32(h)},           // ImageLength
		{tag: 0x0102, typ: 3, count: 3},                             // BitsPerSample, patched below
		{tag: 0x0103, typ: 3, count: 1, value: 1},                   // Compression: none
		{tag: 0x0106, typ: 3, count: 1, value: 2},                   // Photometric: RGB
		{tag: 0x0111, typ: 4, count: 1},                             // StripOffsets, patched below
		{tag: 0x0115, typ: 3, count: 1, value: 3},                   // SamplesPerPixel
		{tag: 0x0116, typ: 3, count: 1, value: uint32(h)},           // RowsPerStrip
		{tag: 0x0117, typ: 4, count: 1, value: uint32(len(pixels))}, // StripByteCounts
	}, extra...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].tag < entries[j].tag })

	const headerLen = 8
	ifdLen := 2 + 12*len(entries) + 4
	bpsOffset := headerLen + ifdLen
	pixelOffset := bpsOffset + 6
	for i := range entries {
		switch entries[i].tag {
		case 0x0102:
			entries[i].value = uint32(bpsOffset)
		case 0x0111:
			entries[i].value = uint32(pixelOffset)
		}
	}

	out := make([]byte, 0, pixelOffset+len(pixels))
	out = append(out, 'I', 'I', 42, 0)
	out = binary.LittleEndian.AppendUint32(out, headerLen)
	out = binary.LittleEndian.AppendUint16(out, uint16(len(entries)))
	for _, e := range entries {
		out = binary.LittleEndian.AppendUint16(out, e.tag)
		out = binary.LittleEndian.AppendUint16(out, e.typ)
		out = binary.LittleEndian.AppendUint32(out, e.count)
		// A SHORT's value sits in the first two bytes of the value field.
		if e.typ == 3 && e.count == 1 {
			out = binary.LittleEndian.AppendUint16(out, uint16(e.value))
			out = append(out, 0, 0)
		} else {
			out = binary.LittleEndian.AppendUint32(out, e.value)
		}
	}
	out = binary.LittleEndian.AppendUint32(out, 0) // no next IFD
	for range 3 {
		out = binary.LittleEndian.AppendUint16(out, 8) // BitsPerSample: 8,8,8
	}
	return append(out, pixels...)
}

// webpWithEXIF appends an EXIF chunk carrying an orientation to a WebP, and
// fixes up the RIFF size. Encoders put EXIF after the bitstream, which is also
// past the point x/image stops reading — so this exercises our own walk.
func webpWithEXIF(t *testing.T, base []byte, orientation uint16) []byte {
	t.Helper()
	if len(base) < 12 || string(base[:4]) != "RIFF" {
		t.Fatal("base is not a RIFF file")
	}

	// A minimal TIFF header with a single-entry IFD0.
	exif := []byte{'I', 'I', 42, 0, 8, 0, 0, 0, 1, 0}
	exif = binary.LittleEndian.AppendUint16(exif, 0x0112)
	exif = binary.LittleEndian.AppendUint16(exif, 3)
	exif = binary.LittleEndian.AppendUint32(exif, 1)
	exif = binary.LittleEndian.AppendUint16(exif, orientation)
	exif = append(exif, 0, 0)
	exif = binary.LittleEndian.AppendUint32(exif, 0) // no next IFD

	chunk := append([]byte("EXIF"), binary.LittleEndian.AppendUint32(nil, uint32(len(exif)))...)
	chunk = append(chunk, exif...)
	if len(exif)%2 == 1 {
		chunk = append(chunk, 0)
	}

	out := append(append([]byte{}, base...), chunk...)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(out)-8))
	return out
}

// animatedWebP wraps a real VP8 bitstream in the ANIM/ANMF structure, which is
// a WebP with no single image to fingerprint.
func animatedWebP(t *testing.T) []byte {
	t.Helper()
	lossy := readFixture(t, "iscc_demo.lossy.webp")
	if string(lossy[12:16]) != "VP8 " {
		t.Fatalf("fixture's first chunk is %q, expected a simple lossy one", lossy[12:16])
	}
	bitstream := lossy[12:] // the VP8 chunk, header and all

	// VP8X: a flag byte, three reserved, then canvas width-1 and height-1 as
	// 24-bit little-endian values.
	put24 := func(b []byte, v uint32) { b[0], b[1], b[2] = byte(v), byte(v>>8), byte(v>>16) }
	vp8x := make([]byte, 10)
	vp8x[0] = 1 << 1 // the animation bit
	put24(vp8x[4:], 199)
	put24(vp8x[7:], 132)

	anim := []byte{0, 0, 0, 0, 0, 0} // background colour, loop count
	frame := make([]byte, 16)        // frame x/y/w/h/duration, then the payload
	frame = append(frame, bitstream...)

	body := chunkOf("VP8X", vp8x)
	body = append(body, chunkOf("ANIM", anim)...)
	body = append(body, chunkOf("ANMF", frame)...)

	out := append([]byte("RIFF"), binary.LittleEndian.AppendUint32(nil, uint32(len(body)+4))...)
	out = append(out, "WEBP"...)
	return append(out, body...)
}

func chunkOf(fourCC string, payload []byte) []byte {
	out := append([]byte(fourCC), binary.LittleEndian.AppendUint32(nil, uint32(len(payload)))...)
	out = append(out, payload...)
	if len(payload)%2 == 1 {
		out = append(out, 0)
	}
	return out
}
