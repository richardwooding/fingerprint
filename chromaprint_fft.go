package fingerprint

import "math"

// The FFT half of the Chromaprint pipeline: window a frame, transform it, and
// hand on the power spectrum.
//
// Two details here are the reference's arithmetic rather than the obvious
// choice, and both are load-bearing.
//
// The window is scaled by 1/INT16_MAX and stored in a float array
// (FFTSample), so every coefficient is rounded to float32 before it is ever
// used, and so is every windowed sample. That rounding is deterministic, it is
// visible in the output, and it costs nothing to reproduce — so this does,
// storing the same float32 values the reference stores.
//
// The transform itself is not reproducible to the bit. The reference computes
// it in whichever library it was built against — FFTW, KissFFT, vDSP or
// FFmpeg's — and the FFmpeg backends work in float32 throughout, so "the"
// reference spectrum is really a build's spectrum, accurate to about one part
// in ten million. There is no bit-exact target to hit, so this computes the
// transform in float64 instead: as close to the true value as the input
// allows, which is the closest thing to every build at once.

// chromaFFT windows and transforms one frame at a time. Its buffers are
// reused across frames, so it is not safe for concurrent use.
type chromaFFT struct {
	window  []float32 // Hamming, pre-scaled by 1/INT16_MAX, rounded as the reference rounds it
	re, im  []float64 // transform workspace, chromaFrameSize wide
	cosTab  []float64 // twiddle factors, indexed by bit-reversal stage
	sinTab  []float64
	revBits []int // bit-reversal permutation for chromaFrameSize
}

// newChromaFFT builds the window and the twiddle tables for the fixed
// 4096-sample frame.
func newChromaFFT() *chromaFFT {
	const n = chromaFrameSize
	f := &chromaFFT{
		window:  make([]float32, n),
		re:      make([]float64, n),
		im:      make([]float64, n),
		cosTab:  make([]float64, n/2),
		sinTab:  make([]float64, n/2),
		revBits: make([]int, n),
	}

	// PrepareHammingWindow(m_window, m_window + frame_size, 1.0 / INT16_MAX).
	// Computed in double, as the reference computes it, then narrowed to
	// float32, as the reference stores it.
	const scale = 1.0 / 32767.0
	for i := range n {
		f.window[i] = float32(scale * (0.54 - 0.46*math.Cos(float64(i)*2.0*math.Pi/float64(n-1))))
	}

	for i := range n / 2 {
		angle := -2.0 * math.Pi * float64(i) / float64(n)
		f.cosTab[i] = math.Cos(angle)
		f.sinTab[i] = math.Sin(angle)
	}

	bits := 0
	for 1<<bits < n {
		bits++
	}
	for i := range n {
		rev := 0
		for b := range bits {
			if i&(1<<b) != 0 {
				rev |= 1 << (bits - 1 - b)
			}
		}
		f.revBits[i] = rev
	}
	return f
}

// transform windows frame and writes its power spectrum into out, which must
// hold 1+chromaFrameSize/2 values. Bin i is |X(i)|², matching the reference,
// which squares without taking a root because the chroma stage only ever sums
// and normalises the result.
func (f *chromaFFT) transform(frame []int16, out []float64) {
	const n = chromaFrameSize

	// ApplyWindow: int16 promoted to float, multiplied by the float window,
	// and stored back into a float array. Every product is a float32.
	for i := range n {
		f.re[f.revBits[i]] = float64(float32(frame[i]) * f.window[i])
		f.im[f.revBits[i]] = 0
	}

	for size := 2; size <= n; size <<= 1 {
		half := size / 2
		step := n / size
		for start := 0; start < n; start += size {
			for k := range half {
				c, s := f.cosTab[k*step], f.sinTab[k*step]
				i, j := start+k, start+k+half
				tr := f.re[j]*c - f.im[j]*s
				ti := f.re[j]*s + f.im[j]*c
				f.re[j] = f.re[i] - tr
				f.im[j] = f.im[i] - ti
				f.re[i] += tr
				f.im[i] += ti
			}
		}
	}

	for i := range n/2 + 1 {
		out[i] = f.re[i]*f.re[i] + f.im[i]*f.im[i]
	}
}
