# Native image and PDF preview

`internal/media` provides local image and PDF preview without a WebView or JVM.
Images are decoded and resized in Go. PDFs are rendered through the PDFium C
API using `purego`; the UI receives independently owned RGBA pixels and owns display, zoom, pan, page
navigation, selection, and overlays.

## API

```go
doc, err := media.OpenPDF(ctx, absolutePath, media.Options{})
info := doc.Info() // page dimensions, page count, internal outline
page, err := doc.RenderPagePNG(ctx, pageIndex, requestedWidthPx)
pixels, err := doc.RenderPageRGBA(ctx, pageIndex, requestedWidthPx) // in-process UI
index, err := doc.TextIndex(ctx) // text plus normalized glyph rectangles
index, err = doc.TextIndexPrioritized(ctx, visiblePageIndices)
doc.ClearCache()
err = doc.Close()

image, err := media.OpenImage(ctx, absolutePath, media.Options{})
imageInfo := image.Info()
preview, err := image.RenderPNG(ctx, requestedWidthPx)
pixels, err = image.RenderRGBA(ctx, requestedWidthPx)
image.Close()
```

The caller must resolve the selected file through the existing materials-path
resolver before passing its absolute path. The backend independently rejects a
symlink as the final file, non-regular files, oversized inputs, and malformed
content. It does not walk directories or decide which files are visible.

`Options` zero values select these defaults:

| Limit | Default |
| --- | ---: |
| PDF encoded bytes | 64 MiB |
| PDF pages | 10,000 |
| PDF rendered page | requested width up to 8,192 px; 4,096 px maximum edge; 4,000,000 pixels |
| Image encoded bytes | 32 MiB |
| Decoded image pixels | 20,000,000 |
| Combined PNG and RGBA LRU caches | 32 MiB, divided equally between formats |

`CacheBytes: 0` selects the default cache; a negative cache size is invalid.
`ClearCache` drops both caches. Returned PNG slices and RGBA pixels are owned
copies; neither aliases mutable cache storage or freed PDFium buffers. The UI
uses RGBA directly to avoid PNG compression and decompression. Memory used by
the original decoded image, an active render, and images retained by callers
is outside the cache budget. Call `Close` when a preview is dismissed or
its file changes.

### PDF display and text

- Open once and keep the `PDFDocument` while the preview is visible. `Info`
  returns a defensive copy. Pages should be requested lazily as the UI scrolls;
  `PageInfo` dimensions already account for page rotation and use PDF points.
- `RenderPagePNG` clamps the requested width to 8,192 and ensures rendered
  edges/pixels remain bounded. It clamps a page index above the last page to
  the last page, but rejects negative indices and non-positive widths.
- PDFium receives the source bytes in a stable native allocation for the
  document lifetime. Calls into PDFium are serialized process-wide.
- `TextIndex` returns UTF-8 text per page and one `Glyph` rectangle per mapped
  character where PDFium supplies a box. `Start` and `End` are half-open rune
  offsets into that page's `Text`; rectangles are normalized to the displayed
  page, with origin at the top left and coordinates in `[0, 1]`. The UI can
  use these for find highlights and text selection/copy overlays.
- Text extraction closes page handles and releases document/PDFium execution
  rights between pages. `TextIndexPrioritized` visits visible pages first and
  still returns a complete index. Visible page renders can run between index
  pages; cancellation never leaves a native page handle open.
- The desktop maintains one bounded render queue per document, replaces pending
  widths for the same page and removes offscreen requests. Results still pass
  document, generation and current-width checks before reaching the UI.
- `OutlineEntry` contains only internal page destinations. The backend does
  not execute PDF actions, URLs, or scripts. Scanned pages have no searchable
  text unless the PDF contains an OCR text layer. Unusual font encodings and
  bidirectional glyph ordering can differ from PDFBox's text extraction.
- Password-protected documents are rejected with a clear message. Invalid or
  damaged documents return a parse error; no external reader is launched.

### Images

