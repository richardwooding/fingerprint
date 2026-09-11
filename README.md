# fingerprint

[![Go Reference](https://pkg.go.dev/badge/github.com/richardwooding/fingerprint.svg)](https://pkg.go.dev/github.com/richardwooding/fingerprint)
[![CI](https://github.com/richardwooding/fingerprint/actions/workflows/ci.yml/badge.svg)](https://github.com/richardwooding/fingerprint/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/richardwooding/fingerprint)](https://goreportcard.com/report/github.com/richardwooding/fingerprint)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**Website:** [richardwooding.github.io/fingerprint](https://richardwooding.github.io/fingerprint/)

Small content-fingerprinting primitives for **near-duplicate / similar-content
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
- **Audio — Chromaprint** (`Chromaprint`): the raw acoustic fingerprint an **ISCC**
  Audio-Code is computed from, in pure Go. Also the missing step before one, and
  [nothing else in Go does it without cgo](#audio-fingerprints-chromaprint).

The two hashes return a `uint64`; pairwise **Hamming `Distance`** (and `Similarity = 1 -
distance/64`) measures closeness — small distance means similar content. The two ISCC
inputs are not hashes and not on that metric: they are the bytes and the vector the
standard's own code generators take.

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

## Audio fingerprints (Chromaprint)

An ISCC **Audio-Code** is a SimHash over a [Chromaprint](https://acoustid.org/chromaprint)
fingerprint, and `iscc-lib`'s `GenAudioCodeV0` takes that fingerprint as given. Producing one in Go
meant shelling out to the `fpcalc` binary or binding the C library through cgo — closed to anything
that ships a static binary or compiles to WebAssembly. This does it in process:

```go
f, _ := os.Open("track.wav")
cv, err := fingerprint.ChromaprintFromWAV(f)   // []int32, one word per ~124 ms
code, err := iscc.GenAudioCodeV0(cv, 64)       // github.com/iscc/iscc-lib/packages/go
// ISCC:EIAWUJFCEZZOJYVD
```

`ChromaprintFromWAV` reads uncompressed RIFF/WAVE — 8-bit unsigned, 16-, 24- and 32-bit signed
integer and 32- or 64-bit float samples, including `WAVE_FORMAT_EXTENSIBLE`, and it skips the
`LIST`, `fact` and `bext` chunks real files carry. A compressed WAV is refused by name rather than
mis-read: this reads samples, it does not decode audio.

`ChromaprintFromMP3` reads MPEG-1 Layer III, applying the encoder delay and padding from the
file's Xing/LAME header exactly as FFmpeg does — [without which every frame in the file is
re-cut](#mp3-and-the-2257-frames-that-are-not-there). MPEG-2 and 2.5 are refused; see below.

For anything else, decode it yourself and hand over the samples. There is no audio equivalent of
`image.Decode`'s registry to hide the decoding behind, so the reader has to name its format:

```go
cv, err := fingerprint.Chromaprint(fingerprint.PCM{
    Samples: samples, SampleRate: 44100, Channels: 2,
})
```

Downmixing to mono and resampling to 11025 Hz happen here, by the same route `fpcalc` takes, so
the vector is the one `fpcalc` would print for the same content. That route is not the obvious one
and it is [not negotiable](#resampling-and-why-it-is-ffmpegs-and-not-chromaprints).

The result is a slice, not a `uint64`, so `Distance` and `Similarity` do not apply: two Chromaprint
vectors are compared through the ISCC code computed from them.

Audio under about three seconds is **refused** rather than fingerprinted. It would otherwise return
an empty vector, and `GenAudioCodeV0` turns an empty vector into a perfectly well-formed code — the
same one for every such file. A wrong code is a claim that this content is some other content.

### Why this is a port of the reference rather than an implementation of a spec

Chromaprint has no specification. There is no ISO document, no RFC, not even a complete written
description — the author's own write-up stops at "just the basics to get the general idea". The
algorithm is whatever `acoustid/chromaprint` does, and ISO 24138 inherits that: it names
Chromaprint as the Audio-Code's input and says nothing about how to compute one.

So this is a port, and it is faithful in the places where faithfulness is visible in the output:
the Hamming window scaled by `1/INT16_MAX` and **narrowed to `float32`**, because the reference
stores it in a `float` array and that rounding reaches the result; the sixteen trained classifiers
of algorithm TEST2 in their table order, because the packing shifts each one's two bits in as it
goes; the integral-image rectangle splits with their integer division, because an odd rectangle's
halves are uneven there too; and the Gray coding, so a value drifting across a quantiser threshold
costs one bit of distance rather than two.

**It matches `fpcalc` exactly** — all 104 subfingerprints, all 3328 bits, for the test recording.

The one place it deliberately does not follow the reference is the transform itself. Chromaprint
computes its FFT in whichever library it was built against, and the FFmpeg backends work in
`float32` throughout, so there is no single reference spectrum to reproduce — "bit-exact against
`fpcalc`" would mean bit-exact against one build. This computes the transform in `float64`, which
is as close to the true value as the windowed input allows, and therefore the closest single answer
to every build at once. The measurement above says it is close enough that the question never
arises.

### Resampling, and why it is FFmpeg's and not Chromaprint's

Chromaprint fingerprints at 11025 Hz and almost nothing is stored at that rate, so the resampler
decides the answer for nearly every real file. There are two candidates and they are not
interchangeable.

`fpcalc` never uses Chromaprint's own resampler. It hands the audio to FFmpeg's `swresample`
first, and the fingerprinter only ever sees 11025 Hz mono. Chromaprint's internal `av_resample` is
reached only by a caller feeding the library raw PCM at some other rate. Porting that one — the
obvious choice, since it ships inside Chromaprint — produces a code that looks right and is not:
on the test recording it moves **62 of 3328 fingerprint bits**, and the resulting ISCC differs from
the published one in 2 of its 64 bits.

| resampler | code for the test recording |
| --- | --- |
| FFmpeg `swresample`, what `fpcalc` and `iscc-sdk` use | `ISCC:EIAWUJFCEZZOJYVD` |
| Chromaprint's internal `av_resample` | `ISCC:EIAXUJFCEZZOJYVC` |

So this reproduces `swresample`, configured as Chromaprint configures it: engine SWR, filter size
16, phase shift 8, linear interpolation on, cutoff 0.8, and — because the formats are 16-bit in and
16-bit out at differing rates — a float32 internal format, which fixes the filter bank's type and
the rounding of the whole chain. The lead-in and the tail are mirrored rather than zero-padded,
which is `swresample`'s choice and is visible in the first and last subfingerprints.

**`swresample` is not one number either**, and the same honesty applies as with the FFT: its
hand-written SIMD kernels and its portable C give float outputs that differ by about one unit in
the last place. This matches the portable C exactly. The difference never survives the step back to
16 bits — the two produce byte-identical samples and an identical fingerprint — so matching the
scalar path matches every build.

### MP3, and the 2257 frames that are not there

Two conformant MP3 decoders agree on the audio without agreeing on where the stream begins, and
Chromaprint cuts a frame every 124 ms from sample zero, so a shifted start re-cuts every frame in
the file. Decoding the test recording with `go-mp3` and fingerprinting it naively disagrees with
`fpcalc` in **222 of 3328 bits** — not because the samples are wrong, but because they begin 2257
frames early.

FFmpeg drops three things `go-mp3` keeps, and this reproduces all three:

| dropped | frames | why |
| --- | --- | --- |
| the Xing/Info header frame | 1152 | it is metadata written into a silent frame, not audio |
| the encoder delay | 576 here | the LAME tag records what the encoder prepended |
| the synthesis filterbank's group delay | 529 | every decoder incurs it; FFmpeg discards it |

and it trims the encoder's end padding by that same 529. With those applied the vector matches
`fpcalc` exactly — even though the two decoders' samples still differ by an average of 0.6 in
32768. That is the robustness a perceptual fingerprint exists to provide, measured rather than
hoped for.

**MPEG-2 and MPEG-2.5 are refused.** The decoder behind this rejects 2.5 outright, and mis-decodes
some MPEG-2 configurations badly enough to fingerprint as unrelated audio — 96 kbps at 22050 Hz
comes out wrong, 64 kbps at the same rate comes out exact — with nothing in the header separating
them. Every MPEG-1 configuration tested is exact: three sample rates, four bitrates, mono and
stereo, constant and variable, 26 of 27 bit-identical and the twenty-seventh differing in one bit
of 3328, which does not change the code. Refusing a file this cannot read correctly is the only
honest answer; convert it to WAV, or decode it yourself and call `Chromaprint`.

### What carries an exactness claim

**Every sample rate above 1000 Hz, mono or stereo.** The conformance test checks eight rates
against `fpcalc`, and all of them are exact — all 104 subfingerprints, every bit:

| rate | why it is in the matrix |
| --- | --- |
| 11025 | passthrough: `fpcalc` builds no converter at all |
| 22050, 44100 | integer ratios: one phase, no fractional carry |
| 48000, 96000 | reduce to 147 phases, still no carry |
| 8000, 16000, 32000 | do not reduce below the 256-phase bank, so the interpolating path runs |

The two convolution paths are both covered, which matters: a resampler can be right about one and
wrong about the other.

**More than two channels is refused above 11025 Hz.** FFmpeg's downmix for a 5.1 layout uses
layout-aware coefficients this does not reproduce, and a plausible guess would be silently wrong.
At 11025 Hz no resampling happens, the fingerprinter's own integer downmix applies, and any channel
count is fine.

Encoding still moves a little, and the code still does not: the 24-bit 44.1 kHz master, the MP3 and
the 11025 Hz mono derivative differ by **2 values of 104, 2 bits of 3328**, and all three produce
`ISCC:EIAWUJFCEZZOJYVD`. See [`testdata/README.md`](testdata/README.md) for the fixtures and the
commands.

## Requirements

- **Go 1.25+** — two dependencies, one per medium. The SimHash half is pure stdlib, and so is
  all of the Chromaprint arithmetic: the FFT, the resampler, the chroma folding and the
  classifiers are hand-written, and only decoding MP3 pulls in
  [`go-mp3`](https://github.com/hajimehoshi/go-mp3). Everything image-shaped uses
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

EXIF orientation is applied wherever the format in hand keeps it — a JPEG's Exif APP1 segment, a
TIFF's own IFD0, a WebP's `EXIF` chunk, a PNG's `eXIf` chunk — because ISO 24138 transposes first,
so a missed orientation yields the code of a rotation of the image rather than of the image.

Known gaps: a 16-bit BMP (Pillow reads it, `x/image/bmp` does not), JPEG-in-TIFF, BigTIFF, CMYK and
YCbCr TIFF — all clean decode errors. Pillow will also take a PNG's EXIF from an ImageMagick-style
`Raw profile type exif` text chunk, which this does not read. HEIC and AVIF need a decoder that does
not exist in pure Go.

## License

MIT — see [LICENSE](LICENSE).

---

Extracted from [file-search-on](https://github.com/richardwooding/file-search-on), where it
powers `find_near_duplicates` and `image_similar_to`.
