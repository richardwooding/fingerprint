package fingerprint

// Stage tests for the Chromaprint pipeline.
//
// This is the only internal test file in the package — everything else is
// tested from fingerprint_test through the exported API. The audio pipeline
// earns the exception because its stages are individually specified by the
// reference implementation and individually wrong in different ways: when the
// end-to-end vector stops matching fpcalc, "which stage" is the whole
// question, and an end-to-end assertion cannot answer it.

import (
	"math"
	"testing"
)

// naiveArea sums a rectangle straight out of the raw rows, with no integral
// image involved. It is the independent check that the summed-area table and
// its half-open bounds agree with the obvious reading.
func naiveArea(rows [][chromaBands]float64, r1, c1, r2, c2 int) float64 {
	var sum float64
	for r := r1; r < r2; r++ {
		for c := c1; c < c2; c++ {
			sum += rows[r][c]
		}
	}
	return sum
}

// testRows builds a deterministic block of chroma rows with no symmetry, so a
// transposed or off-by-one rectangle cannot pass by luck.
func testRows(n int) [][chromaBands]float64 {
	rows := make([][chromaBands]float64, n)
	for r := range n {
		for c := range chromaBands {
			rows[r][c] = float64(r*7+c*3) + float64((r*c)%5)/8
		}
	}
	return rows
}

func TestIntegralImageAreaMatchesNaiveSum(t *testing.T) {
	const n = chromaMaxFilterWidth
	rows := testRows(n)
	img := newChromaIntegralImage(n)
	for _, row := range rows {
		img.addRow(row)
	}

	for r1 := range n + 1 {
		for r2 := r1; r2 <= n; r2++ {
			for c1 := range chromaBands + 1 {
				for c2 := c1; c2 <= chromaBands; c2++ {
					got := img.area(r1, c1, r2, c2)
					want := naiveArea(rows, r1, c1, r2, c2)
					if math.Abs(got-want) > 1e-9 {
						t.Fatalf("area(%d,%d,%d,%d) = %v, want %v", r1, c1, r2, c2, got, want)
					}
				}
			}
		}
	}
}

// TestIntegralImageRolls checks the ring buffer: the table keeps only the last
// maxRows+1 rows, and rows still inside that window must read correctly after
// it has wrapped several times.
func TestIntegralImageRolls(t *testing.T) {
	const window = chromaMaxFilterWidth
	rows := testRows(window * 4)
	img := newChromaIntegralImage(window)

	for i, row := range rows {
		img.addRow(row)
		if img.rows < window {
			continue
		}
		// The most recent full window, addressed the way the calculator
		// addresses it.
		offset := img.rows - window
		got := img.area(offset, 0, offset+window, chromaBands)
		want := naiveArea(rows[:i+1], offset, 0, offset+window, chromaBands)
		if math.Abs(got-want) > 1e-6 {
			t.Fatalf("after %d rows: window total %v, want %v", img.rows, got, want)
		}
	}
}

// TestFilterKindsSplitTheRectangle checks each of the six trained comparisons
// against its documented geometry, computed naively. Integer division is part
// of the specification here: an odd rectangle's halves are uneven, and both
// sides must agree on which part gets the extra row.
func TestFilterKindsSplitTheRectangle(t *testing.T) {
	const n = chromaMaxFilterWidth
	rows := testRows(n)
	img := newChromaIntegralImage(n)
	for _, row := range rows {
		img.addRow(row)
	}

	// Odd widths and heights throughout, so truncation is exercised.
	specs := []chromaFilterSpec{
		{0, 4, 3, 15}, {1, 0, 4, 16}, {2, 6, 2, 16},
		{3, 5, 6, 4}, {4, 0, 3, 5}, {5, 10, 1, 15},
		{1, 1, 5, 7}, {2, 2, 7, 5}, {3, 0, 5, 5}, {4, 0, 7, 4}, {5, 3, 4, 7},
	}

	for _, spec := range specs {
		x, y, w, h := 0, spec.y, spec.width, spec.height
		var a, b float64
		switch spec.kind {
		case 0:
			a, b = naiveArea(rows, x, y, x+w, y+h), 0
		case 1:
			h2 := h / 2
			a = naiveArea(rows, x, y+h2, x+w, y+h)
			b = naiveArea(rows, x, y, x+w, y+h2)
		case 2:
			w2 := w / 2
			a = naiveArea(rows, x+w2, y, x+w, y+h)
			b = naiveArea(rows, x, y, x+w2, y+h)
		case 3:
			w2, h2 := w/2, h/2
			a = naiveArea(rows, x, y+h2, x+w2, y+h) + naiveArea(rows, x+w2, y, x+w, y+h2)
			b = naiveArea(rows, x, y, x+w2, y+h2) + naiveArea(rows, x+w2, y+h2, x+w, y+h)
		case 4:
			h3 := h / 3
			a = naiveArea(rows, x, y+h3, x+w, y+2*h3)
			b = naiveArea(rows, x, y, x+w, y+h3) + naiveArea(rows, x, y+2*h3, x+w, y+h)
		case 5:
			w3 := w / 3
			a = naiveArea(rows, x+w3, y, x+2*w3, y+h)
			b = naiveArea(rows, x, y, x+w3, y+h) + naiveArea(rows, x+2*w3, y, x+w, y+h)
		}

		want := chromaSubtractLog(a, b)
		got := spec.apply(img, x)
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("kind %d (y=%d h=%d w=%d): got %v, want %v", spec.kind, spec.y, spec.height, spec.width, got, want)
		}
	}
}

