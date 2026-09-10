package fingerprint_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/richardwooding/fingerprint"
)

// TestChromaprintFromWAVMatchesReference is the reader's conformance test:
// handed the real-music fixture it must produce the same vector fpcalc does,
// which is also what Chromaprint produces from the same samples read by hand.
func TestChromaprintFromWAVMatchesReference(t *testing.T) {
	f, err := os.Open("testdata/iscc_demo_audio.wav")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	got, err := fingerprint.ChromaprintFromWAV(f)
	if err != nil {
		t.Fatalf("ChromaprintFromWAV: %v", err)
	}
	if want := loadFpcalcVector(t, "iscc_demo_audio.fpcalc.json"); !slices.Equal(got, want) {
		t.Errorf("differs from fpcalc: got %d values, want %d", len(got), len(want))
	}
}

// TestWAVSampleFormatsAgree checks the widening and narrowing arithmetic
// without needing an oracle per format: the same signal written as 16-, 24-
// and 32-bit integers, and as 32- and 64-bit floats, carries the same 16 bits
// of information, so all five must fingerprint identically.
//
// 8-bit is deliberately excluded — it genuinely loses information, and is
// covered separately.
func TestWAVSampleFormatsAgree(t *testing.T) {
	pcm := synthPCM(44100, 2)
	want, err := fingerprint.Chromaprint(pcm)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}

	for _, tc := range []struct {
		name  string
		bytes []byte
	}{
		{"s16", wavIn(pcm, 1, 16)},
		{"s24", wavIn(pcm, 1, 24)},
		{"s32", wavIn(pcm, 1, 32)},
		{"f32", wavIn(pcm, 3, 32)},
		{"f64", wavIn(pcm, 3, 64)},
		{"extensible s16", wavExtensible(pcm)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := fingerprint.ChromaprintFromWAV(bytes.NewReader(tc.bytes))
			if err != nil {
				t.Fatalf("ChromaprintFromWAV: %v", err)
			}
			if !slices.Equal(got, want) {
				t.Errorf("fingerprint differs from the 16-bit baseline")
			}
		})
	}
}

// TestWAVEightBitIsLossyButWorks covers the one width that cannot agree: 8-bit
// keeps the top byte only, so the fingerprint may move. It must still be
// produced, and still be the length the same audio gives at 16 bits.
func TestWAVEightBitIsLossyButWorks(t *testing.T) {
	pcm := synthPCM(44100, 2)
	want, err := fingerprint.Chromaprint(pcm)
	if err != nil {
		t.Fatal(err)
	}
	got, err := fingerprint.ChromaprintFromWAV(bytes.NewReader(wavIn(pcm, 1, 8)))
	if err != nil {
		t.Fatalf("ChromaprintFromWAV: %v", err)
	}
	if len(got) != len(want) {
		t.Errorf("got %d subfingerprints, want %d", len(got), len(want))
	}
}