Supported extensions match the current materials preview: PNG, JPG/JPEG, WebP,
GIF, BMP, and ICO. JPEG EXIF orientations 1–8 are applied before returning the
image. GIF is intentionally rendered as its first frame. ICO selects the
largest valid entry and supports embedded PNG and supported DIB/BMP entries.
Positive `RenderPNG` widths downsample only; they do not enlarge source pixels.
Pass width `0` to request the original dimensions. The shared native UI viewer
draws images and diagrams directly on a dim overlay. It has no white dialog,
title, zoom toolbar, fit/original-size buttons or footer hints. Initial fit and
centering use measured canvas bounds; wheel zoom and drag pan use the same
coordinates. Escape or a blank-mask click closes the viewer; a quiet corner
close icon remains available. The originating reader keeps its content and
position. The media backend continues to own decoding, not these controls.

### Cancellation and ownership

File reads check `ctx` between 64 KiB chunks; EXIF orientation checks once per
row. Go image decoders and the resampling call are synchronous and cannot be
interrupted mid-call; cancellation is checked before/after these operations.
PDFium's page-render function is synchronous too, so cancellation is checked
before entering PDFium and immediately after it returns. Cancellation does not
interrupt a native call already in progress. A PDFium operation can wait for
another document's serialized operation; its context cancels that wait.

`Close` is idempotent and releases the document's native input and per-document
cache. Do not call methods after `Close`; they return `ErrClosed`. `Info` on a
closed document returns its last snapshot. A UI worker should stop scheduling
requests for a document before closing it.

## PDFium runtime and redistribution

The pinned validation runtime is PDFium `157.0.8086.0` (`chromium/8086`) from
the upstream [`bblanchon/pdfium-binaries` release](https://github.com/bblanchon/pdfium-binaries/releases/tag/chromium%2F8086):

- Validated asset: [`pdfium-win-x64.tgz`](https://github.com/bblanchon/pdfium-binaries/releases/download/chromium/8086/pdfium-win-x64.tgz), 3,866,531 bytes.
- Archive SHA-256: `1fd8af952832dbb0eb16d9249f68fe09e5f5ebf7c3dd9f6066ea2720cc28487d`.
- Extracted `bin/pdfium.dll`: 7,494,656 bytes; SHA-256 `1f2aee4286b8cf0413ab4562b7121aa454f9cfd9afd6ca8609bcef80927b3f80`.
- The tested runtime is **Windows x64 only**. The loader has Windows, Linux,
  and macOS implementations, but this validation does not establish a runtime
  asset, build, or distribution for Linux/macOS or other architectures.
- Runtime lookup checks an explicit `Options.PDFiumPath` first, then
  `SILVERWING_PDFIUM_PATH`, then `pdfium.dll` beside the executable or in its
  `native/` subdirectory. It also checks `resources/native/` beside Windows
  and Linux executables, and `../Resources/native/` from a macOS executable in
  `Contents/MacOS`. These match the native runtime packaging layout. On
  Linux/macOS, the corresponding names are `libpdfium.so` and
  `libpdfium.dylib`. A directory path is accepted and the platform library
  name is appended.
- If the library is absent or fails to load, `OpenPDF` returns an actionable
  error. It never falls back to WebView, a JVM process, or a fake success.
  The first PDFium library loaded remains process-wide; a later attempt to
  select a different explicit library path is rejected.

The archive's root `LICENSE` covers the `pdfium-binaries` distribution/build
repository (MIT). It is not PDFium's license. The archive's
`licenses/pdfium.txt` contains the PDFium Authors' BSD 3-Clause text and an
Apache 2.0 text; keep that packaged file intact. **Redistribution must preserve
both the archive-root `LICENSE` and the entire archive `licenses/` directory**
alongside the DLL so notices for PDFium and its third-party dependencies remain
available. Do not replace these files with a single generic PDFium notice or
attribute the root MIT license to PDFium. See the upstream
[`LICENSE`](https://github.com/bblanchon/pdfium-binaries/blob/chromium/8086/LICENSE)
and [`licenses/`](https://github.com/bblanchon/pdfium-binaries/tree/chromium/8086/licenses)
for the source copies.

## Integration boundaries

PDF rendering and text extraction use PDFium. The media package owns decoding,
page limits, geometry and bounded rasterization; it has no knowledge of task
business flows or external services. Native UI adapters own fit, zoom, pan,
selection and lightbox behavior. Keep representative PDF and image fixtures
in the UI regression checks when changing these boundaries.