// TestFilterKindUnknownIsZero pins the reference's fall-through: an
// unrecognised kind contributes no signal rather than panicking.
func TestFilterKindUnknownIsZero(t *testing.T) {
	img := newChromaIntegralImage(chromaMaxFilterWidth)
	for _, row := range testRows(chromaMaxFilterWidth) {
		img.addRow(row)
	}
	if got := (chromaFilterSpec{99, 0, 4, 4}).apply(img, 0); got != 0 {
		t.Errorf("unknown filter kind returned %v, want 0", got)
	}
}

func TestSubtractLogIsAntisymmetric(t *testing.T) {
	for _, pair := range [][2]float64{{0, 0}, {1, 0}, {0, 1}, {3.5, 0.25}, {1e6, 1e-6}} {
		a, b := pair[0], pair[1]
		if got, want := chromaSubtractLog(a, b), -chromaSubtractLog(b, a); math.Abs(got-want) > 1e-12 {
			t.Errorf("subtractLog(%v,%v) = %v, want %v", a, b, got, want)
		}
	}
	if got := chromaSubtractLog(0, 0); got != 0 {
		t.Errorf("subtractLog(0,0) = %v, want 0 — the +1 must keep it finite", got)
	}
}

// TestQuantizerBoundaries pins the comparison direction at every threshold.
// The reference uses a strict less-than, so a value sitting exactly on a
// threshold falls into the upper level.
func TestQuantizerBoundaries(t *testing.T) {
	q := chromaQuantizer{-1, 0, 1}
	cases := []struct {
		value float64
		want  int
	}{
		{-2, 0}, {math.Nextafter(-1, -2), 0},
		{-1, 1}, {-0.5, 1}, {math.Nextafter(0, -1), 1},
		{0, 2}, {0.5, 2}, {math.Nextafter(1, 0), 2},
		{1, 3}, {2, 3},
	}
	for _, tc := range cases {
		if got := q.quantize(tc.value); got != tc.want {
			t.Errorf("quantize(%v) = %d, want %d", tc.value, got, tc.want)
		}
	}
}

// TestGrayCodeIsSingleBitStepped is the property the table exists for: two
// adjacent quantiser levels must differ in exactly one bit, so a value that
// drifts across a threshold costs one bit of distance, not two.
func TestGrayCodeIsSingleBitStepped(t *testing.T) {
	for i := range 3 {
		diff := chromaGrayCode[i] ^ chromaGrayCode[i+1]
		if diff&(diff-1) != 0 || diff == 0 {
			t.Errorf("gray[%d]=%d and gray[%d]=%d differ in %d bits, want exactly 1",
				i, chromaGrayCode[i], i+1, chromaGrayCode[i+1], diff)
		}
	}
	seen := map[uint32]bool{}
	for _, v := range chromaGrayCode {
		if v > 3 {
			t.Errorf("gray code %d does not fit in two bits", v)
		}
		seen[v] = true
	}
	if len(seen) != 4 {
		t.Errorf("gray code table is not a permutation: %v", chromaGrayCode)
	}
}

// TestClassifierTableShape guards the trained constants against a careless
// edit: the thresholds must be ordered, and every rectangle must fit inside
// the 16-frame by 12-class window the calculator provides.
func TestClassifierTableShape(t *testing.T) {
	widest := 0
	for i, cl := range chromaClassifiersTest2 {
		q := cl.quantizer
		if !(q.t0 <= q.t1 && q.t1 <= q.t2) {
			t.Errorf("classifier %d: thresholds out of order: %v", i, q)
		}
		f := cl.filter
		if f.kind < 0 || f.kind > 5 {
			t.Errorf("classifier %d: filter kind %d out of range", i, f.kind)
		}
		if f.y < 0 || f.height < 1 || f.y+f.height > chromaBands {
			t.Errorf("classifier %d: rows %d..%d escape the %d pitch classes", i, f.y, f.y+f.height, chromaBands)
		}
		if f.width < 1 || f.width > chromaMaxFilterWidth {
			t.Errorf("classifier %d: width %d escapes the window", i, f.width)
		}
		widest = max(widest, f.width)
	}
	if widest != chromaMaxFilterWidth {
		t.Errorf("widest filter is %d but the calculator waits for %d rows", widest, chromaMaxFilterWidth)
	}
}