// TestWAVSkipsUnknownChunks is what makes the reader usable on real files,
// which carry LIST, fact, bext and more between fmt and data — including
// odd-sized ones, whose pad byte the reader has to consume or every
// subsequent chunk header lands one byte out.
func TestWAVSkipsUnknownChunks(t *testing.T) {
	pcm := synthPCM(11025, 1)
	want, err := fingerprint.Chromaprint(pcm)
	if err != nil {
		t.Fatal(err)
	}

	plain := wavIn(pcm, 1, 16)
	fmtEnd := 12 + 8 + 16
	var padded []byte
	padded = append(padded, plain[:fmtEnd]...)
	padded = append(padded, chunk("LIST", []byte("INFOhello"))...) // odd, 9 bytes
	padded = append(padded, chunk("fact", []byte{1, 2, 3, 4})...)  // even
	padded = append(padded, chunk("junk", []byte{9})...)           // odd, 1 byte
	padded = append(padded, plain[fmtEnd:]...)
	// The RIFF size field is not read, but keep it honest anyway.
	binary.LittleEndian.PutUint32(padded[4:], uint32(len(padded)-8))

	got, err := fingerprint.ChromaprintFromWAV(bytes.NewReader(padded))
	if err != nil {
		t.Fatalf("ChromaprintFromWAV: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Error("interposed chunks changed the fingerprint")
	}
}

// TestWAVRefusals covers the inputs that must not produce a vector. Every one
// of them would otherwise be fingerprinted as some other content.
func TestWAVRefusals(t *testing.T) {
	pcm := synthPCM(11025, 1)
	good := wavIn(pcm, 1, 16)

	cases := map[string]struct {
		in   []byte
		want string
	}{
		"empty":         {nil, "the stream ends early"},
		"not RIFF":      {[]byte("ID3\x04\x00\x00\x00\x00\x00\x00\x00\x00extra"), "not a RIFF/WAVE stream"},
		"RIFF not WAVE": {append([]byte("RIFF\x00\x00\x00\x00AVI "), make([]byte, 8)...), "not a RIFF/WAVE stream"},
		"no data chunk": {good[:12+8+16], "no data chunk"},
		"compressed":    {wavIn(pcm, 0x0011, 4), "compressed"},
		"short fmt":     {append(append([]byte{}, good[:12]...), chunk("fmt ", make([]byte, 8))...), "too short to describe a format"},
		"odd bit width": {wavIn(pcm, 1, 12), "not a WAV format this reads"},
		"data before fmt": {func() []byte {
			out := append([]byte{}, good[:12]...)
			return append(out, chunk("data", make([]byte, 100))...)
		}(), "before the fmt chunk"},
		"truncated data": {good[:len(good)-1000], "the stream ends early"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := fingerprint.ChromaprintFromWAV(bytes.NewReader(tc.in))
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

// TestWAVRaggedFinalFrame checks that a data chunk holding half a stereo
// frame drops it rather than rotating the channels for the rest of the file.
// Chromaprint itself refuses ragged PCM, so without the trim this would be an
// error rather than a fingerprint.
func TestWAVRaggedFinalFrame(t *testing.T) {
	pcm := synthPCM(11025, 2)
	want, err := fingerprint.Chromaprint(pcm)
	if err != nil {
		t.Fatal(err)
	}

	// One sample short of a whole number of stereo frames.
	ragged := pcm
	ragged.Samples = pcm.Samples[:len(pcm.Samples)-1]
	if _, err := fingerprint.Chromaprint(ragged); err == nil {
		t.Fatal("the ragged PCM was accepted directly; the reader's trim is untested")
	}

	got, err := fingerprint.ChromaprintFromWAV(bytes.NewReader(wavIn(ragged, 1, 16)))
	if err != nil {
		t.Fatalf("ChromaprintFromWAV: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("dropping half a frame changed the fingerprint: got %d values, want %d", len(got), len(want))
	}
}

// --- fixture builders -------------------------------------------------------

// wavIn re-encodes PCM into a WAV of the given format tag and bit width, so
// the tests can cover every width the reader claims without carrying a
// fixture for each.
func wavIn(pcm fingerprint.PCM, format uint16, bits int) []byte {
	var data []byte
	for _, s := range pcm.Samples {
		switch {
		case format == 1 && bits == 8:
			data = append(data, byte(int(s>>8)+128))
		case format == 1 && bits == 16:
			data = binary.LittleEndian.AppendUint16(data, uint16(s))
		case format == 1 && bits == 24:
			v := int32(s) << 8
			data = append(data, byte(v), byte(v>>8), byte(v>>16))
		case format == 1 && bits == 32:
			data = binary.LittleEndian.AppendUint32(data, uint32(int32(s)<<16))
		case format == 3 && bits == 32:
			data = binary.LittleEndian.AppendUint32(data, math.Float32bits(float32(s)/32768))
		case format == 3 && bits == 64:
			data = binary.LittleEndian.AppendUint64(data, math.Float64bits(float64(s)/32768))
		default:
			// An unsupported width, written so the reader can refuse it.
			data = append(data, make([]byte, (bits+7)/8)...)
		}
	}
	return riff(fmtChunk(format, pcm, bits), data)
}

// wavExtensible writes the same 16-bit samples behind a WAVE_FORMAT_EXTENSIBLE
// header, which is what anything above two channels or 16 bits uses in
// practice.
func wavExtensible(pcm fingerprint.PCM) []byte {
	body := fmtChunk(0xFFFE, pcm, 16)
	body = binary.LittleEndian.AppendUint16(body, 22) // cbSize
	body = binary.LittleEndian.AppendUint16(body, 16) // valid bits
	body = binary.LittleEndian.AppendUint32(body, 0)  // channel mask
	body = binary.LittleEndian.AppendUint16(body, 1)  // SubFormat: PCM
	body = append(body, make([]byte, 14)...)          // rest of the GUID
	var data []byte
	for _, s := range pcm.Samples {
		data = binary.LittleEndian.AppendUint16(data, uint16(s))
	}
	return riff(body, data)
}

func fmtChunk(format uint16, pcm fingerprint.PCM, bits int) []byte {
	blockAlign := pcm.Channels * ((bits + 7) / 8)
	var b []byte
	b = binary.LittleEndian.AppendUint16(b, format)
	b = binary.LittleEndian.AppendUint16(b, uint16(pcm.Channels))
	b = binary.LittleEndian.AppendUint32(b, uint32(pcm.SampleRate))
	b = binary.LittleEndian.AppendUint32(b, uint32(pcm.SampleRate*blockAlign))
	b = binary.LittleEndian.AppendUint16(b, uint16(blockAlign))
	b = binary.LittleEndian.AppendUint16(b, uint16(bits))
	return b
}

func chunk(id string, body []byte) []byte {
	out := append([]byte(id), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(body)))
	out = append(out, body...)
	if len(body)%2 == 1 {
		out = append(out, 0)
	}
	return out
}

func riff(fmtBody, data []byte) []byte {
	inner := append(chunk("fmt ", fmtBody), chunk("data", data)...)
	out := append([]byte("RIFF"), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(out[4:], uint32(4+len(inner)))
	out = append(out, "WAVE"...)
	return append(out, inner...)
}
