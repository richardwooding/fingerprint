package fingerprint

import (
	"bytes"
	"encoding/binary"
	"image"
	"io"
)

// EXIF orientation, read by hand.
//
// ISO 24138 applies the orientation transpose before anything else, so a
// portrait photograph a phone stored as landscape-plus-a-flag must normalise
// the way it is meant to be seen — otherwise its code describes a rotation of
// itself. Reading one tag does not justify a dependency in a package that has
// one, so the twelve bytes that matter are parsed here: APP1, the TIFF header's
// byte order, IFD0, tag 0x0112.
//
// Everything here is best-effort. A truncated, absent, or nonsensical
// orientation means no transpose, never an error: the image is still perfectly
// normalisable, just possibly rotated.

// bytesReader is image.Decode's input, kept as a function so the reader and the
// EXIF scan can share one buffer.
func bytesReader(data []byte) io.Reader { return bytes.NewReader(data) }

// jpegOrientation returns the EXIF orientation of a JPEG, 1 through 8, or 1
// when there is none to read. Only JPEG carries one in a form this matters for;
// PNG and GIF return 1 by falling through the marker scan.
func jpegOrientation(data []byte) int {
	const none = 1
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return none
	}
	// Walk the marker segments looking for APP1/Exif. Scanning stops at SOS:
	// EXIF never follows the entropy-coded data, and the scan's bytes are not
	// marker-structured.
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xFF {
			return none
		}
		marker := data[i+1]
		if marker == 0xD8 || (marker >= 0xD0 && marker <= 0xD9) {
			i += 2
			continue
		}
		if marker == 0xDA { // start of scan
			return none
		}
		length := int(binary.BigEndian.Uint16(data[i+2:]))
		if length < 2 || i+2+length > len(data) {
			return none
		}
		payload := data[i+4 : i+2+length]
		if marker == 0xE1 && len(payload) > 6 && string(payload[:6]) == "Exif\x00\x00" {
			if o := tiffOrientation(payload[6:]); o != 0 {
				return o
			}
			return none
		}
		i += 2 + length
	}
	return none
}

// tiffOrientation reads tag 0x0112 out of a TIFF header's first IFD. Returns 0
// when it is absent or the structure does not hold together.
func tiffOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 0
	}
	var bo binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0
	}
	if bo.Uint16(tiff[2:]) != 42 {
		return 0
	}
	off := int(bo.Uint32(tiff[4:]))
	if off < 8 || off+2 > len(tiff) {
		return 0
	}
	count := int(bo.Uint16(tiff[off:]))
	// Each entry is 12 bytes: tag, type, count, value.
	for i := 0; i < count; i++ {
		e := off + 2 + i*12
		if e+12 > len(tiff) {
			return 0
		}
		if bo.Uint16(tiff[e:]) != 0x0112 {
			continue
		}
		// A SHORT's value sits in the first two bytes of the value field.
		if bo.Uint16(tiff[e+2:]) != 3 {
			return 0
		}
		v := int(bo.Uint16(tiff[e+8:]))
		if v < 1 || v > 8 {
			return 0
		}
		return v
	}
	return 0
}

// isccTranspose applies an EXIF orientation, returning img unchanged for the
// identity orientation (1) or anything unrecognised.
func isccTranspose(img image.Image, orientation int) image.Image {
	if orientation <= 1 || orientation > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	// Orientations 5 to 8 involve a transpose, so the result's axes swap.
	outW, outH := w, h
	if orientation >= 5 {
		outW, outH = h, w
	}
	out := image.NewRGBA(image.Rect(0, 0, outW, outH))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var nx, ny int
			switch orientation {
			case 2: // mirror horizontal
				nx, ny = w-1-x, y
			case 3: // rotate 180
				nx, ny = w-1-x, h-1-y
			case 4: // mirror vertical
				nx, ny = x, h-1-y
			case 5: // mirror horizontal, rotate 270 CW
				nx, ny = y, x
			case 6: // rotate 90 CW
				nx, ny = h-1-y, x
			case 7: // mirror horizontal, rotate 90 CW
				nx, ny = h-1-y, w-1-x
			case 8: // rotate 270 CW
				nx, ny = y, w-1-x
			}
			out.Set(nx, ny, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return out
}
