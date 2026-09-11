package fingerprint_test

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/richardwooding/fingerprint"
)

// loadPCM reads a 16-bit PCM WAV fixture into raw samples. It parses the
// container by hand rather than calling ChromaprintFromWAV, so the tests that
// need PCM stay independent of the reader they are meant to cross-check.
func loadPCM(t testing.TB, name string) fingerprint.PCM {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}

	var pcm fingerprint.PCM
	var data []byte
	for off := 12; off+8 <= len(b); {
		size := int(binary.LittleEndian.Uint32(b[off+4 : off+8]))
		if off+8+size > len(b) {
			break
		}
		switch body := b[off+8 : off+8+size]; string(b[off : off+4]) {
		case "fmt ":
			pcm.Channels = int(binary.LittleEndian.Uint16(body[2:4]))
			pcm.SampleRate = int(binary.LittleEndian.Uint32(body[4:8]))
			if bits := binary.LittleEndian.Uint16(body[14:16]); bits != 16 {
				t.Fatalf("%s: fixture must be 16-bit PCM, got %d-bit", name, bits)
			}
		case "data":
			data = body
		}
		off += 8 + size + size%2
	}
	pcm.Samples = make([]int16, len(data)/2)
	for i := range pcm.Samples {
		pcm.Samples[i] = int16(binary.LittleEndian.Uint16(data[2*i:]))
	}
	if len(pcm.Samples) == 0 {
		t.Fatalf("%s: no samples — fixture missing or malformed", name)
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
		"too many channels to resample": {
			fingerprint.PCM{Samples: make([]int16, 300000), SampleRate: 44100, Channels: 6},
			"layout-aware downmix coefficients",
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

// TestChromaprintManyChannelsAtTargetRate is the other side of the
// multi-channel refusal: at 11025 Hz no resampling happens, so the
// fingerprinter's own integer downmix applies and any channel count is fine.
// The refusal is about reproducing FFmpeg's downmix, not about the channels.
func TestChromaprintManyChannelsAtTargetRate(t *testing.T) {
	mono := loadPCM(t, "iscc_demo_audio.wav")
	const channels = 6
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
		t.Fatalf("Chromaprint: %v", err)
	}
	want, err := fingerprint.Chromaprint(mono)
	if err != nil {
		t.Fatalf("mono: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Error("six identical channels changed the fingerprint")
	}
}
