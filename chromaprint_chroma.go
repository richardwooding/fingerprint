package fingerprint

import "math"

// Folding a spectrum into pitch classes, and smoothing the result over time.
//
// A chroma vector throws away octave: every FFT bin is assigned to one of the
// twelve semitones and its energy added there, so a melody played an octave up
// lands in the same bins. That is what makes the fingerprint survive
// re-encoding and pitch-preserving edits, and it is why the vector is only
// twelve wide.

const (
	// chromaBands is the number of pitch classes — the reference's NUM_BANDS.
	chromaBands = 12

	// chromaMinFreq and chromaMaxFreq bound the bins that get folded, in Hz.
	// The reference's MIN_FREQ and MAX_FREQ: below 28 Hz a 4096-sample frame
	// has no resolution to speak of, and above 3520 Hz (A7) the harmonics are
	// no longer reliably a pitch.
	chromaMinFreq = 28
	chromaMaxFreq = 3520

	// chromaBaseFreq is A0, the reference's 440/16 Hz — the origin the octave
	// is measured from.
	chromaBaseFreq = 440.0 / 16.0
)

// chromaMapper holds the precomputed bin-to-pitch-class assignment for a fixed
// frame size and sample rate.
type chromaMapper struct {
	notes    []uint8 // pitch class per bin, valid on [minIndex, maxIndex)
	minIndex int
	maxIndex int
}

// newChromaMapper precomputes which pitch class each FFT bin belongs to.
func newChromaMapper(sampleRate int) *chromaMapper {
	m := &chromaMapper{
		notes:    make([]uint8, chromaFrameSize),
		minIndex: max(1, chromaFreqToIndex(chromaMinFreq, sampleRate)),
		maxIndex: min(chromaFrameSize/2, chromaFreqToIndex(chromaMaxFreq, sampleRate)),
	}
	for i := m.minIndex; i < m.maxIndex; i++ {
		freq := float64(i) * float64(sampleRate) / chromaFrameSize
		octave := math.Log(freq/chromaBaseFreq) / math.Log(2.0)
		note := chromaBands * (octave - math.Floor(octave))
		m.notes[i] = uint8(note)
	}
	return m
}

// chromaFreqToIndex is the reference's FreqToIndex: the FFT bin a frequency
// falls in, rounded rather than truncated.
func chromaFreqToIndex(freq float64, sampleRate int) int {
	return int(math.Round(chromaFrameSize * freq / float64(sampleRate)))
}

// fold sums each in-range bin's energy into its pitch class.
//
// The reference can interpolate energy between neighbouring classes, but TEST2
// — the default algorithm, and the one ISCC's vectors come from — leaves that
// off, so this does not implement it.
func (m *chromaMapper) fold(spectrum []float64) [chromaBands]float64 {
	var features [chromaBands]float64
	for i := m.minIndex; i < m.maxIndex; i++ {
		features[m.notes[i]] += spectrum[i]
	}
	return features
}

// chromaFilterCoefficients is the reference's kChromaFilterCoefficients: a
// 5-tap smoothing window over time, so a single noisy frame cannot swing the
// image the classifiers read.
var chromaFilterCoefficients = [5]float64{0.25, 0.75, 1.0, 0.75, 0.25}

// chromaFilter applies that smoothing across consecutive feature vectors.
type chromaFilter struct {
	buffer [8][chromaBands]float64 // the reference's ring, sized 8 for a 5-tap window
	offset int
	size   int
}

// newChromaFilter starts the ring. The reference seeds its buffer size at 1
// rather than 0, so the filter emits nothing until it has seen five frames and
// the first four are consumed to fill the window.
func newChromaFilter() *chromaFilter {
	return &chromaFilter{size: 1}
}

// consume feeds one feature vector in and reports whether a smoothed vector
// came out. The first four calls return false while the window fills.
func (f *chromaFilter) consume(features [chromaBands]float64) ([chromaBands]float64, bool) {
	const taps = len(chromaFilterCoefficients)

	f.buffer[f.offset] = features
	f.offset = (f.offset + 1) % len(f.buffer)

	if f.size < taps {
		f.size++
		return [chromaBands]float64{}, false
	}

	var result [chromaBands]float64
	start := (f.offset + len(f.buffer) - taps) % len(f.buffer)
	for i := range chromaBands {
		for j, coeff := range chromaFilterCoefficients {
			result[i] += f.buffer[(start+j)%len(f.buffer)][i] * coeff
		}
	}
	return result, true
}
