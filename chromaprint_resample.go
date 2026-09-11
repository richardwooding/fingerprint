package fingerprint

import "math"

// Resampling to 11025 Hz, the way fpcalc does it.
//
// Chromaprint fingerprints at 11025 Hz and almost no real file is at that
// rate, so the resampler decides the answer for nearly every input — and it is
// not Chromaprint's own. fpcalc hands the audio to FFmpeg's swresample before
// the fingerprinter sees a sample, and Chromaprint's internal av_resample is
// reached only by a caller feeding raw PCM to the library. The two are
// different filters and they disagree: on the test recording, resampling
// 44.1 kHz down to 11025 Hz one way rather than the other moves 62 of 3328
// fingerprint bits and changes the resulting ISCC.
//
// Only one of those answers is the one iscc-sdk publishes and the one every
// other ISCC implementation will compute, so this reproduces swresample as
// chromaprint configures it: engine SWR, filter_size 16, phase_shift 8,
// linear_interp on, cutoff 0.8, and — because the sample formats are 16-bit
// in and 16-bit out at differing rates — a float32 internal format, which
// fixes the filter bank's type and the whole chain's rounding.
//
// The lead-in and tail are mirrored, not zero-padded. That is swresample's
// choice and it is visible in the first and last subfingerprints.

const (
	// swrFilterSize, swrPhaseShift, swrCutoff and swrLinear are chromaprint's
	// SetCompatibleMode() options, and swrKaiserBeta is swresample's default
	// window, which chromaprint leaves alone.
	swrFilterSize = 16
	swrPhaseShift = 8
	swrCutoff     = 0.8
	swrLinear     = true
	swrKaiserBeta = 9.0
)

// swrResampler is a rate converter for one channel, in the configuration
// fpcalc uses. Its state advances as samples are consumed, so one instance
// converts one channel of one stream.
type swrResampler struct {
	filterBank   []float32
	filterLength int
	filterAlloc  int
	phaseCount   int
	srcIncr      int
	dstIncr      int
	dstIncrDiv   int
	dstIncrMod   int
	index        int
	frac         int
	linear       bool
}

// newSWRResampler builds the polyphase filter bank and the phase accumulator
// for a rate pair, following swresample's resample_init.
func newSWRResampler(inRate, outRate int) *swrResampler {
	factor := math.Min(float64(outRate)*swrCutoff/float64(inRate), 1.0)

	phaseCount := 1 << swrPhaseShift
	filterLength := max(int(math.Ceil(swrFilterSize/factor)), 1)
	if filterLength > 1 {
		filterLength = (filterLength + 1) &^ 1 // FFALIGN(_, 2)
	}

	// exact_rational is on by default: when the rate ratio reduces to a small
	// fraction, only that many distinct phases exist and the bank shrinks to
	// match. 44100 to 11025 reduces to 1/4, so a single phase does it.
	if num, _ := reduceRatio(outRate, inRate); num <= phaseCount {
		phaseCount = num
	}

	c := &swrResampler{
		filterLength: filterLength,
		filterAlloc:  (filterLength + 7) &^ 7, // FFALIGN(_, 8)
		phaseCount:   phaseCount,
		linear:       swrLinear,
	}
	c.filterBank = swrBuildFilter(c.filterAlloc, filterLength, phaseCount, factor)

	// The bank carries one extra phase so the linear path can read phase+1
	// without a bounds check: it wraps to the start, shifted by one tap.
	tail := c.filterAlloc * phaseCount
	copy(c.filterBank[tail+1:tail+c.filterAlloc], c.filterBank[:c.filterAlloc-1])
	c.filterBank[tail] = c.filterBank[c.filterAlloc-1]

	// The accumulator counts in units of 1/srcIncr of a phase.
	c.srcIncr, c.dstIncr = reduceRatio(outRate, inRate*phaseCount)
	for c.dstIncr < 1<<20 && c.srcIncr < 1<<20 {
		c.dstIncr *= 2
		c.srcIncr *= 2
	}
	c.dstIncrDiv = c.dstIncr / c.srcIncr
	c.dstIncrMod = c.dstIncr % c.srcIncr
	c.index = -phaseCount * ((filterLength - 1) / 2)
	c.frac = 0
	return c
}

