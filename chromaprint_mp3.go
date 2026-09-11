package fingerprint

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/hajimehoshi/go-mp3"
)

// Reading MP3, where the samples are the easy part and the alignment is not.
//
// Two conformant MP3 decoders agree on the audio without agreeing on where
// the stream begins, and Chromaprint cuts frames every 124 ms from sample
// zero, so a shifted start re-cuts every frame in the file. Decoding this
// package's reference recording with go-mp3 and fingerprinting it naively
// disagrees with fpcalc in 222 of 3328 bits — not because the samples are
// wrong, but because they begin 2257 frames early.
//
// FFmpeg drops three things go-mp3 keeps, and this reproduces all three: the
// Xing/Info header frame, which is metadata rather than audio; the encoder
// delay the LAME tag records; and the 529-sample group delay of the synthesis
// filterbank. It trims the encoder's end padding by that same 529. With those
// applied the vector matches fpcalc exactly, even though the two decoders'
// samples differ by an average of 0.6 in 32768 — which is the robustness a
// perceptual fingerprint exists to provide, measured rather than hoped for.

const (
	// mp3MaxSamples caps a decode at roughly three hours of stereo audio, so
	// a malformed or hostile stream cannot make this allocate without bound.
	mp3MaxSamples = 2 << 28

	// mp3FrameSamples is an MPEG-1 Layer III frame. The Xing/Info header
	// occupies exactly one, and it is silence FFmpeg never decodes.
	mp3FrameSamples = 1152

	// mp3DecoderDelay is the synthesis filterbank's group delay, which every
	// decoder incurs and FFmpeg discards: mp3dec.c's 528 + 1.
	mp3DecoderDelay = 529

	// mp3VersionMPEG1 is the header's version field for MPEG-1. The half-rate
	// MPEG-2 and 2.5 extensions are refused; see ChromaprintFromMP3.
	mp3VersionMPEG1 = 3
)

// ChromaprintFromMP3 decodes an MP3 stream and returns its raw Chromaprint
// vector, matching what fpcalc prints for the same file.
//
// It accepts MPEG-1 Layer III — every sample rate and bitrate, mono, stereo,
// joint stereo, constant or variable. That is what music is stored as. The
// half-rate MPEG-2 and MPEG-2.5 extensions are refused, because the decoder
// behind this cannot be trusted with them: it rejects 2.5 outright, and
// mis-decodes some MPEG-2 configurations badly enough to fingerprint as
// unrelated audio, with nothing in the header to tell the good from the bad.
// Refusing is the only honest answer to a file this cannot read correctly.
//
// The file also needs a Xing or Info header, which is where the encoder delay
// lives. Without it there is nothing to align to.
func ChromaprintFromMP3(r io.Reader) ([]int32, error) {
	pcm, err := decodeMP3(r)
	if err != nil {
		return nil, err
	}
	return Chromaprint(pcm)
}

// decodeMP3 reads the whole stream into interleaved 16-bit samples and trims
// it to the span FFmpeg would have produced. go-mp3 always emits 16-bit
// little-endian stereo, whatever the file's own channel mode, so the shape is
// fixed and only the rate varies.
func decodeMP3(r io.Reader) (PCM, error) {
	// The gapless header has to be read from the same bytes the decoder gets,
	// and the whole stream is decoded anyway, so buffer it once.
	encoded, err := io.ReadAll(io.LimitReader(r, mp3MaxSamples))
	if err != nil {
		return PCM{}, mp3Errorf("reading the stream: %s", err)
	}

	gapless, err := mp3Gapless(encoded)
	if err != nil {
		return PCM{}, err
	}

	dec, err := mp3.NewDecoder(bytes.NewReader(encoded))
	if err != nil {
		return PCM{}, mp3Errorf("opening the stream: %s", err)
	}
	raw, err := io.ReadAll(io.LimitReader(dec, mp3MaxSamples*2))
	if err != nil {
		return PCM{}, mp3Errorf("decoding: %s", err)
	}

	// A trailing partial frame would shift one channel against the other.
	raw = raw[:len(raw)-len(raw)%4]
	samples := make([]int16, len(raw)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(raw[2*i:]))
	}

	samples = mp3Trim(samples, gapless)
	if len(samples) == 0 {
		return PCM{}, mp3Errorf("the stream is entirely header and encoder padding")
	}
	return PCM{Samples: samples, SampleRate: dec.SampleRate(), Channels: 2}, nil
}

