package fingerprint_test

import (
	"bytes"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/richardwooding/fingerprint"
)

// TestChromaprintFromMP3MatchesReference is the reader's conformance test,
// and the strongest fixture in the package: the file is the one iscc-sdk
// publishes a Chromaprint vector for, so matching fpcalc here matches the
// reference implementation's own published numbers.
//
// It is also the test that proves the gapless trimming is right. Without it
// the samples start 2257 frames early, every 124 ms frame is re-cut, and 222
// of these 3328 bits come out wrong.
func TestChromaprintFromMP3MatchesReference(t *testing.T) {
	f, err := os.Open("testdata/iscc_demo_audio.mp3")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	got, err := fingerprint.ChromaprintFromMP3(f)
	if err != nil {
		t.Fatalf("ChromaprintFromMP3: %v", err)
	}
	want := loadFpcalcVector(t, "iscc_demo_audio.mp3.fpcalc.json")

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
}

// TestMP3AgreesWithTheLosslessMaster is the robustness claim, stated as a
// bound rather than an equality. The MP3 and the 11025 Hz mono WAV are the
// same recording through very different pipelines — a lossy codec and a
// resampler — so their fingerprints are allowed to move a little. How little
// is the interesting number, and a regression that broke either path would
// blow through it.
func TestMP3AgreesWithTheLosslessMaster(t *testing.T) {
	mp3File, err := os.Open("testdata/iscc_demo_audio.mp3")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mp3File.Close() }()
	fromMP3, err := fingerprint.ChromaprintFromMP3(mp3File)
	if err != nil {
		t.Fatal(err)
	}

	wavFile, err := os.Open("testdata/iscc_demo_audio.wav")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wavFile.Close() }()
	fromWAV, err := fingerprint.ChromaprintFromWAV(wavFile)
	if err != nil {
		t.Fatal(err)
	}

	if len(fromMP3) != len(fromWAV) {
		t.Fatalf("lengths differ: %d from the MP3, %d from the WAV", len(fromMP3), len(fromWAV))
	}
	var bits int
	for i := range fromMP3 {
		for x := uint32(fromMP3[i]) ^ uint32(fromWAV[i]); x != 0; x &= x - 1 {
			bits++
		}
	}
	// Measured at 2. The bound is deliberately close: this is the headline
	// robustness claim, and it should fail if it stops being true.
	if bits > 4 {
		t.Errorf("MP3 and lossless differ in %d of %d bits, want at most 4", bits, 32*len(fromMP3))
	}
	t.Logf("MP3 against the lossless master: %d of %d bits", bits, 32*len(fromMP3))
}

// TestMP3Refusals covers the streams that must not produce a vector.
//
// The MPEG-2 case is the important one and it is a deliberate narrowing: the
// decoder behind this rejects MPEG-2.5 outright and mis-decodes some MPEG-2
// configurations badly enough to fingerprint as unrelated audio, with nothing
// in the header separating the good from the bad. Everything music is
// actually stored as is MPEG-1.
func TestMP3Refusals(t *testing.T) {
	good, err := os.ReadFile("testdata/iscc_demo_audio.mp3")
	if err != nil {
		t.Fatal(err)
	}

	// Flip the version field of the first frame header to MPEG-2.
	mpeg2 := append([]byte{}, good...)
	if i := bytes.Index(mpeg2, []byte{0xff, 0xfb}); i >= 0 {
		mpeg2[i+1] = 0xf3 // version bits 10 = MPEG-2
	} else {
		t.Fatal("no frame sync in the fixture")
	}

	// Blank the Xing tag so the delay cannot be read.
	noXing := append([]byte{}, good...)
	if i := bytes.Index(noXing, []byte("Xing")); i >= 0 {
		copy(noXing[i:i+4], "xxxx")
	} else {
		t.Fatal("no Xing header in the fixture")
	}

	cases := map[string]struct {
		in   []byte
		want string
	}{
		"empty":       {nil, "no MPEG audio frame"},
		"not audio":   {[]byte("this is not an MP3 file at all, not even close"), "no MPEG audio frame"},
		"mpeg 2":      {mpeg2, "MPEG-2 or 2.5"},
		"no Xing":     {noXing, "no Xing or Info header"},
		"header only": {good[:200], "no MPEG audio frame"},
		"id3 only":    {good[:4129], "no MPEG audio frame"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := fingerprint.ChromaprintFromMP3(bytes.NewReader(tc.in))
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

// TestMP3IsNotConfusedByID3 checks the tag skip: the fixture carries a 4119
// byte ID3v2 header, and reading its syncsafe size wrong lands the frame
// search in the middle of cover art.
func TestMP3IsNotConfusedByID3(t *testing.T) {
	good, err := os.ReadFile("testdata/iscc_demo_audio.mp3")
	if err != nil {
		t.Fatal(err)
	}
	if string(good[0:3]) != "ID3" {
		t.Skip("the fixture no longer carries an ID3v2 tag")
	}
	want, err := fingerprint.ChromaprintFromMP3(bytes.NewReader(good))
	if err != nil {
		t.Fatal(err)
	}

	// The same audio with the tag removed must fingerprint identically.
	size := int(good[6]&0x7f)<<21 | int(good[7]&0x7f)<<14 | int(good[8]&0x7f)<<7 | int(good[9]&0x7f)
	got, err := fingerprint.ChromaprintFromMP3(bytes.NewReader(good[10+size:]))
	if err != nil {
		t.Fatalf("without the ID3 tag: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Error("stripping the ID3v2 tag changed the fingerprint")
	}
}