// reduceRatio is av_reduce for the small, positive ratios this file needs:
// both terms fit well inside its INT32_MAX/2 bound, so it is a plain gcd
// reduction with no continued-fraction fallback.
func reduceRatio(num, den int) (int, int) {
	a, b := num, den
	for b != 0 {
		a, b = b, a%b
	}
	if a == 0 {
		return num, den
	}
	return num / a, den / a
}

// swrBuildFilter is swresample's build_filter for the float32 internal
// format: a Kaiser-windowed sinc per phase, normalised so that a constant
// signal passes through unchanged.
//
// Only half the phases are computed. For an even phase count the rest are the
// mirror image, which is both faster and exactly what the reference does —
// the mirrored coefficients are copied, not recomputed, so they carry the
// same rounding.
func swrBuildFilter(alloc, tapCount, phaseCount int, factor float64) []float32 {
	bank := make([]float32, alloc*(phaseCount+1))

	phases := phaseCount/2 + 1
	if phaseCount%2 != 0 {
		phases = phaseCount
	}
	center := (tapCount - 1) / 2
	tab := make([]float64, tapCount)
	var norm float64

	for ph := range phases {
		for i := range tapCount {
			x := math.Pi * (float64(i-center) - float64(ph)/float64(phaseCount)) * factor
			y := 1.0
			if x != 0 {
				y = math.Sin(x) / x
			}
			// The Kaiser window, swresample's default.
			w := 2.0 * x / (factor * float64(tapCount) * math.Pi)
			y *= besselI0(swrKaiserBeta * math.Sqrt(math.Max(1-w*w, 0)))

			tab[i] = y
			if ph == 0 {
				norm += y
			}
		}
		for i := range tapCount {
			bank[ph*alloc+i] = float32(tab[i] / norm)
		}
		if phaseCount%2 == 0 {
			for i := range tapCount {
				bank[(phaseCount-ph)*alloc+tapCount-1-i] = bank[ph*alloc+i]
			}
		}
	}
	return bank
}

// resample converts a whole channel at once.
//
// swresample mirrors the signal at both ends rather than padding with
// silence: the lead-in reflects the opening samples so the first output is
// centred on sample zero, and the flush reflects the closing ones. Feeding
// the whole channel in one pass makes both edges deterministic, where a
// streaming caller's tail depends on where its chunk boundaries fell.
func (c *swrResampler) resample(samples []float32) []float32 {
	if len(samples) == 0 {
		return nil
	}

	// Lead-in: buf[filterLength] is sample 0 and buf[filterLength-n] is
	// sample n, which is invert_initial_buffer's reflection.
	buf := make([]float32, 0, c.filterLength+len(samples)+c.filterLength)
	for n := c.filterLength; n >= 1; n-- {
		buf = append(buf, samples[min(n, len(samples)-1)])
	}
	buf = append(buf, samples...)

	// Consume the negative index the same way, by stepping the read position
	// back a phase at a time.
	pos := c.filterLength
	for c.index < 0 {
		pos--
		c.index += c.phaseCount
	}

	// Tail: reflect what is left, as resample_flush does at end of stream.
	remaining := len(buf) - pos
	reflection := (min(remaining, c.filterLength) + 1) / 2
	for j := range reflection {
		buf = append(buf, buf[len(buf)-j-1-j])
	}

	out := make([]float32, 0, len(samples)*c.dstIncrDiv+len(samples))
	src := buf[pos:]
	for {
		n := c.outputCount(len(src))
		if n <= 0 {
			break
		}
		start := len(out)
		out = append(out, make([]float32, n)...)
		consumed := c.convert(out[start:], src)
		src = src[consumed:]
	}
	return out
}

// outputCount is multiple_resample's delta_n: how many output samples the
// remaining input can produce without reading past its end.
func (c *swrResampler) outputCount(srcLen int) int {
	endIndex := int64(1+srcLen-c.filterLength) * int64(c.phaseCount)
	deltaFrac := (endIndex-int64(c.index))*int64(c.srcIncr) - int64(c.frac)
	return int((deltaFrac + int64(c.dstIncr) - 1) / int64(c.dstIncr))
}

