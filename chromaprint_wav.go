package fingerprint

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

// Reading PCM WAV, so a caller with a file rather than a sample slice has
// somewhere to start.
//
// This is a deliberately small reader: RIFF/WAVE containing uncompressed
// PCM, in the sample formats a WAV actually turns up in. It is not a general
// audio decoder, and formats that need one — anything compressed, and the
// container's stranger corners — are refused by name rather than
// mis-decoded, because a fingerprint of the wrong samples is a claim about
// the wrong content.

const (
	wavFormatPCM        = 0x0001
	wavFormatFloat      = 0x0003
	wavFormatExtensible = 0xFFFE

	// wavMaxBytes caps what a single call will read. Chromaprint's own
	// fingerprint stops mattering long before this, and an io.Reader has no
	// length to check in advance.
	wavMaxBytes = 1 << 31
)

// ChromaprintFromWAV reads a PCM WAV stream and returns its raw Chromaprint
// vector — the same result as decoding the file yourself and calling
// Chromaprint, for the formats it supports.
//
// It accepts 8-bit unsigned, 16-, 24- and 32-bit signed integer and 32- or
// 64-bit float samples, at any sample rate above 1000 Hz, including
// WAVE_FORMAT_EXTENSIBLE. Compressed WAVs are refused: this reads samples,
// it does not decode audio.
//
// Samples are narrowed to 16 bits the way FFmpeg narrows them, because
// that is what the fingerprinter consumes.
func ChromaprintFromWAV(r io.Reader) ([]int32, error) {
	pcm, err := decodeWAV(r)
	if err != nil {
		return nil, err
	}
	return Chromaprint(pcm)
}

// wavFormat is the parsed `fmt ` chunk.
type wavFormat struct {
	format     uint16
	channels   int
	sampleRate int
	bits       int
}

// decodeWAV walks the RIFF chunks and converts the data chunk to 16-bit
// samples. Chunks it does not recognise are skipped, as a reader must:
// real files carry LIST, fact, bext and more between fmt and data.
func decodeWAV(r io.Reader) (PCM, error) {
	var header [12]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return PCM{}, wavErrorf("reading the RIFF header: %s", wavReason(err))
	}
	if string(header[0:4]) != "RIFF" || string(header[8:12]) != "WAVE" {
		return PCM{}, wavErrorf("not a RIFF/WAVE stream")
	}

	var format wavFormat
	haveFormat := false
	for {
		var head [8]byte
		if _, err := io.ReadFull(r, head[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			return PCM{}, wavErrorf("reading a chunk header: %s", wavReason(err))
		}
		id := string(head[0:4])
		size := int64(binary.LittleEndian.Uint32(head[4:8]))

		switch id {
		case "fmt ":
			body, err := wavChunk(r, size)
			if err != nil {
				return PCM{}, err
			}
			format, err = parseWAVFormat(body)
			if err != nil {
				return PCM{}, err
			}
			haveFormat = true

		case "data":
			if !haveFormat {
				return PCM{}, wavErrorf("the data chunk arrives before the fmt chunk")
			}
			body, err := wavChunk(r, size)
			if err != nil {
				return PCM{}, err
			}
			return wavSamples(format, body)

		default:
			if err := wavSkip(r, size); err != nil {
				return PCM{}, err
			}
		}
		if size%2 == 1 {
			if err := wavSkip(r, 1); err != nil {
				return PCM{}, err
			}
		}
	}
	return PCM{}, wavErrorf("no data chunk")
}

// parseWAVFormat reads the fields this package needs, resolving
// WAVE_FORMAT_EXTENSIBLE to the format its GUID names.
func parseWAVFormat(body []byte) (wavFormat, error) {
	if len(body) < 16 {
		return wavFormat{}, wavErrorf("the fmt chunk is %d bytes, too short to describe a format", len(body))
	}
	f := wavFormat{
		format:     binary.LittleEndian.Uint16(body[0:2]),
		channels:   int(binary.LittleEndian.Uint16(body[2:4])),
		sampleRate: int(binary.LittleEndian.Uint32(body[4:8])),
		bits:       int(binary.LittleEndian.Uint16(body[14:16])),
	}
	if f.format == wavFormatExtensible {
		// The real format is the first two bytes of the SubFormat GUID, at
		// offset 24 of the extended fmt chunk.
		if len(body) < 26 {
			return wavFormat{}, wavErrorf("an extensible fmt chunk without a SubFormat GUID")
		}
		f.format = binary.LittleEndian.Uint16(body[24:26])
	}
	if f.format != wavFormatPCM && f.format != wavFormatFloat {
		return wavFormat{}, wavErrorf(
			"WAV format 0x%04x is compressed; this reads uncompressed PCM, it does not decode audio",
			f.format)
	}
	return f, nil
}

