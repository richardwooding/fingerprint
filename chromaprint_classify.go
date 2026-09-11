package fingerprint

import "math"

// Turning the chroma image into 32-bit subfingerprints.
//
// The smoothed chroma vectors stack into an image: one row per frame, twelve
// columns of pitch class. Slide a 16-row window along it and, at every
// position, ask sixteen fixed questions of the form "is this rectangle of the
// window brighter than that one?". Each answer is quantised to two bits, Gray
// coded so that adjacent answers differ in one bit, and the sixteen pairs are
// packed into a uint32. That word is one subfingerprint, and the sequence of
// them is the vector ISCC hashes.
//
// The sixteen questions are not derived from anything — they were trained, and
// the reference ships them as a table. This is TEST2, the default algorithm
// and the one behind every ISCC Audio-Code.

// chromaFilterSpec is one trained rectangle comparison. kind selects which
// halves or thirds of the window are compared; y and height address pitch
// classes, width addresses frames.
type chromaFilterSpec struct {
	kind   int
	y      int
	height int
	width  int
}

// chromaQuantizer turns a comparison's value into two bits by three trained
// thresholds.
type chromaQuantizer struct{ t0, t1, t2 float64 }

// chromaClassifier pairs a comparison with the thresholds trained for it.
type chromaClassifier struct {
	filter    chromaFilterSpec
	quantizer chromaQuantizer
}

// chromaClassifiersTest2 is the reference's kClassifiersTest2, verbatim and in
// order. The order is part of the format: the packing shifts each classifier's
// two bits in as it goes, so reordering the table changes every word.
var chromaClassifiersTest2 = [16]chromaClassifier{
	{chromaFilterSpec{0, 4, 3, 15}, chromaQuantizer{1.98215, 2.35817, 2.63523}},
	{chromaFilterSpec{4, 4, 6, 15}, chromaQuantizer{-1.03809, -0.651211, -0.282167}},
	{chromaFilterSpec{1, 0, 4, 16}, chromaQuantizer{-0.298702, 0.119262, 0.558497}},
	{chromaFilterSpec{3, 8, 2, 12}, chromaQuantizer{-0.105439, 0.0153946, 0.135898}},
	{chromaFilterSpec{3, 4, 4, 8}, chromaQuantizer{-0.142891, 0.0258736, 0.200632}},
	{chromaFilterSpec{4, 0, 3, 5}, chromaQuantizer{-0.826319, -0.590612, -0.368214}},
	{chromaFilterSpec{1, 2, 2, 9}, chromaQuantizer{-0.557409, -0.233035, 0.0534525}},
	{chromaFilterSpec{2, 7, 3, 4}, chromaQuantizer{-0.0646826, 0.00620476, 0.0784847}},
	{chromaFilterSpec{2, 6, 2, 16}, chromaQuantizer{-0.192387, -0.029699, 0.215855}},
	{chromaFilterSpec{2, 1, 3, 2}, chromaQuantizer{-0.0397818, -0.00568076, 0.0292026}},
	{chromaFilterSpec{5, 10, 1, 15}, chromaQuantizer{-0.53823, -0.369934, -0.190235}},
	{chromaFilterSpec{3, 6, 2, 10}, chromaQuantizer{-0.124877, 0.0296483, 0.139239}},
	{chromaFilterSpec{2, 1, 1, 14}, chromaQuantizer{-0.101475, 0.0225617, 0.231971}},
	{chromaFilterSpec{3, 5, 6, 4}, chromaQuantizer{-0.0799915, -0.00729616, 0.063262}},
	{chromaFilterSpec{1, 9, 2, 12}, chromaQuantizer{-0.272556, 0.019424, 0.302559}},
	{chromaFilterSpec{3, 4, 2, 14}, chromaQuantizer{-0.164292, -0.0321188, 0.0846339}},
}

// chromaGrayCode is the reference's two-bit Gray code table. Quantiser levels
// that sit next to each other differ in exactly one bit, so a value that drifts
// across a threshold costs one bit of Hamming distance rather than two.
var chromaGrayCode = [4]uint32{0, 1, 3, 2}

// chromaMaxFilterWidth is the widest window any TEST2 classifier reads, and so
// the number of rows that must be in hand before the first subfingerprint.
const chromaMaxFilterWidth = 16

// chromaCalculator accumulates rows and emits one subfingerprint per row once
// the window is full.
type chromaCalculator struct {
	image       *chromaIntegralImage
	fingerprint []int32
}

func newChromaCalculator() *chromaCalculator {
	return &chromaCalculator{image: newChromaIntegralImage(chromaMaxFilterWidth)}
}

// consume adds one smoothed chroma vector and, once there are enough rows,
// appends the subfingerprint for the window ending at it.
func (c *chromaCalculator) consume(features [chromaBands]float64) {
	c.image.addRow(features)
	if c.image.rows >= chromaMaxFilterWidth {
		c.fingerprint = append(c.fingerprint, c.subfingerprint(c.image.rows-chromaMaxFilterWidth))
	}
}