// mp3GaplessInfo is what the LAME-style tag says about the stream's edges,
// in frames.
type mp3GaplessInfo struct{ delay, padding int }

// mp3Trim drops the lead-in and the tail FFmpeg drops: the Xing header frame
// plus the encoder delay plus the decoder's own group delay at the front, and
// the encoder's padding less that same group delay at the back.
func mp3Trim(samples []int16, g mp3GaplessInfo) []int16 {
	front := (mp3FrameSamples + g.delay + mp3DecoderDelay) * 2
	back := max(g.padding-mp3DecoderDelay, 0) * 2
	if front+back >= len(samples) {
		return nil
	}
	return samples[front : len(samples)-back]
}

// mp3Gapless finds the first frame, refuses the MPEG versions this cannot
// read correctly, and reads the encoder delay and padding out of the Xing or
// Info header that frame carries.
func mp3Gapless(data []byte) (mp3GaplessInfo, error) {
	pos := 0
	if len(data) >= 10 && string(data[0:3]) == "ID3" {
		// A syncsafe 28-bit size, seven bits per byte.
		size := int(data[6]&0x7f)<<21 | int(data[7]&0x7f)<<14 |
			int(data[8]&0x7f)<<7 | int(data[9]&0x7f)
		pos = 10 + size
	}
	if pos+4 > len(data) || data[pos] != 0xff || data[pos+1]&0xe0 != 0xe0 {
		return mp3GaplessInfo{}, mp3Errorf("no MPEG audio frame where one should start")
	}

	if version := (data[pos+1] >> 3) & 3; version != mp3VersionMPEG1 {
		return mp3GaplessInfo{}, mp3Errorf(
			"this is an MPEG-2 or 2.5 stream, and the decoder behind this " +
				"mis-decodes some of those badly enough to fingerprint as unrelated " +
				"audio, with nothing in the header to tell which; decode it yourself " +
				"and call Chromaprint, or convert it to WAV")
	}

	// MPEG-1 side information: 32 bytes with two channels, 17 with one.
	sideInfo := 32
	if mode := (data[pos+3] >> 6) & 3; mode == 3 {
		sideInfo = 17
	}

	tag := pos + 4 + sideInfo
	if tag+8 > len(data) {
		return mp3GaplessInfo{}, mp3Errorf("the first frame is truncated")
	}
	if name := string(data[tag : tag+4]); name != "Xing" && name != "Info" {
		return mp3GaplessInfo{}, mp3Errorf(
			"no Xing or Info header, so the file records no encoder delay and " +
				"cannot be aligned the way fpcalc aligns it")
	}

	// Walk past whichever optional fields the flags claim are present.
	flags := binary.BigEndian.Uint32(data[tag+4 : tag+8])
	p := tag + 8
	for i, size := range [4]int{4, 4, 100, 4} {
		if flags&(1<<uint(i)) != 0 {
			p += size
		}
	}

	// The LAME extension, whose delay and padding share three bytes at 0x15.
	if p+0x18 > len(data) {
		return mp3GaplessInfo{}, mp3Errorf("the Xing header has no LAME extension")
	}
	switch string(data[p : p+4]) {
	case "LAME", "Lavc", "Lavf":
	default:
		return mp3GaplessInfo{}, mp3Errorf(
			"the Xing header carries no recognised encoder tag, so it records no delay")
	}
	b := data[p+0x15 : p+0x18]
	return mp3GaplessInfo{
		delay:   int(b[0])<<4 | int(b[1])>>4,
		padding: int(b[1]&0x0f)<<8 | int(b[2]),
	}, nil
}

func mp3Errorf(format string, args ...any) error {
	return &PHashError{msg: "cannot read this MP3: " + fmt.Sprintf(format, args...)}
}
