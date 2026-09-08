# fingerprint

[![Go Reference](https://pkg.go.dev/badge/github.com/richardwooding/fingerprint.svg)](https://pkg.go.dev/github.com/richardwooding/fingerprint)
[![CI](https://github.com/richardwooding/fingerprint/actions/workflows/ci.yml/badge.svg)](https://github.com/richardwooding/fingerprint/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/richardwooding/fingerprint)](https://goreportcard.com/report/github.com/richardwooding/fingerprint)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**Website:** [richardwooding.github.io/fingerprint](https://richardwooding.github.io/fingerprint/)

Two small content-fingerprinting primitives for **near-duplicate / similar-content
detection** in Go:

- **Text — Charikar SimHash** (`Compute` / `Distance` / `Similarity`): a 64-bit
  locality-sensitive hash for finding near-duplicate documents. **Shingled** (3-word) so
  it doesn't collapse unrelated prose the way single-token SimHash does. Pure stdlib.
- **Images — perceptual hash / pHash** (`PHash` / `PHashFromImage`, with `PHashHex` /
  `PHashFromHex`): a 64-bit DCT-based hash for finding visually-similar images regardless
  of scale or minor edits.
- **Images — ISO 24138 normalisation** (`ISCCPixels` / `ISCCPixelsFromReader`): the 1024
  grayscale bytes an **ISCC** Image-Code is computed from. Not a hash — the missing step
  before one.

Both return a `uint64`; pairwise **Hamming `Distance`** (and `Similarity = 1 -
distance/64`) measures closeness — small distance means similar content.

```sh
go get github.com/richardwooding/fingerprint
```

## Text near-duplicates (SimHash)

```go
a := fingerprint.Compute(docA) // 64-bit fingerprint of the body text
b := fingerprint.Compute(docB)

if fingerprint.Similarity(a, b) >= 0.85 {
    // near-duplicate: typo fixes, template fills, regenerated headers, minor revisions
}
```

Rough distance → similarity guide:

| Hamming distance | Similarity | Meaning |
|---|---|---|
| ≤ 3 | ≈ 95% | near-identical (whitespace / a word or two) |
| ≤ 9 | ≈ 85% | minor edits / template fills (a common cut) |
| ~28+ | ≈ 55% | unrelated prose (near the random baseline) |

**Why shingled:** single-token SimHash over natural-language text is dominated by the
near-universal stopword distribution, so unrelated documents score ~90% similar and cluster
spuriously. Hashing overlapping 3-word shingles keys on phrasing instead — unrelated prose
drops to ~0.55 while genuine near-duplicates stay high.

## Image similarity (pHash)

```go
h1, _ := fingerprint.PHash(file1) // io.Reader of a PNG/JPEG/GIF/BMP/TIFF/WebP
h2, _ := fingerprint.PHash(file2)

if fingerprint.Distance(h1, h2) <= 10 {
    // visually similar: resizes, re-encodes, light crops/edits
}

hex := fingerprint.PHashHex(h1)        // 16-char hex for storage
back, _ := fingerprint.PHashFromHex(hex)
```

`PHash` downscales to 32×32 greyscale, runs a 2-D DCT, and keeps the sign of the low-frequency
coefficients — the classic perceptual-hash recipe, robust to scaling and re-compression.

## ISCC normalisation (ISO 24138)

An **ISCC** — the International Standard Content Code, ISO 24138:2024 — is the one open, registered
algorithm on the [C2PA soft binding algorithm
list](https://github.com/c2pa-org/softbinding-algorithm-list), which makes it the way to give a C2PA
manifest a fingerprint that survives re-encoding. The codes themselves are computed by
[`iscc-lib`](https://github.com/iscc/iscc-lib), the official pure-Go implementation of the standard
— but it takes 1024 already-normalised pixels, and its own documentation says to "pre-process your
image to 32x32 grayscale **externally**". Nothing in Go did that. This does:

```go
f, _ := os.Open("photo.jpg")
pixels, err := fingerprint.ISCCPixelsFromReader(f)  // EXIF transpose, flatten, trim, gray, 32x32
code, err := iscc.GenImageCodeV0(pixels, 64)        // github.com/iscc/iscc-lib/packages/go
// ISCC:EEA4GQZQTY6J5DTH
```

The steps are the standard's, in its order: apply the EXIF orientation, composite transparency onto
white, trim a uniform border, convert to grayscale, resample to 32x32.

### Why this re-implements Pillow's arithmetic rather than approximating it

ISO 24138's conformance vectors begin *after* normalisation — they take the 1024 pixels as their
input — so nothing official pins how an image becomes those pixels, and getting it wrong yields a
code that is silently non-conformant. So the greyscale is Rec. 601 in the same 16-bit fixed point
the reference uses, and the resample is its two-pass fixed-point bicubic, intermediate byte rounding
included. A float implementation lands one off on some pixels, and one pixel can be one bit of a
code.

It is checked against the exact pixel values the reference implementation publishes for
`testdata/iscc_demo.png`: **all 28 match byte for byte.** The JPEG of the same photograph differs by
1 on a handful, because Go's `image/jpeg` and libjpeg round YCbCr to RGB differently — a decoder
difference, not a normalisation one, which the PNG's exactness proves. It does not change the
outcome: both encodings still produce `ISCC:EEA4GQZQTY6J5DTH`, the code the reference publishes for
that photograph, which is exactly the robustness a perceptual identifier exists to provide. See
[`testdata/README.md`](testdata/README.md) for the oracle's provenance.

This package computes no ISCC and takes no dependency on one: it produces the bytes and stops.

## Requirements

- **Go 1.25+** — the SimHash half is pure stdlib. Everything image-shaped uses
  [`golang.org/x/image`](https://pkg.go.dev/golang.org/x/image), which sets the floor: the pHash
  half for high-quality downscaling, and both image halves for the BMP, TIFF and WebP decoders.
  That is still one dependency, and the arithmetic remains ours — the ISCC normaliser's resample
  and its EXIF orientation read are hand-written stdlib, because one tag does not justify a second
  dependency and the resample has to match Pillow's rounding rather than x/image's.

### Formats

`PHash` and `ISCCPixelsFromReader` accept whatever is registered with `image.Decode`; this package
registers **GIF, JPEG, PNG** (stdlib) and **BMP, TIFF, WebP** (`x/image`), and a program that
registers another decoder gets that format too.

A few inputs are refused rather than fingerprinted, because their pixels would not mean what they
appear to mean — a wrong ISCC is a claim that this content is some *other* content:

| input | why |
| --- | --- |
| a planar-configuration TIFF | `x/image/tiff` does not read that tag and decodes it as if interleaved, returning colour noise with no error |
| a TIFF whose first directory is a reduced-resolution preview | the preview's code is not the image's |
| a DNG | its first directory is a preview, with the sensor data in a sub-directory `x/image` cannot reach |
| an animated WebP | no single image to identify; the decoder refuses it outright rather than picking a frame |

Known gaps: a 16-bit BMP (Pillow reads it, `x/image/bmp` does not), JPEG-in-TIFF, BigTIFF, CMYK and
YCbCr TIFF — all clean decode errors. A PNG `eXIf` orientation is not yet applied, which Pillow
does apply; see the note in `CLAUDE.md`. HEIC and AVIF need a decoder that does not exist in pure Go.

## License

MIT — see [LICENSE](LICENSE).

---

Extracted from [file-search-on](https://github.com/richardwooding/file-search-on), where it
powers `find_near_duplicates` and `image_similar_to`.
