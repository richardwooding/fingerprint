package fingerprint_test

import (
	"fmt"
	"os"

	"github.com/richardwooding/fingerprint"
)

// The raw Chromaprint vector an ISO 24138 Audio-Code is computed from. Pair
// this with github.com/iscc/iscc-lib/packages/go, which takes exactly this
// vector:
//
//	code, err := iscc.GenAudioCodeV0(cv, 64)  // ISCC:EIAWUJFCEZZOJYVD
//
// This package computes no code itself, and takes no dependency on one.
func Example_chromaprint() {
	f, err := os.Open("testdata/iscc_demo_audio.wav")
	if err != nil {
		panic(err)
	}
	defer func() { _ = f.Close() }()

	cv, err := fingerprint.ChromaprintFromWAV(f)
	if err != nil {
		panic(err)
	}
	fmt.Println("subfingerprints:", len(cv))
	fmt.Printf("first three: %d\n", cv[:3])
	// Output:
	// subfingerprints: 104
	// first three: [684003877 683946551 1749295639]
}

// Audio that arrives already decoded skips the reader. Chromaprint takes
// interleaved 16-bit samples at whatever rate they are in, and does the
// downmix and the resampling itself.
func Example_chromaprintFromSamples() {
	samples := make([]int16, 44100*2*5) // five seconds of stereo silence
	cv, err := fingerprint.Chromaprint(fingerprint.PCM{
		Samples:    samples,
		SampleRate: 44100,
		Channels:   2,
	})
	if err != nil {
		panic(err)
	}
	fmt.Println("subfingerprints:", len(cv))
	// Output:
	// subfingerprints: 19
}
