package fingerprint

import (
	"bytes"
	"encoding/binary"
)

// Format-specific reads that the decoders do not do for us.
//
// x/image's TIFF and WebP decoders hand back pixels and nothing else. Neither
// looks at EXIF orientation, and the TIFF one silently ignores two tags that
// change what its output MEANS. ISO 24138 needs both questions answered before
// the pixels are normalised, so they are answered here, in the same
// twelve-bytes-at-a-time style as the JPEG EXIF read.

// TIFF field types, and the tags this package reads. Everything else in an IFD
// is somebody else's business.
const (
	tiffTypeByte  = 1
	tiffTypeShort = 3
	tiffTypeLong  = 4

	tagNewSubfileType = 0x00FE // 254
	tagOrientation    = 0x0112 // 274
	tagPlanarConfig   = 0x011C // 284
	tagDNGVersion     = 0xC612 // 50706

	// subfileReducedResolution is bit 0 of NewSubfileType: "this is a
	// reduced-resolution version of another image in this file".
	subfileReducedResolution = 1
	// planarChunky is PlanarConfiguration 1, samples interleaved per pixel.
	planarChunky = 1
)

// tiffEntry is one IFD entry, decoded as far as the handful of tags above need.
type tiffEntry struct {
	tag, typ uint16
	count    uint32
	// short and long are the entry's value read as a SHORT or a LONG. A value
	// of either type fits in the entry's four value bytes, so no offset is
	// followed; tags whose values do not fit are read for presence only.
	short uint16
	long  uint32
}

// tiffIFD0 walks the first IFD of a TIFF header, calling fn for each entry
// until fn returns false. tiff[0] must be the byte-order mark, and every offset
// is relative to it — the TIFF convention, which is why this works equally on a
// .tif file's own bytes and on the payload of a JPEG's Exif APP1 segment.
//
// It reports whether the structure held together, so a caller can tell "the tag
// is absent" from "the tags could not be read at all". Best-effort throughout:
// a truncated directory stops the walk rather than failing.
func tiffIFD0(tiff []byte, fn func(tiffEntry) bool) bool {
	if len(tiff) < 8 {
		return false
	}
	var bo binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return false
	}
	if bo.Uint16(tiff[2:]) != 42 {
		return false
	}
	off := int(bo.Uint32(tiff[4:]))
	if off < 8 || off+2 > len(tiff) {
		return false
	}
	count := int(bo.Uint16(tiff[off:]))
	// Each entry is 12 bytes: tag, type, count, then four value bytes.
	for i := range count {
		e := off + 2 + i*12
		if e+12 > len(tiff) {
			return false
		}
		entry := tiffEntry{
			tag:   bo.Uint16(tiff[e:]),
			typ:   bo.Uint16(tiff[e+2:]),
			count: bo.Uint32(tiff[e+4:]),
			// A SHORT's value sits in the first two bytes of the value field,
			// whichever byte order is in force.
			short: bo.Uint16(tiff[e+8:]),
			long:  bo.Uint32(tiff[e+8:]),
		}
		if !fn(entry) {
			return true
		}
	}
	return true
}

// tiffRefusal reports why an ISCC must not be computed from this TIFF, or "" —
// including for anything that is not a TIFF at all, since the walk above
// declines a header it does not recognise.
//
// These are the cases where x/image/tiff returns pixels that do not mean what
// they appear to mean. A wrong ISCC is worse than no ISCC: it is a claim that
// this content is some other content, and nothing downstream can tell.
func tiffRefusal(tiff []byte) string {
	var reason string
	tiffIFD0(tiff, func(e tiffEntry) bool {
		switch e.tag {
		case tagDNGVersion:
			// A DNG's first directory is conventionally a small preview, with
			// the sensor data in a sub-directory this decoder cannot reach. A
			// preview's code is the preview's, not the photograph's.
			reason = "this is a DNG, whose first image directory is a preview rather than the photograph"
		case tagNewSubfileType:
			if e.typ == tiffTypeLong && e.long&subfileReducedResolution != 0 {
				reason = "this TIFF's first image directory is a reduced-resolution preview, not the full image"
			}
		case tagPlanarConfig:
			if e.typ == tiffTypeShort && e.short != planarChunky {
				// x/image/tiff does not read this tag and decodes every TIFF as
				// if the samples were interleaved, so a planar file comes back
				// as colour noise with no error at all.
				reason = "this TIFF stores its colour planes separately, which this build's decoder misreads"
			}
		}
		return reason == ""
	})
	return reason
}

// tiffOrientation reads tag 0x0112 out of a TIFF header's first IFD. Returns 0
// when it is absent or the structure does not hold together.
func tiffOrientation(tiff []byte) int {
	orientation := 0
	tiffIFD0(tiff, func(e tiffEntry) bool {
		if e.tag != tagOrientation {
			return true
		}
		if e.typ == tiffTypeShort && e.short >= 1 && e.short <= 8 {
			orientation = int(e.short)
		}
		return false // the tag appears at most once; stop either way
	})
	return orientation
}

// webpOrientation reads the orientation out of a WebP's EXIF chunk, or 0.
//
// x/image/webp notices only that such a chunk exists — a flag bit in VP8X —
// and never surfaces it, so the RIFF walk is ours. The chunk's payload is a
// bare TIFF header, which is exactly what tiffOrientation eats.
func webpOrientation(data []byte) int {
	if len(data) < 16 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return 0
	}
	// Each chunk is a FourCC, a little-endian u32 payload size, the payload,
	// then a pad byte when that size is odd.
	for i := 12; i+8 <= len(data); {
		size := int(binary.LittleEndian.Uint32(data[i+4:]))
		if size < 0 || i+8+size > len(data) {
			return 0
		}
		if string(data[i:i+4]) == "EXIF" {
			payload := data[i+8 : i+8+size]
			// The spec says a bare TIFF header, but encoders that lift the
			// segment straight out of a JPEG leave its marker on the front.
			return tiffOrientation(bytes.TrimPrefix(payload, []byte("Exif\x00\x00")))
		}
		i += 8 + size
		if size%2 == 1 {
			i++
		}
	}
	return 0
}