// subfingerprint packs the sixteen classifier answers for the window starting
// at row offset. The reference stores these as uint32 and ISCC reads them as
// signed, so the conversion here is the whole of `fpcalc -signed`.
func (c *chromaCalculator) subfingerprint(offset int) int32 {
	var bits uint32
	for _, cl := range chromaClassifiersTest2 {
		value := cl.filter.apply(c.image, offset)
		bits = bits<<2 | chromaGrayCode[cl.quantizer.quantize(value)]
	}
	return int32(bits)
}

// quantize maps a comparison to one of four levels by the trained thresholds.
func (q chromaQuantizer) quantize(value float64) int {
	if value < q.t1 {
		if value < q.t0 {
			return 0
		}
		return 1
	}
	if value < q.t2 {
		return 2
	}
	return 3
}

// chromaSubtractLog is the reference's SubtractLog: comparisons are made in
// log space so that a ratio between two quiet regions counts as much as the
// same ratio between two loud ones. The +1 keeps it finite at zero energy.
func chromaSubtractLog(a, b float64) float64 {
	return math.Log((1.0 + a) / (1.0 + b))
}

// apply evaluates one trained comparison over the window starting at row x.
//
// Each kind splits the rectangle a different way and compares the two parts:
// whole against nothing, bottom against top, right against left, the diagonal
// quadrant pairs, and the middle third against the outer thirds, both ways
// round. Integer division is deliberate throughout — an odd rectangle's halves
// are uneven in the reference too.
func (f chromaFilterSpec) apply(img *chromaIntegralImage, x int) float64 {
	w, h, y := f.width, f.height, f.y
	switch f.kind {
	case 0:
		return chromaSubtractLog(img.area(x, y, x+w, y+h), 0)
	case 1:
		h2 := h / 2
		return chromaSubtractLog(
			img.area(x, y+h2, x+w, y+h),
			img.area(x, y, x+w, y+h2))
	case 2:
		w2 := w / 2
		return chromaSubtractLog(
			img.area(x+w2, y, x+w, y+h),
			img.area(x, y, x+w2, y+h))
	case 3:
		w2, h2 := w/2, h/2
		return chromaSubtractLog(
			img.area(x, y+h2, x+w2, y+h)+img.area(x+w2, y, x+w, y+h2),
			img.area(x, y, x+w2, y+h2)+img.area(x+w2, y+h2, x+w, y+h))
	case 4:
		h3 := h / 3
		return chromaSubtractLog(
			img.area(x, y+h3, x+w, y+2*h3),
			img.area(x, y, x+w, y+h3)+img.area(x, y+2*h3, x+w, y+h))
	case 5:
		w3 := w / 3
		return chromaSubtractLog(
			img.area(x+w3, y, x+2*w3, y+h),
			img.area(x, y, x+w3, y+h)+img.area(x+2*w3, y, x+w, y+h))
	}
	return 0
}

// chromaIntegralImage is the reference's RollingIntegralImage: a summed-area
// table over the last few rows, so any rectangle's total is four lookups
// regardless of size. It keeps maxRows+1 rows and wraps, because nothing ever
// asks about a row that has fallen off the back.
type chromaIntegralImage struct {
	data    []float64
	maxRows int
	rows    int
}

func newChromaIntegralImage(maxRows int) *chromaIntegralImage {
	m := maxRows + 1
	return &chromaIntegralImage{data: make([]float64, m*chromaBands), maxRows: m}
}

// row returns the stored prefix sums for row i.
func (img *chromaIntegralImage) row(i int) []float64 {
	i %= img.maxRows
	return img.data[i*chromaBands : (i+1)*chromaBands]
}

// addRow stores one chroma vector as running sums along the row, plus the
// running total of every row before it.
func (img *chromaIntegralImage) addRow(features [chromaBands]float64) {
	current := img.row(img.rows)
	sum := 0.0
	for i, v := range features {
		sum += v
		current[i] = sum
	}
	if img.rows > 0 {
		previous := img.row(img.rows - 1)
		for i := range current {
			current[i] += previous[i]
		}
	}
	img.rows++
}

// area totals the rectangle spanning rows [r1, r2) and columns [c1, c2).
func (img *chromaIntegralImage) area(r1, c1, r2, c2 int) float64 {
	if r1 == r2 || c1 == c2 {
		return 0
	}
	if r1 == 0 {
		row := img.row(r2 - 1)
		if c1 == 0 {
			return row[c2-1]
		}
		return row[c2-1] - row[c1-1]
	}
	row1, row2 := img.row(r1-1), img.row(r2-1)
	if c1 == 0 {
		return row2[c2-1] - row1[c2-1]
	}
	return row2[c2-1] - row1[c2-1] - row2[c1-1] + row1[c1-1]
}
