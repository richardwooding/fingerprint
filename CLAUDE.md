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
- **Image decoders are registered via blank imports** in `phash.go` (`image/gif`, `image/jpeg`, `image/png`). Add a format → add its blank import.
- Several doc comments reference the upstream project (`index.Entry.PHash`, CEL `image_similar_to`, "Issue #208"). This library was extracted from [file-search-on](https://github.com/richardwooding/file-search-on); those references are historical context, not code in this repo.

## Tests

`testdata/sample.jpg` is the real-image fixture (`loadFixture`); `phash_test.go` also synthesizes images in-memory (`renderTestImage`). pHash tests assert robustness invariants (survives resize / JPEG re-encode, distinguishes different scenes). `example_test.go` holds runnable `Example_*` godoc examples — keep them passing as they double as documentation.

## ISCC normalisation (`iscc.go`, `iscc_exif.go`)

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
