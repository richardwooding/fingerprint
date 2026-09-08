# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`github.com/richardwooding/fingerprint` is a tiny, flat (single-package, no subpackages) Go library exposing two independent content-fingerprinting primitives for near-duplicate detection. Both emit a `uint64` on the same metric, so the `Distance` / `Similarity` helpers work for either:

- **`simhash.go`** — text: Charikar SimHash (`Compute`). Pure stdlib.
- **`phash.go`** — images: DCT perceptual hash (`PHash` / `PHashFromImage`, `PHashHex` / `PHashFromHex`). Uses `golang.org/x/image/draw` for downscaling.

`Distance` (Hamming) and `Similarity` (`1 - distance/64`) live in `simhash.go` but are shared by both paths — small distance means similar content.

## Commands

```sh
go test ./...                              # all tests
go test -run TestSimilarity_Unrelated      # single test by name
go test -race -timeout 120s ./...          # what CI runs
go vet ./...
go build ./...
golangci-lint run                          # CI lints with golangci-lint (version: latest)
```

CI (`.github/workflows/ci.yml`) runs build + vet + race tests on Go `1.25` and `stable`, plus golangci-lint.

## Things to know before editing

- **Go 1.25 is the floor**, set by `golang.org/x/image`, not by language features. The code uses Go 1.22+ `range int` (`for i := range 64`) throughout — keep that idiom. Don't raise the floor without updating both `go.mod` and the CI matrix's lower bound.
- **The numeric thresholds are the contract.** Similarity/distance tables in the package docs, README, and `simhash_test.go` (`~0.55` unrelated baseline, `0.85`/distance-9 near-duplicate cut) and `phash_test.go` are calibrated against real-world behavior. Changing the algorithm (`shingleSize`, the DCT/median recipe, tokenization) shifts these numbers — re-validate against the tests and update the docs in lockstep.
- **SimHash is deliberately shingled** (3-word shingles, `shingleSize`). This is the central design decision: single-token SimHash collapses unrelated prose to ~90% similar because of stopword dominance. Inputs shorter than `shingleSize` tokens fall back to single-token features so short strings still fingerprint. Don't "simplify" back to single tokens.
- **`Compute("")` and decode failures return `0`** — a legitimate fingerprint value, not a sentinel. Callers distinguish "no content" via a separate length/error check.
- **pHash zeroes the DC coefficient** (bit 0, `dct[0][0]`): it carries brightness, not structure. It's excluded from the median and never set in the hash, so both sides of a comparison see the same bit 0. The 8×8 low-frequency sub-block is extracted from a 32×32 DCT (`phashGridSize` / `phashLowFreqSize`); the DCT loops stop early rather than computing the full grid.
- **Image decoders are registered via blank imports** in `phash.go` — `image/gif`, `image/jpeg`,
  `image/png` from the stdlib and `golang.org/x/image/{bmp,tiff,webp}`. Add a format → add its blank
  import. Two consequences to keep in mind: the registry is **process-global**, so this widens
  `PHash` for every consumer (in `file-search-on` those files previously failed to hash and silently
  produced nothing, so the change is additive — but `image_similar_to()` starts matching where it
  matched nothing); and adding a format is not finished until you know whether its decoder AGREES
  with Pillow's, which is what `iscc_formats_test.go` is for.
