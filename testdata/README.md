# testdata

## sample.jpg

A tiny re-encoded crop of a public-domain photograph from Wikimedia Commons, used by the pHash
tests. Public domain.

## iscc_demo.png, iscc_demo.jpg

The same 200×133 photograph in two encodings, copied verbatim from
[iscc/iscc-samples](https://github.com/iscc/iscc-samples) (`iscc_samples/files/image/demo.png` and
`demo.jpg`), by Titusz Pan, **CC-BY-4.0**. Its `pyproject.toml` declares `license = "CC-BY-4.0"`
alongside an MIT classifier; attributing under CC-BY-4.0 satisfies both.

These are the oracle for `ISCCPixels`, and they are the reason it can claim conformance at all. ISO
24138's own conformance vectors start from 1024 already-normalised pixels, so they say nothing about
the normalisation — but `iscc-sdk`'s test suite publishes the exact pixel values its reference
implementation produces for these two files, and its `test_main.py` publishes the resulting ISCC.
That gives two independent checks without running Python:

| oracle | source | what it pins |
| --- | --- | --- |
| the first and last 14 of the 1024 pixels | `iscc-sdk` `tests/test_image.py` | the greyscale conversion and the bicubic resample, byte for byte |
| `ISCC:EEA4GQZQTY6J5DTH` for `demo.jpg` | `iscc-sdk` `tests/test_main.py` | the whole pipeline, end to end |

**The PNG matches all 28 published values exactly. The JPEG differs by ±1 in six of them**, because
Go's `image/jpeg` and libjpeg round the YCbCr→RGB conversion differently — a decoder difference, not
a normalisation one, which the PNG's exactness proves. It does not change the outcome: both files
still produce `ISCC:EEA4GQZQTY6J5DTH`, the same code the reference publishes, and the same code for
both encodings of one photograph — which is the whole point of a perceptual identifier.

## iscc_demo.tif

The same photograph again, copied verbatim from `iscc_samples/files/image/demo.tif` — same author,
same **CC-BY-4.0**. A Photoshop TIFF: **LZW compressed with horizontal differencing (Predictor 2)**,
RGB, 8 bits × 3 samples, one strip. It exists because Go's own TIFF encoder cannot write LZW, so
this is the only way to exercise the predictor undo, and because it is what a real-world TIFF
actually looks like.

**It is not an oracle, and it took a measurement to find out why.** LZW is lossless, so this file
was expected to decode to the PNG's pixels exactly — it does not. Its raw pixels track `demo.jpg`
(first pixel 48,43,39 against the JPEG's 48,43,38 and the PNG's 51,43,41), so upstream produced it
from the JPEG rather than from the lossless master. 187 of the 1024 normalised pixels therefore
differ from the PNG's by 1. The exactness claim for TIFF rests on `tiff.Encode` round-trips instead,
which have no such provenance question; this fixture is held to a bound of 2, which a broken
predictor would miss by a mile. It still produces `ISCC:EEA4GQZQTY6J5DTH`.

## iscc_demo.lossless.webp, iscc_demo.lossy.webp

Derivatives of `iscc_demo.png`, and therefore of the same CC-BY-4.0 photograph. Generated once with
[sharp](https://sharp.pixelplumbing.com) 0.34 (libvips 8.18.6, **libwebp 1.6.0** — the reference
encoder), since neither Go's standard library nor `golang.org/x/image` can write WebP:

```js
const sharp = require('sharp');
await sharp('iscc_demo.png').webp({lossless: true}).toFile('iscc_demo.lossless.webp');
await sharp('iscc_demo.png').webp({quality: 80}).toFile('iscc_demo.lossy.webp');
```

**The lossless one is the conformance proof for WebP**: VP8L is exact, so it must reproduce the
PNG's normalised pixels, and it matches all 28 published reference values byte for byte. libwebp
encoded it, `x/image/webp` decoded it, and Pillow's numbers agree.

**The lossy one is the demonstration.** At 5 KB it is seven times smaller than the 35 KB JPEG of the
same photograph; 1004 of the 1024 normalised pixels move, the worst by 20 — and the ISCC does not
change. That is what a soft binding is for, and no other fixture here makes the point as sharply.

## A format the reference reads and this package cannot

`iscc_samples` also ships `demo.bmp`, and it is deliberately **not** vendored here: it is 16 bits
per pixel, and `golang.org/x/image/bmp` supports 1/2/4/8/24/32 only. Pillow reads it, so `iscc-sdk`
would fingerprint it and this package cannot. BMP support here covers the paletted, 24-bit and
32-bit cases, which is proven by a `bmp.Encode` round-trip rather than by a checked-in file.