// TestChromaMapperBinRange pins the two indices that decide which part of the
// spectrum is folded at all. A one-off here silently moves a whole pitch
// class's energy, and nothing downstream would look wrong.
func TestChromaMapperBinRange(t *testing.T) {
	m := newChromaMapper(chromaSampleRate)
	// round(4096*28/11025) = 10 and round(4096*3520/11025) = 1308.
	if m.minIndex != 10 {
		t.Errorf("minIndex = %d, want 10", m.minIndex)
	}
	if m.maxIndex != 1308 {
		t.Errorf("maxIndex = %d, want 1308", m.maxIndex)
	}
	for i := m.minIndex; i < m.maxIndex; i++ {
		if m.notes[i] >= chromaBands {
			t.Fatalf("bin %d maps to pitch class %d, outside 0..11", i, m.notes[i])
		}
	}
}

// TestChromaMapperFoldsOctavesTogether is the property that makes a chroma
// vector a chroma vector: energy one octave apart lands in the same class.
func TestChromaMapperFoldsOctavesTogether(t *testing.T) {
	m := newChromaMapper(chromaSampleRate)
	for i := m.minIndex; i < m.maxIndex; i++ {
		if 2*i >= m.maxIndex {
			continue
		}
		if m.notes[i] != m.notes[2*i] {
			freq := float64(i) * chromaSampleRate / chromaFrameSize
			t.Fatalf("bin %d (%.1f Hz) is class %d but its octave is class %d",
				i, freq, m.notes[i], m.notes[2*i])
		}
	}
}

// TestChromaMapperIgnoresOutOfRangeBins checks that energy below 28 Hz or
// above 3520 Hz contributes nothing at all.
func TestChromaMapperIgnoresOutOfRangeBins(t *testing.T) {
	m := newChromaMapper(chromaSampleRate)
	spectrum := make([]float64, 1+chromaFrameSize/2)
	for i := range m.minIndex {
		spectrum[i] = 1e6
	}
	for i := m.maxIndex; i < len(spectrum); i++ {
		spectrum[i] = 1e6
	}
	if got := m.fold(spectrum); got != ([chromaBands]float64{}) {
		t.Errorf("out-of-range energy leaked into the chroma vector: %v", got)
	}
}

// TestChromaFilterWarmsUp pins the reference's off-by-one: the ring starts at
// size 1, not 0, so four vectors go in before anything comes out and the
// fifth is the first smoothed frame.
func TestChromaFilterWarmsUp(t *testing.T) {
	f := newChromaFilter()
	for i := range 4 {
		if _, ok := f.consume([chromaBands]float64{}); ok {
			t.Fatalf("frame %d produced output during warm-up", i)
		}
	}
	if _, ok := f.consume([chromaBands]float64{}); !ok {
		t.Fatal("the fifth frame produced no output")
	}
}

// TestChromaFilterAppliesCoefficients checks the smoothing itself: a single
// impulse walking out through the ring must reproduce the tap weights.
//
// The impulse goes in at frame 4, the first frame that produces output, so
// that it occupies every tap position in turn as the window slides past it.
// An impulse in any earlier frame is already partway out of the window by the
// time the filter starts emitting, and would show only its tail.
func TestChromaFilterAppliesCoefficients(t *testing.T) {
	var got []float64
	f := newChromaFilter()
	for i := range 9 {
		var in [chromaBands]float64
		if i == 4 {
			in[0] = 1
		}
		if out, ok := f.consume(in); ok {
			got = append(got, out[0])
		}
	}
	want := []float64{0.25, 0.75, 1.0, 0.75, 0.25}
	if len(got) != len(want) {
		t.Fatalf("got %d smoothed frames, want %d", len(got), len(want))
	}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-12 {
			t.Errorf("frame %d weighted %v, want %v", i, got[i], want[i])
		}
	}
}

func TestChromaNormalizeUnitLength(t *testing.T) {
	f := [chromaBands]float64{3, 4}
	chromaNormalize(&f)
	if math.Abs(f[0]-0.6) > 1e-12 || math.Abs(f[1]-0.8) > 1e-12 {
		t.Errorf("normalised to %v, want [0.6 0.8 ...]", f[:2])
	}
}

