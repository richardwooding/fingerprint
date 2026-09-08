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
