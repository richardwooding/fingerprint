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
h1, _ := fingerprint.PHash(file1) // io.Reader of a PNG/JPEG/GIF
h2, _ := fingerprint.PHash(file2)

if fingerprint.Distance(h1, h2) <= 10 {
    // visually similar: resizes, re-encodes, light crops/edits
}

hex := fingerprint.PHashHex(h1)        // 16-char hex for storage
back, _ := fingerprint.PHashFromHex(hex)
```

`PHash` downscales to 32×32 greyscale, runs a 2-D DCT, and keeps the sign of the low-frequency
coefficients — the classic perceptual-hash recipe, robust to scaling and re-compression.

## Requirements

- **Go 1.25+** — the SimHash half is pure stdlib; the pHash half uses
  [`golang.org/x/image`](https://pkg.go.dev/golang.org/x/image) for high-quality downscaling,
  which sets the floor.

## License

MIT — see [LICENSE](LICENSE).

---

Extracted from [file-search-on](https://github.com/richardwooding/file-search-on), where it
powers `find_near_duplicates` and `image_similar_to`.