// TestChromaNormalizeQuietFramesZero pins the 0.01 floor. Below it the frame
// is zeroed rather than scaled up, because dividing near-silence by its own
// norm turns noise into apparent structure.
func TestChromaNormalizeQuietFramesZero(t *testing.T) {
	just := math.Sqrt(0.01*0.01/chromaBands) * 1.0001
	under := math.Sqrt(0.01*0.01/chromaBands) * 0.9

	var loud, quiet [chromaBands]float64
	for i := range chromaBands {
		loud[i], quiet[i] = just, under
	}
	chromaNormalize(&loud)
	chromaNormalize(&quiet)

	if loud == ([chromaBands]float64{}) {
		t.Error("a frame just over the threshold was zeroed")
	}
	if quiet != ([chromaBands]float64{}) {
		t.Errorf("a frame under the threshold survived as %v", quiet[:2])
	}
}

// TestHammingWindowMatchesReference pins the window's arithmetic. The scale is
// 1/INT16_MAX and every coefficient is stored as a float32, exactly as the
// reference's FFTSample array stores it — that rounding is visible in the
// output, so it is part of the contract rather than an implementation detail.
func TestHammingWindowMatchesReference(t *testing.T) {
	f := newChromaFFT()
	if len(f.window) != chromaFrameSize {
		t.Fatalf("window is %d wide, want %d", len(f.window), chromaFrameSize)
	}

	ends := float32(0.08 / 32767.0)
	if f.window[0] != ends {
		t.Errorf("window[0] = %v, want %v", f.window[0], ends)
	}
	if f.window[chromaFrameSize-1] != ends {
		t.Errorf("window[last] = %v, want %v — the divisor is size-1, so both ends match", f.window[chromaFrameSize-1], ends)
	}

	// Every coefficient must be exactly representable as a float32, which is
	// the point of storing them narrowed.
	for i, v := range f.window {
		if float32(float64(v)) != v {
			t.Fatalf("window[%d] is not a float32 value", i)
		}
	}

	peak := f.window[0]
	for _, v := range f.window {
		peak = max(peak, v)
	}
	if want := float32(1.0 / 32767.0); math.Abs(float64(peak-want)) > 1e-9 {
		t.Errorf("window peak = %v, want about %v", peak, want)
	}
}

// TestFFTPowerSpectrum checks the transform against a signal whose spectrum is
// known by hand: a sinusoid exactly on bin k puts all its energy in bin k.
func TestFFTPowerSpectrum(t *testing.T) {
	const bin = 64
	f := newChromaFFT()
	frame := make([]int16, chromaFrameSize)
	for i := range frame {
		frame[i] = int16(16000 * math.Sin(2*math.Pi*bin*float64(i)/chromaFrameSize))
	}

	out := make([]float64, 1+chromaFrameSize/2)
	f.transform(frame, out)

	peak, at := 0.0, 0
	var total float64
	for i, v := range out {
		total += v
		if v > peak {
			peak, at = v, i
		}
	}
	if at != bin {
		t.Errorf("peak at bin %d, want %d", at, bin)
	}
	// The Hamming window spreads a little into the neighbours, but the bulk
	// must be at the peak and its two immediate neighbours.
	near := out[bin-1] + out[bin] + out[bin+1]
	if near < 0.99*total {
		t.Errorf("only %.1f%% of the energy is at the peak, want over 99%%", 100*near/total)
	}
}

func TestFFTSilenceIsZero(t *testing.T) {
	f := newChromaFFT()
	out := make([]float64, 1+chromaFrameSize/2)
	f.transform(make([]int16, chromaFrameSize), out)
	for i, v := range out {
		if v != 0 {
			t.Fatalf("silence produced energy %v in bin %d", v, i)
		}
	}
}

// TestDownmixTruncation pins the integer arithmetic of the downmix, including
// the direction Go truncates a negative average — the reference divides
// int32 sums the same way, so a rounding "fix" here would desynchronise every
// frame of a stereo file.
func TestDownmixTruncation(t *testing.T) {
	cases := []struct {
		in       []int16
		channels int
		want     int16
	}{
		{[]int16{3, 4}, 2, 3},
		{[]int16{-3, -4}, 2, -3},
		{[]int16{-1, 0}, 2, 0},
		{[]int16{32767, 32767}, 2, 32767},
		{[]int16{-32768, -32768}, 2, -32768},
		{[]int16{1, 2, 3, 5}, 4, 2},
		{[]int16{-1, -2, -3, -5}, 4, -2},
	}
	for _, tc := range cases {
		pcm := PCM{Samples: tc.in, SampleRate: chromaSampleRate, Channels: tc.channels}
		if err := chromaValidate(pcm); err != nil {
			t.Fatalf("chromaValidate(%v): %v", tc.in, err)
		}
		got := chromaMono(pcm)
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("downmix %v over %d channels = %v, want [%d]", tc.in, tc.channels, got, tc.want)
		}
	}
}
