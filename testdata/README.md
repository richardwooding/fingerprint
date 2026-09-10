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

## iscc_demo_audio.wav, iscc_demo_audio.fpcalc.json

The audio counterpart to `iscc_demo.png`, and the oracle for `Chromaprint`.

The recording is "Belly Button", 15.5 seconds, from `iscc_samples`
(`iscc_samples/files/audio/demo.wav`) by Titusz Pan, **CC-BY-4.0** — the same collection, author
and licence as the image fixtures above.

Upstream ships it as 24-bit stereo 44.1 kHz, which is 4.1 MB. What is vendored here is that file
converted once to the rate Chromaprint actually fingerprints at, which is 342 KB:

```sh
ffmpeg -i demo.wav -ac 1 -ar 11025 -sample_fmt s16 -c:a pcm_s16le iscc_demo_audio.wav
```

The conversion is not a convenience — it is what makes the fixture an oracle. `fpcalc` resamples
with FFmpeg's `swresample` before Chromaprint ever sees the audio, so its numbers for a 44.1 kHz
file are FFmpeg's resampler as much as they are Chromaprint. Feed it audio already at 11025 Hz mono
and the resampler is a no-op, leaving Chromaprint alone under test. That is the only reason this
package can claim an exact match at all.

`iscc_demo_audio.fpcalc.json` is the verbatim stdout of

```sh
fpcalc -raw -json -signed -length 0 iscc_demo_audio.wav
```

run with **fpcalc 1.6.0 (FFmpeg Lavc62.11.100 Lavf62.3.100 SwR6.1.100)**. It is committed as
`fpcalc` printed it rather than transcribed, so there is no hand-copying step to get wrong.

| oracle | source | what it pins |
| --- | --- | --- |
| all 104 subfingerprints | `fpcalc -raw -signed` on this file | the whole pipeline, every bit |
| the same 104 values for `demo.mp3` | `iscc-sdk` `tests/test_audio.py::test_audio_extract_features` | that this `fpcalc` build agrees with the reference |
| `ISCC:EIAWUJFCEZZOJYVD` | `iscc-sdk` `tests/test_audio.py::test_code_audio_mp3` / `test_code_audio_wav` | the vector end to end, through `GenAudioCodeV0` |

**This package matches all 104 values exactly**, and `iscc.GenAudioCodeV0(cv, 64)` on them returns
`ISCC:EIAWUJFCEZZOJYVD` — the code `iscc-sdk` publishes for this recording.

**Three encodings, one code, and the measurement that says so.** `fpcalc` was run on all three
forms of the recording: the 24-bit stereo 44.1 kHz master, the 225 KB MP3, and this 16-bit mono
11025 Hz derivative. The master and the MP3 agree on all 104 values. The derivative — resampled to
a quarter of the rate, folded to mono and requantised to 16 bits — differs from them in **2 values
of 104, which is 2 bits of 3328**, and every one of the three still produces
`ISCC:EIAWUJFCEZZOJYVD`. That is the audio twin of the lossy-WebP paragraph above, and a sharper
version of it: the pixels there moved under re-encoding, and here the sample rate itself moved.

## `chromaprint_synth_<rate>_<channels>.fpcalc.json`

The resampling oracles: sixteen of them, one per sample rate and channel count, each the verbatim
stdout of `fpcalc -raw -json -signed -length 0` on a synthetic six-second clip.

**The audio is generated, not committed.** `synthPCM` in `chromaprint_synth_test.go` builds it from
three triangle partials, a seeded noise floor and a slow envelope, using **integer arithmetic
only** — Go permits fused multiply-add, so a floating-point generator is not guaranteed to produce
identical bytes on every architecture, and an oracle made on one machine would then fail on
another. The sixteen WAVs come to about 10 MB; the JSON that pins them comes to 64 KB, so only the
JSON is here. `synthDigests` in the same file pins the generator's output, and fails first and
loudly if an edit to `synthPCM` would leave these oracles describing audio that no longer exists.
See CLAUDE.md for the regeneration commands.

The rates are chosen to reach both of `swresample`'s convolution paths: 11025 is a passthrough,
22050 and 44100 reduce to a single phase, 48000 and 96000 reduce to 147 phases, and 8000, 16000 and
32000 do not reduce below the 256-phase bank, so a fractional phase carries and the interpolating
path runs. A resampler can be right about one path and wrong about the other.

## The resampler measurement

Two numbers worth keeping, both taken on the 44.1 kHz master of this recording.

**Chromaprint's own resampler is the wrong one.** `fpcalc` resamples with FFmpeg's `swresample`
before Chromaprint sees a sample; Chromaprint's internal `av_resample` is reached only by a library
caller feeding raw PCM. Running the vendored `av_resample` instead moves **62 of 3328 fingerprint
bits** and yields `ISCC:EIAXUJFCEZZOJYVC` rather than the published `ISCC:EIAWUJFCEZZOJYVD` — two
bits apart in the code, which matches under a similarity threshold and fails under equality. That
is the whole reason this package ports `swresample`.

**`swresample` is not one number either, and it does not matter.** Its hand-written SIMD kernels
and its portable C differ by about one unit in the last place, which shows up as **54 of 170917
resampled samples differing by 1**. After the step back to 16 bits the two are byte-identical, and
the fingerprints are identical to the bit. This package matches the portable C exactly, and
therefore matches both.

## iscc_demo_audio.mp3, iscc_demo_audio.mp3.fpcalc.json

The same recording as `iscc_demo_audio.wav`, and the same CC-BY-4.0 by Titusz Pan — but this one is
**copied verbatim from `iscc_samples`** (`iscc_samples/files/audio/demo.mp3`) rather than derived,
because it is the exact file `iscc-sdk` publishes numbers for.

That makes it the strongest fixture here. `fpcalc -raw -json -signed -length 0` on this file
reproduces the 104-element vector in `iscc-sdk`'s `tests/test_audio.py` element for element, and
this package reproduces that in turn — so the chain runs from committed Go code to the reference
implementation's own published constants with nothing taken on trust in between.

It is also the fixture that pins the gapless trimming. Decoded naively the samples begin **2257
frames early** — one Xing header frame at 1152, the LAME tag's 576-frame encoder delay, and the
decoder's 529-sample group delay — and every 124 ms frame is re-cut, costing 222 of 3328 bits. The
committed oracle fails immediately if any of those three constants moves.

**The MP3 and the lossless WAV differ by 2 bits of 3328** and produce the same
`ISCC:EIAWUJFCEZZOJYVD`. `TestMP3AgreesWithTheLosslessMaster` holds that to a bound of 4 rather
than asserting equality, because the two paths are a lossy codec and a resampler and they are
genuinely allowed to move — just not much.

**No MPEG-2 fixture is committed**, because the reader refuses the whole version. `go-mp3` rejects
MPEG-2.5 outright and mis-decodes some MPEG-2 configurations badly: at 22050 Hz, 96 kbps comes out
wrong by ~1100 of 3328 bits while 64 kbps comes out exact, and nothing in the header separates
them. Every MPEG-1 configuration measured is exact — 32000/44100/48000 Hz at 64/128/192/320 kbps,
mono and stereo, plus VBR at three quality settings: 26 of 27 bit-identical, the twenty-seventh
differing in a single bit that does not change the code.