// wavSamples converts a data chunk to the 16-bit samples the fingerprinter
// consumes, narrowing the way FFmpeg narrows each format: an arithmetic
// shift for the integer widths, and scale-and-round for the floats.
func wavSamples(f wavFormat, data []byte) (PCM, error) {
	if f.channels <= 0 {
		return PCM{}, wavErrorf("the fmt chunk declares %d channels", f.channels)
	}

	var samples []int16
	switch {
	case f.format == wavFormatPCM && f.bits == 8:
		samples = make([]int16, len(data))
		for i, b := range data {
			samples[i] = int16(int(b)-128) << 8
		}
	case f.format == wavFormatPCM && f.bits == 16:
		samples = make([]int16, len(data)/2)
		for i := range samples {
			samples[i] = int16(binary.LittleEndian.Uint16(data[2*i:]))
		}
	case f.format == wavFormatPCM && f.bits == 24:
		samples = make([]int16, len(data)/3)
		for i := range samples {
			// Little-endian 24-bit: the top two bytes are the 16 we keep.
			samples[i] = int16(uint16(data[3*i+1]) | uint16(data[3*i+2])<<8)
		}
	case f.format == wavFormatPCM && f.bits == 32:
		samples = make([]int16, len(data)/4)
		for i := range samples {
			samples[i] = int16(binary.LittleEndian.Uint32(data[4*i:]) >> 16)
		}
	case f.format == wavFormatFloat && f.bits == 32:
		samples = make([]int16, len(data)/4)
		for i := range samples {
			v := math.Float32frombits(binary.LittleEndian.Uint32(data[4*i:]))
			samples[i] = floatToS16(float64(v * 32768))
		}
	case f.format == wavFormatFloat && f.bits == 64:
		samples = make([]int16, len(data)/8)
		for i := range samples {
			v := math.Float64frombits(binary.LittleEndian.Uint64(data[8*i:]))
			samples[i] = floatToS16(v * 32768)
		}
	default:
		kind := "integer"
		if f.format == wavFormatFloat {
			kind = "float"
		}
		return PCM{}, wavErrorf("%d-bit %s samples are not a WAV format this reads", f.bits, kind)
	}

	// A truncated final frame would silently rotate the channels.
	samples = samples[:len(samples)-len(samples)%f.channels]

	return PCM{Samples: samples, SampleRate: f.sampleRate, Channels: f.channels}, nil
}

// floatToS16 scales and rounds a float sample, by swresample's rule: round
// half to even, then clamp.
func floatToS16(scaled float64) int16 {
	return int16(min(max(math.RoundToEven(scaled), -32768), 32767))
}

// wavChunk reads one chunk body, refusing a declared size the stream cannot
// possibly hold rather than trying to allocate it.
func wavChunk(r io.Reader, size int64) ([]byte, error) {
	if size < 0 || size > wavMaxBytes {
		return nil, wavErrorf("a chunk declares %d bytes, which is not a size this reads", size)
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, wavErrorf("reading a %d-byte chunk: %s", size, wavReason(err))
	}
	return body, nil
}

func wavSkip(r io.Reader, n int64) error {
	if n < 0 {
		return wavErrorf("a chunk declares a negative size")
	}
	if _, err := io.CopyN(io.Discard, r, n); err != nil && !errors.Is(err, io.EOF) {
		return wavErrorf("skipping %d bytes: %s", n, wavReason(err))
	}
	return nil
}

// wavErrorf builds the package's error type with a WAV-specific prefix.
func wavErrorf(format string, args ...any) error {
	return &PHashError{msg: "cannot read this WAV: " + fmt.Sprintf(format, args...)}
}

// wavReason renders an I/O failure without leaking a wrapped error type into
// a package whose only error type is PHashError.
func wavReason(err error) string {
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return "the stream ends early"
	}
	return err.Error()
}
