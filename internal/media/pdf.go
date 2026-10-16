package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/png"
	"math"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"
)

const (
	maxPDFPreviewWidth  = 8192
	maxPDFPreviewEdge   = 4096
	maxPDFPreviewPixels = 4_000_000
	maxPDFTextChars     = 2_000_000
	maxPDFTextPositions = 500_000
	maxPDFPagePositions = 100_000
	maxPDFOutlineDepth  = 32
	maxPDFOutlineNodes  = 2000
	maxPDFOutlineItems  = 1000
)

type cropBox struct{ left, bottom, right, top float64 }

// PDFDocument owns the source buffer and native PDFium document until Close.
// Native PDFium calls are serialized because document/page objects are not
// shared between concurrent operations.
type PDFDocument struct {
	mu        sync.Mutex
	closed    bool
	api       *pdfiumAPI
	document  uintptr
	input     nativeInput
	options   Options
	info      DocumentInfo
	cropBoxes []cropBox
	cache     *pngCache
}

func OpenPDF(ctx context.Context, path string, options Options) (*PDFDocument, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	options, err := options.normalized()
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(filepath.Ext(path), ".pdf") {
		return nil, fmt.Errorf("%w：只允许预览 PDF 文件", ErrUnsupportedType)
	}
	data, err := readRegularFile(ctx, path, options.MaxPDFBytes, "PDF")
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, errors.New("PDF 文件为空")
	}
	if uint64(len(data)) > math.MaxInt32 {
		return nil, errors.New("PDF 文件超过 PDFium 内存文档 API 的大小上限")
	}
	api, err := loadPDFium(options.PDFiumPath)
	if err != nil {
		return nil, err
	}
	input, err := makeNativeInput(data)
	if err != nil {
		return nil, err
	}
	if err := acquirePDFium(ctx); err != nil {
		_ = input.close()
		return nil, err
	}
	defer releasePDFium()
	if err := contextError(ctx); err != nil {
		_ = input.close()
		return nil, err
	}
	document := api.loadMemDocument(input.ptr, int32(len(input.bytes)), "")
	runtime.KeepAlive(input.bytes)
	if document == 0 {
		code := api.getLastError()
		_ = input.close()
		if code == 4 {
			return nil, errors.New("PDF 需要密码，暂不支持预览；请使用外部阅读器打开")
		}
		return nil, fmt.Errorf("PDF 无法读取，文件可能已损坏或格式不受支持（PDFium error %d）", code)
	}
	pageCount := int(api.getPageCount(document))
	if pageCount <= 0 {
		api.closeDocument(document)
		_ = input.close()
		return nil, errors.New("PDF 没有可预览的页面")
	}
	if pageCount > options.MaxPDFPages {
		api.closeDocument(document)
		_ = input.close()
		return nil, fmt.Errorf("PDF 页数超过 %d 页预览限制，请使用外部阅读器打开", options.MaxPDFPages)
	}
	doc := &PDFDocument{
		api: api, document: document, input: input, options: options,
		info:      DocumentInfo{PageCount: pageCount, Pages: make([]PageInfo, 0, pageCount)},
		cropBoxes: make([]cropBox, 0, pageCount), cache: newPNGCache(options.CacheBytes),
	}
	for index := 0; index < pageCount; index++ {
		if err := contextError(ctx); err != nil {
			api.closeDocument(document)
			_ = input.close()
			return nil, err
		}
		page := api.loadPage(document, int32(index))
		if page == 0 {
			api.closeDocument(document)
			_ = input.close()
			return nil, fmt.Errorf("PDF 第 %d 页无法读取", index+1)
		}
		width := float64(api.getPageWidth(page))
		height := float64(api.getPageHeight(page))
		rotation := int(api.getPageRotation(page))
		api.closePage(page)
		if rotation < 0 || rotation > 3 {
			api.closeDocument(document)
			_ = input.close()
			return nil, fmt.Errorf("PDF 第 %d 页旋转信息无效", index+1)
		}
		rotation *= 90
		if !finitePositive(width) || !finitePositive(height) || !finitePositive(width/height) {
			api.closeDocument(document)
			_ = input.close()
			return nil, fmt.Errorf("PDF 第 %d 页页面尺寸无效", index+1)
		}
		box := pageCropBox(api, document, int32(index), width, height, rotation)
		doc.cropBoxes = append(doc.cropBoxes, box)
		doc.info.Pages = append(doc.info.Pages, PageInfo{
			Index: index, WidthPoints: width, HeightPoints: height,
			Rotation: rotation, AspectRatio: width / height,
		})
	}
	doc.info.Outline = readPDFOutline(ctx, api, document, pageCount)
	if err := contextError(ctx); err != nil {
		api.closeDocument(document)
		_ = input.close()
		return nil, err
	}
	return doc, nil
}

