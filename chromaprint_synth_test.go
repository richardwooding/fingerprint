package fingerprint_test

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"os"
	"testing"

	"github.com/richardwooding/fingerprint"
)

// writeFixtures regenerates the synthetic WAVs the resampling oracles are
// built from. It is not part of a normal run: see testdata/README.md for the
// fpcalc commands that turn them into committed oracles.
var writeFixtures = flag.String("write-fixtures", "", "directory to write synthetic WAV fixtures into")

// synthRates and synthChannels are the resampling matrix. They cover an
// integer ratio (44100, 22050), a reduced rational one (48000, 96000, 16000,
// 32000), upsampling (8000) and the passthrough case (11025).
var (
	synthRates    = []int{8000, 11025, 16000, 22050, 32000, 44100, 48000, 96000}
	synthChannels = []int{1, 2}
)

// synthSeconds is long enough to yield a useful number of subfingerprints at
// every rate without making the fixtures large.
const synthSeconds = 6

// synthPCM builds deterministic test audio with integer arithmetic only.
//
// No math.Sin anywhere: Go permits fused multiply-add, so floating-point
// synthesis is not guaranteed to produce identical bytes on every
// architecture, and an oracle generated on one machine would then fail on
// another. Integers have one answer everywhere.
//
// The signal is three triangle partials at musical ratios plus a seeded noise
// floor and a slow envelope, which keeps every chroma bin occupied and the
// frames non-stationary — a pure tone would leave most of the pipeline
// untested.
func synthPCM(rate, channels int) fingerprint.PCM {
	n := rate * synthSeconds
	samples := make([]int16, n*channels)

	periods := [3]int{max(rate/220, 2), max(rate/330, 2), max(rate/550, 2)}
	amps := [3]int32{9000, 4200, 2000}
	noise := uint32(0x9e3779b9)

	for i := range n {
		var v int32
		for j, p := range periods {
			// A triangle: up for half the period, down for the other half.
			phase := int32((i + j*7) % p)
			t := phase * 4 * amps[j] / int32(p)
			if t > 2*amps[j] {
				t = 4*amps[j] - t
			}
			v += t - amps[j]
		}
		noise = noise*1664525 + 1013904223
		v += int32(noise>>25) - 64

		// A slow triangular envelope over the whole clip, so the frames are
		// not all alike.
		env := int32(i % n * 2 / n)
		if env == 0 {
			v = v * (int32(i)*512/int32(n) + 512) / 1024
		} else {
			v = v * (1024 - int32(i)*512/int32(n)) / 1024
		}

		for c := range channels {
			// Give the channels different content so the downmix is exercised.
			s := v
			if c == 1 {
				s = v/2 + int32((i*3)%1024) - 512
			}
			samples[i*channels+c] = int16(min(max(s, -32768), 32767))
		}
	}
	return fingerprint.PCM{Samples: samples, SampleRate: rate, Channels: channels}
}

