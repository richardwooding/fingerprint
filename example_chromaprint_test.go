package fingerprint_test

import (
	"encoding/binary"
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
	pcm, err := readTestWAV("testdata/iscc_demo_audio.wav")
	if err != nil {
		panic(err)
	}

	cv, err := fingerprint.Chromaprint(pcm)
	if err != nil {
		panic(err)
	}
	fmt.Println("subfingerprints:", len(cv))
	fmt.Printf("first three: %d\n", cv[:3])
	// Output:
	// subfingerprints: 104
	// first three: [684003877 683946551 1749295639]
}

// readTestWAV is just enough RIFF parsing to reach the samples in the
// 16-bit PCM fixture. Decoding audio is the caller's job — this package
// takes decoded samples, the way ISCCPixels takes a decoded image.
func readTestWAV(path string) (fingerprint.PCM, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return fingerprint.PCM{}, err
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
		case "data":
			data = body
		}
		off += 8 + size + size%2
	}
	pcm.Samples = make([]int16, len(data)/2)
	for i := range pcm.Samples {
		pcm.Samples[i] = int16(binary.LittleEndian.Uint16(data[2*i:]))
	}
	return pcm, nil
}
