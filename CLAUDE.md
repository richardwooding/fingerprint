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