// convert is swresample's resample_common and resample_linear, which differ
// only in whether they blend between neighbouring phases. The reference picks
// between them per call, and they agree when there is no fractional phase to
// blend, so the choice is made here on the same condition.
//
// The paired accumulators in the common path are the reference's, not an
// optimisation: summing odd and even taps separately and adding the two at
// the end rounds differently from a single running total, and the difference
// reaches the output.
func (c *swrResampler) convert(dst []float32, src []float32) int {
	index, frac, sampleIndex := c.index, c.frac, 0
	for index >= c.phaseCount {
		sampleIndex++
		index -= c.phaseCount
	}

	linear := c.linear && (frac != 0 || c.dstIncrMod != 0)
	invSrcIncr := 1.0 / float64(c.srcIncr)

	for n := range dst {
		filter := c.filterBank[c.filterAlloc*index:]
		window := src[sampleIndex:]

		var val float32
		if linear {
			next := filter[c.filterAlloc:]
			var v2 float32
			for i := range c.filterLength {
				val += window[i] * filter[i]
				v2 += window[i] * next[i]
			}
			val += float32(float64(v2-val) * invSrcIncr * float64(frac))
		} else {
			var val2 float32
			i := 0
			for ; i+1 < c.filterLength; i += 2 {
				val += window[i] * filter[i]
				val2 += window[i+1] * filter[i+1]
			}
			if i < c.filterLength {
				val += window[i] * filter[i]
			}
			val += val2
		}
		dst[n] = val

		frac += c.dstIncrMod
		index += c.dstIncrDiv
		if frac >= c.srcIncr {
			frac -= c.srcIncr
			index++
		}
		for index >= c.phaseCount {
			sampleIndex++
			index -= c.phaseCount
		}
	}

	c.frac, c.index = frac, index
	return sampleIndex
}

// besselI0 is av_bessel_i0: the modified Bessel function of the first kind,
// order zero, by the Blair and Edwards minimax rational approximations.
// The Kaiser window is built from it, so its last bits reach the filter bank.
func besselI0(x float64) float64 {
	if x == 0 {
		return 1.0
	}
	x = math.Abs(x)
	if x <= 15 {
		y := x * x
		return evalPoly(besselP1[:], y) / evalPoly(besselQ1[:], y)
	}
	y := 1/x - 1.0/15
	r := evalPoly(besselP2[:], y) / evalPoly(besselQ2[:], y)
	return math.Exp(x) / math.Sqrt(x) * r
}

// evalPoly is Horner's method over coefficients in ascending order.
func evalPoly(coeff []float64, x float64) float64 {
	sum := coeff[len(coeff)-1]
	for i := len(coeff) - 2; i >= 0; i-- {
		sum *= x
		sum += coeff[i]
	}
	return sum
}

var (
	besselP1 = [15]float64{
		-2.2335582639474375249e+15, -5.5050369673018427753e+14, -3.2940087627407749166e+13,
		-8.4925101247114157499e+11, -1.1912746104985237192e+10, -1.0313066708737980747e+08,
		-5.9545626019847898221e+05, -2.4125195876041896775e+03, -7.0935347449210549190e+00,
		-1.5453977791786851041e-02, -2.5172644670688975051e-05, -3.0517226450451067446e-08,
		-2.6843448573468483278e-11, -1.5982226675653184646e-14, -5.2487866627945699800e-18,
	}
	besselQ1 = [6]float64{
		-2.2335582639474375245e+15, 7.8858692566751002988e+12, -1.2207067397808979846e+10,
		1.0377081058062166144e+07, -4.8527560179962773045e+03, 1.0,
	}
	besselP2 = [7]float64{
		-2.2210262233306573296e-04, 1.3067392038106924055e-02, -4.4700805721174453923e-01,
		5.5674518371240761397e+00, -2.3517945679239481621e+01, 3.1611322818701131207e+01,
		-9.6090021968656180000e+00,
	}
	besselQ2 = [8]float64{
		-5.5194330231005480228e-04, 3.2547697594819615062e-02, -1.1151759188741312645e+00,
		1.3982595353892851542e+01, -6.0228002066743340583e+01, 8.5539563258012929600e+01,
		-3.1446690275135491500e+01, 1.0,
	}
)