- Several doc comments reference the upstream project (`index.Entry.PHash`, CEL `image_similar_to`, "Issue #208"). This library was extracted from [file-search-on](https://github.com/richardwooding/file-search-on); those references are historical context, not code in this repo.

## Tests

`testdata/sample.jpg` is the real-image fixture (`loadFixture`); `phash_test.go` also synthesizes images in-memory (`renderTestImage`). pHash tests assert robustness invariants (survives resize / JPEG re-encode, distinguishes different scenes). `example_test.go` holds runnable `Example_*` godoc examples — keep them passing as they double as documentation.

## ISCC normalisation (`iscc.go`, `iscc_exif.go`, `iscc_formats.go`)

`ISCCPixels` produces the 1024 grayscale bytes an ISO 24138 Image-Code is computed from. It is
**deliberately a re-implementation of Pillow's arithmetic**, because ISO 24138's conformance
vectors start *after* normalisation and nothing official pins how an image becomes those pixels —
so "close" is a silently non-conformant code:

- greyscale is Rec. 601 in Pillow's 16-bit fixed point (`19595/38470/7471`, `+32768` rounding),
  not the float form, which lands one off on some pixels;
- the resample is Pillow's two-pass fixed-point bicubic — `PRECISION_BITS = 22`, coefficients
  normalised in float and *then* rounded, and the intermediate **rounded back to bytes between the
  passes**. Accumulating both passes in float gives different bytes. The kernel itself is not where
  implementations differ: `x/image`'s `CatmullRom` is the same Mitchell–Netravali B=0, C=0.5 spline
  as Pillow's BICUBIC.

**Do not "simplify" any of that, and do not reuse `phash.go`'s DCT or resize.** pHash resizes then
greyscales (ISCC does the reverse), uses Catmull-Rom on premultiplied RGBA, and excludes the DC
coefficient — and its values are cached in file-search-on's bbolt index (`Entry.PHash`), so changing
them silently invalidates every cached hash.

**The oracle** is `iscc-sdk`'s own published pixel values for `testdata/iscc_demo.{png,jpg}` — 28
bytes per file — plus the ISCC its `test_main.py` publishes for the JPEG. There is no Python here to
run, which is why those numbers are transcribed into `iscc_test.go` and their provenance recorded in
`testdata/README.md`. The PNG matches exactly; the JPEG is ±1 on a handful of pixels because Go's
`image/jpeg` and libjpeg round YCbCr→RGB differently, and the test bounds that drift rather than
ignoring it. Both still give `ISCC:EEA4GQZQTY6J5DTH`.

EXIF orientation is read by hand (`iscc_exif.go`: APP1, TIFF byte order, IFD0, tag 0x0112) rather
than by taking a dependency, and is best-effort throughout — a malformed or absent tag means no
transpose, never an error.

## Formats beyond PNG/JPEG/GIF (`iscc_formats.go`)

BMP, TIFF and WebP came later, and the plumbing was the easy half. What is worth not re-deriving:

- **`orientationFor(format, data)` routes on `image.Decode`'s OWN format string**, not on a sniff of
  our own, so the orientation can never be read out of a different format from the one that produced
  the pixels. Each format keeps the same tag somewhere else: JPEG in an Exif APP1 segment, **TIFF in
  its own IFD0** (the file *is* a TIFF header, so `tiffOrientation` takes it verbatim — that function
  was already a standalone parser and needed no change), **WebP in a RIFF `EXIF` chunk** that
  `x/image/webp` notices only as a VP8X flag bit and never surfaces. ISO 24138 transposes FIRST, so
  a missed orientation is a code for a rotation of the image, not a near miss.
- **`tiffRefusal` exists because `x/image/tiff` returns pixels that lie.** It does not read
  `PlanarConfiguration` at all and decodes a planar file as if interleaved — colour noise, no error;
  and for a DNG it happily returns the reduced-resolution preview from IFD0 as though it were the
  image (unless that preview is JPEG-compressed, in which case it errors — so the failure is
  *inconsistent*, which is worse). Both are refused by inspecting IFD0 ourselves. It returns "" for
  anything that is not a TIFF, because the walk declines an unrecognised header, so the caller needs
  no sniff.
- **`PHash` deliberately does NOT apply that guard**, and the asymmetry is intentional:
  screening would mean buffering every image in full to inspect its directory, and a perceptual hash
  that is wrong about one odd file simply fails to match, where a wrong ISCC is a claim that this
  content is some other content. Said in `PHash`'s doc comment; do not "fix" one to match the other
  without weighing that.
- **Exactness follows the codec, not the container** — the argument the tests rest on. For a
  lossless encoding there is exactly one correct answer, so a lossless decoder must reproduce the
  PNG oracle's pixels EXACTLY. `iscc_demo.lossless.webp` does (all 28 published values), and so do
  `tiff.Encode`/`bmp.Encode` round-trips. That is a conformance proof with no Python involved, and
  three independent codecs agreeing with the reference beats any one of them.
- **The measurement that overturned an assumption**: upstream's `demo.tif` is LZW — lossless — and
  was therefore expected to be exact. It is not; its pixels track `demo.jpg`, so upstream made it
  from the JPEG. Do not reinstate an exactness assertion on it. See `testdata/README.md`.
- **Lossy WebP is not the JPEG's ±1 story.** At quality 80 libwebp is far more aggressive than
  libjpeg at 75 (5 KB against 35 KB for the same photograph): 1004 of 1024 pixels move, worst by 20,
  and the ISCC is unchanged anyway. So its test asserts a *generous* pixel bound as a decode canary
  plus a pHash distance, not a conformance bound. The code-level assertion — every encoding giving
  `ISCC:EEA4GQZQTY6J5DTH` — lives in `c2pa-mcp`, which is where this package meets `iscc-lib`; this
  package still computes no ISCC and takes no dependency on one.
- **PNG's `eXIf` chunk is read too** (`pngOrientation`, closing #8): PNG 1.5 added it, Pillow reads
  it, so `iscc-sdk` transposes a rotated PNG — and before this a rotated PNG got the code of its
  rotation while the reference got the code of the image. The walk **stops at `IEND`**: bytes
  appended after a PNG has ended must not be able to reorient it, which is the same argument
  `c2pa`'s PDF reader makes about bytes after `%%EOF`.
  **The remaining divergence**: Pillow ALSO accepts PNG EXIF from a `tEXt`/`zTXt`/`iTXt` chunk keyed
  `exif` or `Raw profile type exif` — ImageMagick's older hex-encoded convention — which means
  hex-decoding a text chunk and is not implemented. Recorded here rather than left to be
  rediscovered; the `eXIf` chunk is what any modern encoder writes.
- `x/image` decoder limits that surface as clean errors and need no code: 16-bit BMP (Pillow reads
  it), JPEG-in-TIFF, BigTIFF, CMYK/YCbCr TIFF, 12/14-bit TIFF, animated WebP. A multi-page TIFF
  decodes its first directory only, which is what Pillow's frame 0 does too — correct, not a gap.
- **Hand-built fixtures over checked-in ones** where possible: `tinyTIFF` in
  `iscc_formats_test.go` assembles a valid uncompressed TIFF with arbitrary IFD entries (sorted
  ascending, which TIFF requires and x/image enforces), which is how the refusals and the TIFF
  orientation are tested without vendoring four more files. WebP has no Go encoder, so its two
  fixtures are generated once with `npx sharp` and the command is recorded.