// pcmDigest fingerprints the generated samples themselves, so that an edit to
// synthPCM can never silently invalidate a committed oracle: the digest fails
// first, and says so.
func pcmDigest(pcm fingerprint.PCM) string {
	h := sha256.New()
	b := make([]byte, 2)
	for _, s := range pcm.Samples {
		binary.LittleEndian.PutUint16(b, uint16(s))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// synthDigests pins the generator's output. If one of these fails, the
// committed oracles are stale and must be regenerated with fpcalc, not the
// assertion relaxed — the whole point is that a change to synthPCM cannot
// quietly invalidate an oracle it can no longer reproduce.
var synthDigests = map[string]string{
	"8000_1": "99d3e9b8208c2875", "8000_2": "8b3e69c6f19387a4",
	"11025_1": "c49719fdc8ec5a3f", "11025_2": "82bff612117f164d",
	"16000_1": "121d20c24a80b0c0", "16000_2": "0cc2c135c6b5eb11",
	"22050_1": "44d93e685153a7c4", "22050_2": "55668ddbb839d67b",
	"32000_1": "f98b99642b2b39e5", "32000_2": "698edc69f6950013",
	"44100_1": "349834dff8872fe8", "44100_2": "55283bc51f4f64d8",
	"48000_1": "f0d8a4e0111550df", "48000_2": "6d5693fcc209a8eb",
	"96000_1": "4976de92989f7777", "96000_2": "6c7b5ec45124644a",
}

// TestChromaprintResamplingMatchesReference is the resampling conformance
// test: for every rate and channel count, the vector must be exactly the one
// fpcalc prints for the same audio.
//
// The matrix is what makes it meaningful. 11025 is the passthrough case;
// 44100 and 22050 are integer ratios; 48000, 96000, 16000 and 32000 reduce to
// rationals with many phases; 8000 upsamples. They exercise different paths
// through the phase accumulator, and a resampler can be right about one and
// wrong about the rest.
func TestChromaprintResamplingMatchesReference(t *testing.T) {
	for _, rate := range synthRates {
		for _, ch := range synthChannels {
			name := itoa(rate) + "_" + itoa(ch)
			t.Run(name, func(t *testing.T) {
				pcm := synthPCM(rate, ch)
				if got, want := pcmDigest(pcm), synthDigests[name]; got != want {
					t.Fatalf("synthPCM drifted: digest %s, want %s — regenerate the oracles", got, want)
				}

				got, err := fingerprint.Chromaprint(pcm)
				if err != nil {
					t.Fatalf("Chromaprint: %v", err)
				}
				want := loadFpcalcVector(t, "chromaprint_synth_"+name+".fpcalc.json")
				if len(got) != len(want) {
					t.Fatalf("length: got %d subfingerprints, want %d", len(got), len(want))
				}
				var values, bits int
				for i := range got {
					if got[i] == want[i] {
						continue
					}
					values++
					for x := uint32(got[i]) ^ uint32(want[i]); x != 0; x &= x - 1 {
						bits++
					}
				}
				if values != 0 {
					t.Errorf("differs from fpcalc: %d of %d values, %d of %d bits",
						values, len(got), bits, 32*len(got))
				}
			})
		}
	}
}

// TestWriteSynthFixtures writes the synthetic WAVs so fpcalc can be run over
// them. It does nothing unless -write-fixtures names a directory.
func TestWriteSynthFixtures(t *testing.T) {
	if *writeFixtures == "" {
		t.Skip("pass -write-fixtures=DIR to regenerate")
	}
	for _, rate := range synthRates {
		for _, ch := range synthChannels {
			pcm := synthPCM(rate, ch)
			name := *writeFixtures + "/synth_" + itoa(rate) + "_" + itoa(ch) + ".wav"
			if err := os.WriteFile(name, wavBytes(pcm), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Logf("wrote %s (%s)", name, pcmDigest(pcm))
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// wavBytes wraps PCM in a canonical 16-bit RIFF/WAVE header.
func wavBytes(pcm fingerprint.PCM) []byte {
	data := make([]byte, len(pcm.Samples)*2)
	for i, s := range pcm.Samples {
		binary.LittleEndian.PutUint16(data[2*i:], uint16(s))
	}
	blockAlign := pcm.Channels * 2
	out := make([]byte, 0, 44+len(data))
	u32 := func(v uint32) { out = binary.LittleEndian.AppendUint32(out, v) }
	u16 := func(v uint16) { out = binary.LittleEndian.AppendUint16(out, v) }
	out = append(out, "RIFF"...)
	u32(uint32(36 + len(data)))
	out = append(out, "WAVEfmt "...)
	u32(16)
	u16(1)
	u16(uint16(pcm.Channels))
	u32(uint32(pcm.SampleRate))
	u32(uint32(pcm.SampleRate * blockAlign))
	u16(uint16(blockAlign))
	u16(16)
	out = append(out, "data"...)
	u32(uint32(len(data)))
	return append(out, data...)
}
