package fingerprint

import (
	"math"
	"strconv"
)

// Chromaprint audio fingerprinting — the step that has to happen before an
// ISCC Audio-Code can be computed, and the one no Go library provides.
//
// ISO 24138's Audio-Code is a SimHash over a Chromaprint feature vector, and
// iscc-lib's GenAudioCodeV0 takes that vector as given. Nothing official pins
// how audio becomes it: Chromaprint has no specification, only an
// implementation, so the algorithm here is a deliberate re-implementation of
// acoustid/chromaprint's arithmetic rather than an approximation of it — the
// Hamming window scaled by 1/INT16_MAX and rounded to float32 exactly as the
// reference's FFTSample array is, the TEST2 classifier table verbatim, the
// integral-image filters and the Gray-coded packing in the reference's order.
//
// The tests check it against the vector fpcalc prints for
// testdata/iscc_demo_audio.wav, element for element, and that vector is in
// turn the one iscc-sdk publishes for the same recording.
//
// What this package does NOT do is compute the code. Pair it with
// github.com/iscc/iscc-lib/packages/go, which is the official pure-Go
// implementation of ISO 24138 and needs exactly this vector:
//
//	cv, err := fingerprint.Chromaprint(pcm)
//	code, err := iscc.GenAudioCodeV0(cv, 64)

const (
	// chromaSampleRate is the rate Chromaprint fingerprints at. Audio arriving
	// at any other rate has to be resampled to it first.
	chromaSampleRate = 11025

	// chromaFrameSize is the reference's kDefaultFrameSize, and chromaFrameHop
	// the increment it implies: kDefaultFrameOverlap is size - size/3, so a
	// new frame starts every size/3 samples — about 8 frames a second.
	chromaFrameSize = 4096
	chromaFrameHop  = chromaFrameSize / 3

	// chromaMinSampleRate mirrors the reference's kMinSampleRate.
	chromaMinSampleRate = 1000
)

// PCM is decoded audio: interleaved signed 16-bit samples, the form every
// audio decoder can produce and the only form Chromaprint consumes.
//
// Samples holds Channels values per frame, interleaved, so its length must be
// a multiple of Channels. SampleRate is in Hz.
type PCM struct {
	Samples    []int16
	SampleRate int
	Channels   int
}

// Chromaprint returns the raw fingerprint vector for decoded audio — the
// signed 32-bit values `fpcalc -raw -signed` prints, and the input
// iscc.GenAudioCodeV0 takes.
//
// Multi-channel audio is downmixed to mono by integer averaging, as the
// reference does. The audio must already be at 11025 Hz.
//
// Unlike the hashes in this package the result is a slice, not a uint64, so
// Distance and Similarity do not apply to it: two Chromaprint vectors are
// compared by the ISCC code computed from them, not directly.
func Chromaprint(pcm PCM) ([]int32, error) {
	if err := chromaValidate(pcm); err != nil {
		return nil, err
	}
	if pcm.SampleRate != chromaSampleRate {
		return nil, &PHashError{msg: "cannot fingerprint this audio: " +
			strconv.Itoa(pcm.SampleRate) + " Hz needs resampling to 11025 Hz, which this build does not do"}
	}
	mono := chromaMono(pcm)
	fp := chromaFingerprint(mono)
	if len(fp) == 0 {
		ms := len(mono) * 1000 / chromaSampleRate
		return nil, &PHashError{msg: "cannot fingerprint this audio: too short — " +
			strconv.Itoa(ms) + " ms yields no subfingerprints, and an empty vector " +
			"would encode as the one code every silent file shares"}
	}
	return fp, nil
}

// chromaValidate rejects PCM that is not self-consistent, before any work is
// done on it.
func chromaValidate(pcm PCM) error {
	switch {
	case pcm.Channels <= 0:
		return &PHashError{msg: "cannot fingerprint this audio: no channels"}
	case pcm.SampleRate <= chromaMinSampleRate:
		return &PHashError{msg: "cannot fingerprint this audio: sample rate " +
			strconv.Itoa(pcm.SampleRate) + " Hz is at or below the 1000 Hz floor"}
	case len(pcm.Samples)%pcm.Channels != 0:
		return &PHashError{msg: "cannot fingerprint this audio: " +
			strconv.Itoa(len(pcm.Samples)) + " samples is not a whole number of " +
			strconv.Itoa(pcm.Channels) + "-channel frames"}
	}
	return nil
}

// chromaMono downmixes validated PCM to a single channel, by the reference's
// integer arithmetic: a plain average, truncated, per frame.
func chromaMono(pcm PCM) []int16 {
	n := len(pcm.Samples) / pcm.Channels
	out := make([]int16, n)
	switch pcm.Channels {
	case 1:
		copy(out, pcm.Samples)
	case 2:
		for i := range n {
			out[i] = int16((int32(pcm.Samples[2*i]) + int32(pcm.Samples[2*i+1])) / 2)
		}
	default:
		for i := range n {
			var sum int32
			for c := range pcm.Channels {
				sum += int32(pcm.Samples[i*pcm.Channels+c])
			}
			out[i] = int16(sum / int32(pcm.Channels))
		}
	}
	return out
}

// chromaFingerprint runs the reference's pipeline over mono 11025 Hz samples:
// frame and window, FFT, fold the spectrum into 12 pitch classes, smooth and
// normalise, then classify a sliding window of the resulting image.
//
// A trailing partial frame is dropped rather than zero-padded — the reference's
// AudioSlicer keeps it buffered for input that never arrives.
func chromaFingerprint(mono []int16) []int32 {
	fft := newChromaFFT()
	mapper := newChromaMapper(chromaSampleRate)
	filter := newChromaFilter()
	calc := newChromaCalculator()

	spectrum := make([]float64, 1+chromaFrameSize/2)
	for start := 0; start+chromaFrameSize <= len(mono); start += chromaFrameHop {
		fft.transform(mono[start:start+chromaFrameSize], spectrum)
		features := mapper.fold(spectrum)
		smoothed, ok := filter.consume(features)
		if !ok {
			continue
		}
		chromaNormalize(&smoothed)
		calc.consume(smoothed)
	}
	return calc.fingerprint
}

// chromaNormalize scales a feature vector to unit Euclidean length, or zeroes
// it when the norm is below the reference's 0.01 threshold — a frame that
// quiet carries no usable pitch content, and dividing by its norm would
// amplify noise into structure.
func chromaNormalize(f *[chromaBands]float64) {
	var squares float64
	for _, v := range f {
		squares += v * v
	}
	norm := 0.0
	if squares > 0 {
		norm = math.Sqrt(squares)
	}
	if norm < 0.01 {
		*f = [chromaBands]float64{}
		return
	}
	for i := range f {
		f[i] /= norm
	}
}
