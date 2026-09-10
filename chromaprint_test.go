package fingerprint_test

import (
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/richardwooding/fingerprint"
)

// loadPCM reads one of the 16-bit PCM WAV fixtures, via the same helper the
// godoc example uses. Decoding is deliberately outside the package: its
// contract is decoded samples, the way ISCCPixels takes a decoded image.
func loadPCM(t testing.TB, name string) fingerprint.PCM {
	t.Helper()
	pcm, err := readTestWAV("testdata/" + name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if len(pcm.Samples) == 0 {
		t.Fatalf("%s: no samples — fixture missing or not 16-bit PCM", name)
	}
	return pcm
}

// loadFpcalcVector reads the committed stdout of
// `fpcalc -raw -json -signed -length 0`. See testdata/README.md for the
// command and the fpcalc version that produced each one.
func loadFpcalcVector(t testing.TB, name string) []int32 {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var doc struct {
		Duration    float64 `json:"duration"`
		Fingerprint []int32 `json:"fingerprint"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	if len(doc.Fingerprint) == 0 {
		t.Fatalf("%s: no fingerprint in the oracle", name)
	}
	return doc.Fingerprint
}

// TestChromaprintMatchesReference is the conformance test.
//
// Chromaprint has no specification, so the reference implementation's output
// is the whole of the contract, and this asserts every bit of it: all 104
// subfingerprints for a real recording, identical to what fpcalc prints. That
// same vector is the one iscc-sdk publishes for this recording, so matching it
// pins the pipeline against two independent sources at once.
func TestChromaprintMatchesReference(t *testing.T) {
	pcm := loadPCM(t, "iscc_demo_audio.wav")
	if pcm.SampleRate != 11025 || pcm.Channels != 1 {
		t.Fatalf("fixture drifted: got %d Hz, %d channels", pcm.SampleRate, pcm.Channels)
	}

	got, err := fingerprint.Chromaprint(pcm)
	if err != nil {
		t.Fatalf("Chromaprint: %v", err)
	}
	want := loadFpcalcVector(t, "iscc_demo_audio.fpcalc.json")

	if len(got) != len(want) {
		t.Fatalf("length: got %d subfingerprints, want %d", len(got), len(want))
	}
	// Report bits, not just values: a fingerprint is a packed bit field, so
	// "three values differ" says nothing about whether the cause is one
	// borderline quantiser decision or a broken stage.
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
}

// TestChromaprintDownmixIsAveraging checks the multi-channel path against the
// mono one without needing a second oracle: duplicating a mono signal into
// every channel must average back to exactly the original, so the fingerprint
// must be unchanged.
func TestChromaprintDownmixIsAveraging(t *testing.T) {
	mono := loadPCM(t, "iscc_demo_audio.wav")
	want, err := fingerprint.Chromaprint(mono)
	if err != nil {
		t.Fatalf("mono: %v", err)
	}

	for _, channels := range []int{2, 4} {
		wide := fingerprint.PCM{
			Samples:    make([]int16, len(mono.Samples)*channels),
			SampleRate: mono.SampleRate,
			Channels:   channels,
		}
		for i, s := range mono.Samples {
			for c := range channels {
				wide.Samples[i*channels+c] = s
			}
		}
		got, err := fingerprint.Chromaprint(wide)
		if err != nil {
			t.Fatalf("%d channels: %v", channels, err)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%d identical channels changed the fingerprint", channels)
		}
	}
}

// TestChromaprintRefusals covers the inputs that must not produce a vector.
//
// The short case matters most: the reference happily returns nothing for audio
// under about three seconds, and GenAudioCodeV0 turns an empty vector into a
// perfectly well-formed code built from a zero digest — the same code for
// every such file. Refusing is the only honest answer.
func TestChromaprintRefusals(t *testing.T) {
	cases := map[string]struct {
		pcm  fingerprint.PCM
		want string
	}{
		"no channels": {
			fingerprint.PCM{Samples: make([]int16, 100), SampleRate: 11025, Channels: 0},
			"no channels",
		},
		"rate at the floor": {
			fingerprint.PCM{Samples: make([]int16, 100), SampleRate: 1000, Channels: 1},
			"1000 Hz floor",
		},
		"ragged frames": {
			fingerprint.PCM{Samples: make([]int16, 101), SampleRate: 11025, Channels: 2},
			"whole number of 2-channel frames",
		},
		"needs resampling": {
			fingerprint.PCM{Samples: make([]int16, 100000), SampleRate: 44100, Channels: 1},
			"needs resampling to 11025 Hz",
		},
		"too short": {
			fingerprint.PCM{Samples: make([]int16, 11025), SampleRate: 11025, Channels: 1},
			"too short",
		},
		"empty": {
			fingerprint.PCM{Samples: nil, SampleRate: 11025, Channels: 1},
			"too short",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := fingerprint.Chromaprint(tc.pcm)
			if err == nil {
				t.Fatalf("got %d values, want a refusal", len(got))
			}
			if got != nil {
				t.Errorf("refused but still returned %d values", len(got))
			}
			var perr *fingerprint.PHashError
			if !errors.As(err, &perr) {
				t.Errorf("error is %T, want *fingerprint.PHashError", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// TestChromaprintSilenceStillFingerprints guards a property the refusal above
// must not accidentally swallow: long silence is degenerate content, not an
// error, and the reference does produce a vector for it.
func TestChromaprintSilenceStillFingerprints(t *testing.T) {
	pcm := fingerprint.PCM{
		Samples:    make([]int16, 11025*10),
		SampleRate: 11025,
		Channels:   1,
	}
	got, err := fingerprint.Chromaprint(pcm)
	if err != nil {
		t.Fatalf("Chromaprint: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("no subfingerprints for ten seconds of silence")
	}
	// Every frame normalises to zero, so every window is identical and every
	// subfingerprint is the same word.
	for i, v := range got {
		if v != got[0] {
			t.Fatalf("silence produced varying subfingerprints: [0]=%d [%d]=%d", got[0], i, v)
		}
	}
}