func pageCropBox(api *pdfiumAPI, document uintptr, index int32, displayedWidth, displayedHeight float64, rotation int) cropBox {
	page := api.loadPage(document, index)
	if page == 0 {
		return cropBox{right: displayedWidth, top: displayedHeight}
	}
	defer api.closePage(page)
	var left, bottom, right, top float32
	if api.getCropBox(page, &left, &bottom, &right, &top) == 0 || !(right > left && top > bottom) {
		left, bottom, right, top = 0, 0, 0, 0
		if api.getMediaBox(page, &left, &bottom, &right, &top) == 0 || !(right > left && top > bottom) {
			width, height := displayedWidth, displayedHeight
			if rotation == 90 || rotation == 270 {
				width, height = displayedHeight, displayedWidth
			}
			return cropBox{right: width, top: height}
		}
	}
	return cropBox{left: float64(left), bottom: float64(bottom), right: float64(right), top: float64(top)}
}

func (d *PDFDocument) Info() DocumentInfo {
	if d == nil {
		return DocumentInfo{}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return cloneDocumentInfo(d.info)
}

func (d *PDFDocument) RenderPagePNG(ctx context.Context, pageIndex, widthPx int) (RenderedPage, error) {
	if d == nil {
		return RenderedPage{}, ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if pageIndex < 0 {
		return RenderedPage{}, errors.New("PDF 页码无效")
	}
	if widthPx <= 0 {
		return RenderedPage{}, errors.New("PDF 预览宽度无效")
	}
	if err := contextError(ctx); err != nil {
		return RenderedPage{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.document == 0 {
		return RenderedPage{}, ErrClosed
	}
	pageIndex = minInt(pageIndex, d.info.PageCount-1)
	widthPx = minInt(widthPx, maxPDFPreviewWidth)
	pageInfo := d.info.Pages[pageIndex]
	width, height, err := pdfPixelDimensions(pageInfo.WidthPoints, pageInfo.HeightPoints, widthPx)
	if err != nil {
		return RenderedPage{}, err
	}
	key := fmt.Sprintf("pdf:%d:%d:%d", pageIndex, width, height)
	if png, ok := d.cache.get(key); ok {
		return RenderedPage{PageIndex: pageIndex, PageCount: d.info.PageCount,
			Width: width, Height: height, AspectRatio: pageInfo.AspectRatio, PNG: png}, nil
	}
	if err := acquirePDFium(ctx); err != nil {
		return RenderedPage{}, err
	}
	page := d.api.loadPage(d.document, int32(pageIndex))
	if page == 0 {
		releasePDFium()
		return RenderedPage{}, fmt.Errorf("PDF 第 %d 页无法读取", pageIndex+1)
	}
	bitmap := d.api.createBitmap(int32(width), int32(height), 0)
	if bitmap == 0 {
		d.api.closePage(page)
		releasePDFium()
		return RenderedPage{}, errors.New("PDFium 无法分配页面预览位图")
	}
	if d.api.fillRect(bitmap, 0, 0, int32(width), int32(height), 0xffffffff) == 0 {
		d.api.destroyBitmap(bitmap)
		d.api.closePage(page)
		releasePDFium()
		return RenderedPage{}, errors.New("PDFium 无法初始化页面预览位图")
	}
	d.api.renderPageBitmap(bitmap, page, 0, 0, int32(width), int32(height), 0, 1)
	if err := contextError(ctx); err != nil {
		d.api.destroyBitmap(bitmap)
		d.api.closePage(page)
		releasePDFium()
		return RenderedPage{}, err
	}
	imageData, err := copyPDFiumBitmap(d.api, bitmap, width, height)
	d.api.destroyBitmap(bitmap)
	d.api.closePage(page)
	releasePDFium()
	if err != nil {
		return RenderedPage{}, err
	}
	var output bytes.Buffer
	if err := png.Encode(&output, imageData); err != nil {
		return RenderedPage{}, fmt.Errorf("编码 PDF 页面失败：%w", err)
	}
	if err := contextError(ctx); err != nil {
		return RenderedPage{}, err
	}
	encoded := output.Bytes()
	d.cache.put(key, encoded)
	return RenderedPage{PageIndex: pageIndex, PageCount: d.info.PageCount,
		Width: width, Height: height, AspectRatio: pageInfo.AspectRatio, PNG: append([]byte(nil), encoded...)}, nil
}

func copyPDFiumBitmap(api *pdfiumAPI, bitmap uintptr, width, height int) (*image.RGBA, error) {
	buffer := api.getBitmapBuffer(bitmap)
	stride := int(api.getBitmapStride(bitmap))
	actualWidth := int(api.getBitmapWidth(bitmap))
	actualHeight := int(api.getBitmapHeight(bitmap))
	format := int(api.getBitmapFormat(bitmap))
	if buffer == nil || stride <= 0 || actualWidth != width || actualHeight != height {
		return nil, errors.New("PDFium 返回的页面位图无效")
	}
	switch format {
	case 1, 2, 3, 4, 5:
	default:
		return nil, fmt.Errorf("PDFium 返回了不支持的位图格式 %d", format)
	}
	pixels := unsafe.Slice((*byte)(buffer), stride*height)
	destination := image.NewRGBA(image.Rect(0, 0, width, height))
	bytesPerPixel := 4
	if format == 1 {
		bytesPerPixel = 1
	} else if format == 2 {
		bytesPerPixel = 3
	}
	if stride < width*bytesPerPixel {
		return nil, errors.New("PDFium 页面位图的行跨度无效")
	}
	for y := 0; y < height; y++ {
		sourceRow := pixels[y*stride : y*stride+width*bytesPerPixel]
		destinationRow := destination.Pix[y*destination.Stride : y*destination.Stride+width*4]
		for x := 0; x < width; x++ {
			source := x * bytesPerPixel
			to := x * 4
			switch format {
			case 1:
				destinationRow[to], destinationRow[to+1], destinationRow[to+2] = sourceRow[source], sourceRow[source], sourceRow[source]
			case 2:
				destinationRow[to], destinationRow[to+1], destinationRow[to+2] = sourceRow[source+2], sourceRow[source+1], sourceRow[source]
			default: // BGRx / BGRA, with the white page background made opaque above.
				destinationRow[to], destinationRow[to+1], destinationRow[to+2] = sourceRow[source+2], sourceRow[source+1], sourceRow[source]
			}
			destinationRow[to+3] = 0xff
		}
	}
	return destination, nil
}

func pdfPixelDimensions(width, height float64, requestedWidth int) (int, int, error) {
	if !finitePositive(width) || !finitePositive(height) || requestedWidth < 1 {
		return 0, 0, errors.New("PDF 页面尺寸无效")
	}
	requestedScale := float64(minInt(maxInt(requestedWidth, 1), maxPDFPreviewWidth)) / width
	dimensionScale := float64(maxPDFPreviewEdge-1) / math.Max(width, height)
	pixelScale := math.Sqrt(float64(maxPDFPreviewPixels-2) / (width * height))
	scale := math.Min(requestedScale, math.Min(dimensionScale, pixelScale))
	if !finitePositive(scale) {
		return 0, 0, errors.New("PDF 页面尺寸无效")
	}
	w := maxInt(1, int(math.Ceil(width*scale)))
	h := maxInt(1, int(math.Ceil(height*scale)))
	if w > maxPDFPreviewEdge || h > maxPDFPreviewEdge || int64(w)*int64(h) > maxPDFPreviewPixels {
		return 0, 0, errors.New("PDF 页面超过预览位图上限")
	}
	return w, h, nil
}

func (d *PDFDocument) TextIndex(ctx context.Context) (TextIndex, error) {
	if d == nil {
		return TextIndex{}, ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := contextError(ctx); err != nil {
		return TextIndex{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.document == 0 {
		return TextIndex{}, ErrClosed
	}
	if err := acquirePDFium(ctx); err != nil {
		return TextIndex{}, err
	}
	defer releasePDFium()
	index := TextIndex{Pages: make([]TextPage, 0, d.info.PageCount), Outline: append([]OutlineEntry(nil), d.info.Outline...)}
	totalChars, totalPositions := 0, 0
	for pageIndex := 0; pageIndex < d.info.PageCount; pageIndex++ {
		if err := contextError(ctx); err != nil {
			return TextIndex{}, err
		}
		page := d.api.loadPage(d.document, int32(pageIndex))
		if page == 0 {
			return TextIndex{}, fmt.Errorf("PDF 第 %d 页文字无法读取", pageIndex+1)
		}
		textPage := d.api.textLoadPage(page)
		if textPage == 0 {
			d.api.closePage(page)
			return TextIndex{}, fmt.Errorf("PDF 第 %d 页文字无法读取", pageIndex+1)
		}
		charCount := int(d.api.textCountChars(textPage))
		if charCount < 0 || charCount > maxPDFPagePositions {
			d.api.textClosePage(textPage)
			d.api.closePage(page)
			return TextIndex{}, errors.New("PDF 文字较多，请使用外部阅读器查找文字")
		}
		totalPositions += charCount
		if totalPositions > maxPDFTextPositions {
			d.api.textClosePage(textPage)
			d.api.closePage(page)
			return TextIndex{}, errors.New("PDF 文字较多，请使用外部阅读器查找文字")
		}
		pageText, err := readPDFPageText(ctx, d.api, textPage, d.cropBoxes[pageIndex], d.info.Pages[pageIndex].Rotation)
		d.api.textClosePage(textPage)
		d.api.closePage(page)
		if err != nil {
			return TextIndex{}, err
		}
		totalChars += utf8.RuneCountInString(pageText.Text)
		if totalChars > maxPDFTextChars {
			return TextIndex{}, errors.New("PDF 文字较多，请使用外部阅读器查找文字")
		}
		index.Pages = append(index.Pages, pageText)
	}
	if err := contextError(ctx); err != nil {
		return TextIndex{}, err
	}
	return index, nil
}

func readPDFPageText(ctx context.Context, api *pdfiumAPI, textPage uintptr, box cropBox, rotation int) (TextPage, error) {
	count := int(api.textCountChars(textPage))
	if count < 0 || count > maxPDFPagePositions {
		return TextPage{}, errors.New("PDF 文字较多，请使用外部阅读器查找文字")
	}
	runes := make([]rune, 0, count)
	glyphs := make([]Glyph, 0, count)
	for index := 0; index < count; index++ {
		if index&127 == 0 {
			if err := contextError(ctx); err != nil {
				return TextPage{}, err
			}
		}
		codepoint := api.textGetUnicode(textPage, int32(index))
		if codepoint == 0 {
			continue
		}
		if codepoint > unicode.MaxRune || codepoint >= 0xd800 && codepoint <= 0xdfff {
			codepoint = unicode.ReplacementChar
		}
		start := len(runes)
		runes = append(runes, rune(codepoint))
		var left, right, bottom, top float64
		if api.textGetCharBox(textPage, int32(index), &left, &right, &bottom, &top) != 0 {
			if glyph, ok := normalizedGlyph(box, rotation, left, right, bottom, top, start, start+1); ok {
				glyphs = append(glyphs, glyph)
			}
		}
	}
	trimStart, trimEnd := 0, len(runes)
	for trimStart < trimEnd && unicode.IsSpace(runes[trimStart]) {
		trimStart++
	}
	for trimEnd > trimStart && unicode.IsSpace(runes[trimEnd-1]) {
		trimEnd--
	}
	for index := range glyphs {
		glyphs[index].Start -= trimStart
		glyphs[index].End -= trimStart
	}
	filtered := glyphs[:0]
	for _, glyph := range glyphs {
		if glyph.Start >= 0 && glyph.End <= trimEnd-trimStart && glyph.Start < glyph.End {
			filtered = append(filtered, glyph)
		}
	}
	return TextPage{Text: string(runes[trimStart:trimEnd]), Glyphs: filtered}, nil
}

func normalizedGlyph(box cropBox, rotation int, left, right, bottom, top float64, start, end int) (Glyph, bool) {
	width, height := box.right-box.left, box.top-box.bottom
	if !finitePositive(width) || !finitePositive(height) || !finite(left) || !finite(right) || !finite(bottom) || !finite(top) {
		return Glyph{}, false
	}
	type point struct{ x, y float64 }
	mapPoint := func(x, y float64) point {
		switch rotation {
		case 90:
			return point{(y - box.bottom) / height, (x - box.left) / width}
		case 180:
			return point{(box.right - x) / width, (y - box.bottom) / height}
		case 270:
			return point{(box.top - y) / height, (box.right - x) / width}
		default:
			return point{(x - box.left) / width, (box.top - y) / height}
		}
	}
	corners := []point{mapPoint(left, bottom), mapPoint(left, top), mapPoint(right, bottom), mapPoint(right, top)}
	minX, maxX := corners[0].x, corners[0].x
	minY, maxY := corners[0].y, corners[0].y
	for _, corner := range corners[1:] {
		minX, maxX = math.Min(minX, corner.x), math.Max(maxX, corner.x)
		minY, maxY = math.Min(minY, corner.y), math.Max(maxY, corner.y)
	}
	minX, maxX = clampUnit(minX), clampUnit(maxX)
	minY, maxY = clampUnit(minY), clampUnit(maxY)
	if maxX <= minX || maxY <= minY {
		return Glyph{}, false
	}
	return Glyph{Start: start, End: end, Left: float32(minX), Top: float32(minY), Right: float32(maxX), Bottom: float32(maxY)}, true
}

func readPDFOutline(ctx context.Context, api *pdfiumAPI, document uintptr, pageCount int) []OutlineEntry {
	result := make([]OutlineEntry, 0)
	visited := make(map[uintptr]struct{})
	var visit func(parent uintptr, depth int)
	visit = func(parent uintptr, depth int) {
		if depth > maxPDFOutlineDepth || len(visited) >= maxPDFOutlineNodes || len(result) >= maxPDFOutlineItems {
			return
		}
		bookmark := api.bookmarkFirstChild(document, parent)
		for bookmark != 0 && len(visited) < maxPDFOutlineNodes && len(result) < maxPDFOutlineItems {
			if contextError(ctx) != nil {
				return
			}
			if _, exists := visited[bookmark]; exists {
				return
			}
			visited[bookmark] = struct{}{}
			pageIndex := -1
			destination := api.bookmarkGetDest(document, bookmark)
			if destination == 0 {
				action := api.bookmarkGetAction(bookmark)
				if action != 0 {
					destination = api.actionGetDest(document, action)
				}
			}
			if destination != 0 {
				pageIndex = int(api.destGetPageIndex(document, destination))
			}
			if pageIndex >= 0 && pageIndex < pageCount {
				result = append(result, OutlineEntry{Title: pdfBookmarkTitle(api, bookmark), PageIndex: pageIndex, Depth: depth})
			}
			visit(bookmark, depth+1)
			bookmark = api.bookmarkNextSibling(document, bookmark)
		}
	}
	visit(0, 0)
	return result
}

func pdfBookmarkTitle(api *pdfiumAPI, bookmark uintptr) string {
	length := int(api.bookmarkGetTitle(bookmark, nil, 0))
	if length <= 2 || length > 8192 {
		return ""
	}
	buffer := make([]byte, length)
	api.bookmarkGetTitle(bookmark, unsafe.Pointer(&buffer[0]), uint32(len(buffer)))
	units := make([]uint16, len(buffer)/2)
	for index := range units {
		units[index] = binary.LittleEndian.Uint16(buffer[index*2 : index*2+2])
	}
	if len(units) > 0 && units[len(units)-1] == 0 {
		units = units[:len(units)-1]
	}
	title := string(utf16.Decode(units))
	titleRunes := []rune(title)
	if len(titleRunes) > 300 {
		title = string(titleRunes[:300])
	}
	return title
}

func (d *PDFDocument) ClearCache() {
	if d == nil {
		return
	}
	d.cache.clear()
}

func (d *PDFDocument) Close() error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	d.cache.clear()
	if d.document != 0 {
		if err := acquirePDFium(context.Background()); err != nil {
			return err
		}
		d.api.closeDocument(d.document)
		d.document = 0
		releasePDFium()
	}
	return d.input.close()
}

func cloneDocumentInfo(info DocumentInfo) DocumentInfo {
	clone := DocumentInfo{PageCount: info.PageCount}
	clone.Pages = append([]PageInfo(nil), info.Pages...)
	clone.Outline = append([]OutlineEntry(nil), info.Outline...)
	return clone
}

func finite(value float64) bool         { return !math.IsNaN(value) && !math.IsInf(value, 0) }
func finitePositive(value float64) bool { return value > 0 && finite(value) }
func clampUnit(value float64) float64   { return math.Max(0, math.Min(1, value)) }
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
