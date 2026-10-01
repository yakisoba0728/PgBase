# Bounded image orientation and transparent thumbnails

Development baseline: `abc94ef` on `codex/remaining-development`; this record describes subsequent changes.

## Product contract

JPEG thumbnails apply EXIF IFD0 orientation values 2 through 8 before sizing and cropping. Both the upload-time ordinary thumbnail and named variants use the normalized dimensions and pixels. Missing, malformed, out-of-range, or excessively large EXIF directories are ignored and the decoded pixels retain their original orientation.

Thumbnail output is determined by the decoded source format: JPEG produces JPEG, PNG produces PNG, and GIF or WebP produces PNG. GIF and animated WebP decoding uses the first displayed frame; animation is not preserved. Animated WebP follows the [WebP container specification](https://developers.google.com/speed/webp/docs/riff_container): the bounded ANMF frame is decoded from its VP8L or ALPH/VP8 subchunks, placed at its doubled frame offsets on the declared canvas, and composed over the ANIM background according to the frame blend flag. Disposal and later frames cannot affect the first displayed still. PNG output retains transparent pixels from GIF and WebP sources. `RenderThumbnail`, `RenderThumbnailReader`, and `RenderThumbnailData` use the same rules, so JavaScript `createThumb` calls receive the same bytes as record-file variants.

Existing cached GIF/WebP thumbnails and named variants may contain JPEG bytes produced by earlier releases. Download responses therefore sniff the selected transformed object's bytes rather than inferring its media type from the original file. This preserves truthful `Content-Type` for both legacy JPEG cache entries and newly generated PNG entries without rewriting stored objects or adding mutable cache metadata.

## Bounds and failure behavior

The existing `MaximumBytes` limit bounds the complete encoded input before image, WebP frame, or EXIF decoding. The existing `MaximumImagePixels` limit is applied to the animated canvas and to the extracted frame's encoded configuration before either pixel buffer is decoded or allocated, then checked again against decoded and requested dimensions. WebP parsing requires exact RIFF and padded chunk bounds, caps both container and frame traversal at 1024 chunks, rejects duplicate required chunks and bitstreams, validates frame rectangles against the canvas, and requires the embedded VP8/VP8L dimensions to equal the ANMF declaration. EXIF parsing does not allocate from metadata lengths, follows offsets only inside the APP1 payload, and accepts at most 1024 IFD0 entries. Corrupt metadata does not make an otherwise valid JPEG fail; corrupt image data, a declared/decoded format mismatch, byte-limit overflow, pixel-limit overflow, or encoded output over the byte limit still returns `ErrInvalid`.

## Regression coverage

The focused filestore suite checks all eight EXIF orientations against a non-square asymmetric pixel table, malformed offsets and oversized IFD entry counts, transparent GIF and WebP-to-PNG encoding, a valid ANMF/VP8L fixture that the base decoder rejects, transparent VP8L background blending and replacement, an uncompressed ALPH plus VP8 frame, frame/canvas bounds, the ordinary upload thumbnail path, named variants, caller-owned `RenderThumbnailData`, and byte/pixel resource limits:

```powershell
go test ./internal/filestore -count=1
```

The file HTTP tests cover truthful transformed response media types, including legacy cached JPEG and new PNG output. The JavaScript filesystem integration exercises `RenderThumbnail` through `objects.createThumb`; the local compatibility bridge exercises `RenderThumbnailReader` through `createThumb`.

No dependency was added. Runtime production code adds a bounded EXIF parser and orientation transform, changes intermediate buffers from RGBA to NRGBA, and centralizes output encoding. Root owns the final repository-wide runtime delta and integration results.
