package desktop

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode"

	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	"silverwing-go/internal/media"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type nativeMediaState struct {
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	closed bool

	path string
	kind string

	pdf       *media.PDFDocument
	image     *media.ImageDocument
	pdfInfo   media.DocumentInfo
	imageInfo media.ImageInfo
	textIndex *media.TextIndex

	page            int
	zoom            int
	fit             bool
	bitmap          *ui.Bitmap
	bitmapKey       string
	wantedRenderKey string
	renderLoading   bool
	renderCancel    context.CancelFunc
	openLoading     bool
	textLoading     bool
	textError       string
	search          string
	searchKey       string
	searchResults   []pdfTextMatch
	searchResult    int
	materialsHit    *documents.SearchHit
	pdfSelection    pdfTextSelection
	pdfPageBounds   map[int]ui.Rect
	selecting       bool
	showPDFText     bool
	pageInput       string
	viewerWidth     float32
	viewerHeight    float32
	previewError    string

	pdfPages                  ui.ListState
	pdfPageScroll             ui.ScrollState
	pdfPageBitmaps            map[int]pdfPageBitmap
	pdfPageRenders            map[string]context.CancelFunc
	pdfPageErrors             map[int]string
	pdfRestoreIndex           int
	pdfRestoreOffset          int
	pdfRestoreAnchor          bool
	pdfRestoreOffsetWait      bool
	pdfRestoreScrollRequested bool
	pdfPagePosition           int
	pdfPageOffset             int
	pdfPositionReady          bool
	pdfRenderTraceMu          sync.Mutex
	pdfRenderTraces           map[string]pdfPageRenderJobTrace

	imageExpanded           bool
	imageExpandedBitmap     *ui.Bitmap
	imageExpandedLoading    bool
	imageExpandedError      string
	imageExpandedCancel     context.CancelFunc
	imageExpandedGeneration uint64
	imageZoom               float32
	imageOffsetX            float32
	imageOffsetY            float32
	imageDragging           bool
	imageDragX              float32
	imageDragY              float32
	imagePressX             float32
	imagePressY             float32
	imagePressOutside       bool
	imageMoved              bool
	imageViewportWidth      float32
	imageViewportHeight     float32
}

type pdfTextMatch struct {
	page       int
	start, end int
	text       string
}

func (m *nativeMediaState) task(run func(context.Context)) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return false
	}
	ctx := m.ctx
	m.wg.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.wg.Done()
		run(ctx)
	}()
	return true
}

func (s *app) openNativeMedia(path, kind string) {
	if path == "" {
		s.nativeMedia = &nativeMediaState{path: s.documentPath, kind: kind, previewError: "后端未返回经过校验的文件路径，无法打开媒体预览。"}
		return
	}
	// Every accepted document.read is a new disk snapshot, including an
	// explicit reread or window-activation refresh of the same path.
	s.closeNativeMedia()
	ctx, cancel := context.WithCancel(context.Background())
	zoom := 100
	if kind == "pdf" {
		zoom = s.readingZoom()
	}
	m := &nativeMediaState{ctx: ctx, cancel: cancel, path: path, kind: kind, zoom: zoom, fit: true, openLoading: true, pageInput: "1"}
	s.nativeMedia = m
	m.task(func(ctx context.Context) {
		var (
			pdfDoc   *media.PDFDocument
			imageDoc *media.ImageDocument
			err      error
		)
		if kind == "pdf" {
			pdfDoc, err = media.OpenPDF(ctx, path, media.Options{PDFiumPath: nativePDFiumPath()})
		} else {
			imageDoc, err = media.OpenImage(ctx, path, media.Options{})
		}
		if err != nil {
			s.updateNativeMedia(m, func() {
				m.openLoading = false
				if ctx.Err() == nil {
					m.previewError = err.Error()
				}
			})
			return
		}
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			if pdfDoc != nil {
				_ = pdfDoc.Close()
			}
			if imageDoc != nil {
				_ = imageDoc.Close()
			}
			return
		}
		m.pdf, m.image = pdfDoc, imageDoc
		if pdfDoc != nil {
			m.pdfInfo = pdfDoc.Info()
		}
		if imageDoc != nil {
			m.imageInfo = imageDoc.Info()
		}
		m.mu.Unlock()
		s.updateNativeMedia(m, func() {
			m.openLoading = false
			if pdfDoc != nil {
				s.initializePDFReadingPosition(m)
			} else {
				m.pageInput = "1"
			}
		})
	})
}

func nativePDFiumPath() string {
	resources, err := mygo.App.Path(mygo.PathResources)
	if err != nil {
		return ""
	}
	name := "libpdfium.so"
	if runtime.GOOS == "windows" {
		name = "pdfium.dll"
	} else if runtime.GOOS == "darwin" {
		name = "libpdfium.dylib"
	}
	return filepath.Join(resources, "native", name)
}

func (s *app) closeNativeMedia() {
	m := s.nativeMedia
	s.nativeMedia = nil
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	if m.cancel != nil {
		m.cancel()
	}
	for _, cancel := range m.pdfPageRenders {
		cancel()
	}
	if m.imageExpandedCancel != nil {
		m.imageExpandedCancel()
		m.imageExpandedCancel = nil
	}
	if m.renderCancel != nil {
		m.renderCancel()
		m.renderCancel = nil
	}
	m.mu.Unlock()
	go func() {
		m.wg.Wait()
		m.mu.Lock()
		pdfDoc, imageDoc := m.pdf, m.image
		m.pdf, m.image = nil, nil
		m.bitmap = nil
		m.imageExpandedBitmap = nil
		m.pdfPageBitmaps = nil
		m.mu.Unlock()
		if pdfDoc != nil {
			_ = pdfDoc.Close()
		}
		if imageDoc != nil {
			_ = imageDoc.Close()
		}
	}()
}

func (s *app) updateNativeMedia(m *nativeMediaState, update func()) bool {
	win := s.win
	if win == nil || win.IsDestroyed() {
		return false
	}
	win.Update(func() {
		if s.nativeMedia != m || m.closed {
			return
		}
		update()
	})
	return true
}

func (s *app) renderNativeMediaControls(c *ui.Context, p palette) {
	m := s.nativeMedia
	if m == nil {
		ui.Row(c).Height(38).Padding(0, 14).Children(func() {
			label(c, "正在准备本地预览…", 12, p.muted)
		})
		return
	}
	ui.Row(c).Height(42).Shrink(0).Padding(0, 12).Gap(6).Background(p.surface).Border(1, p.line).BorderWidth(0, 0, 1, 0).AlignItems(ui.Center).Children(func() {
		if m.kind == "pdf" {
			s.renderPDFControls(c, p, m)
			return
		}
		s.renderImageControls(c, p, m)
	})
}

func (s *app) renderPDFControls(c *ui.Context, p palette, m *nativeMediaState) {
	pageCount := m.pdfInfo.PageCount
	if miniButton(c, p, "pdf-prev", "chevron-left", "上一页", false).Clicked() && pageCount > 0 && m.page > 0 {
		s.navigatePDFPage(m, m.page-1)
	}
	if pageCount > 0 {
		label(c, fmt.Sprintf("第 %d / %d 页", m.page+1, pageCount), 12, p.text).NoWrap()
	} else {
		label(c, "PDF", 12, p.text)
	}
	if miniButton(c, p, "pdf-next", "chevron", "下一页", false).Clicked() && m.page+1 < pageCount {
		s.navigatePDFPage(m, m.page+1)
	}
	if action(c, "pdf-go", "arrow", "跳转", p, false) {
		var requested int
		if _, err := fmt.Sscanf(m.pageInput, "%d", &requested); err == nil && requested >= 1 && requested <= pageCount {
			s.navigatePDFPage(m, requested-1)
		}
	}
	ui.TextInput(c.Key("pdf-page-input"), &m.pageInput).Width(48).Height(28).Placeholder("页")
	ui.Box(c).Width(1).Height(20).Background(p.line)
	if miniButton(c, p, "pdf-zoom-out", "minus", "缩小", false).Clicked() {
		s.changePDFZoom(m, m.zoom-25)
	}
	label(c, fmt.Sprintf("%d%%", m.zoom), 11, p.muted).Width(42).NoWrap()
	if miniButton(c, p, "pdf-zoom-in", "plus", "放大", false).Clicked() {
		s.changePDFZoom(m, m.zoom+25)
	}
	if action(c, "pdf-fit", "expand", "适合宽度", p, false) {
		s.changePDFZoom(m, 100)
	}
	ui.Spacer(c)
	miniButton(c, p, "pdf-outline", "list", "文档目录", false).Menu(func(menu *ui.Menu) {
		if len(m.pdfInfo.Outline) == 0 {
			menu.Item("此 PDF 没有目录")
			return
		}
		for _, entry := range m.pdfInfo.Outline {
			name := strings.Repeat("　", maxInt(entry.Depth, 0)) + entry.Title
			if menu.Item(name).Chosen() {
				s.navigatePDFPage(m, entry.PageIndex)
			}
		}
	})
	if action(c, "pdf-text-toggle", "file", map[bool]string{true: "隐藏文本", false: "查找文本"}[m.showPDFText], p, m.showPDFText) {
		m.showPDFText = !m.showPDFText
		if m.showPDFText {
			s.openDocumentFind(c)
			s.requestPDFTextIndex(m)
		}
	}
}

func (s *app) renderImageControls(c *ui.Context, p palette, m *nativeMediaState) {
	images := s.imageDocuments()
	selected := -1
	for i, path := range images {
		if path == m.path {
			selected = i
			break
		}
	}
	if miniButton(c, p, "image-prev", "chevron-left", "上一张图片", false).Clicked() && selected > 0 {
		s.selectDocument(images[selected-1])
	}
	if selected >= 0 {
		label(c, fmt.Sprintf("%d / %d", selected+1, len(images)), 11, p.muted).NoWrap()
	}
	if miniButton(c, p, "image-next", "chevron", "下一张图片", false).Clicked() && selected >= 0 && selected+1 < len(images) {
		s.selectDocument(images[selected+1])
	}
	ui.Box(c).Width(1).Height(20).Background(p.line)
	if action(c, "image-fit", "expand", "适合窗口", p, m.fit) {
		m.fit = true
		m.bitmap = nil
		m.wantedRenderKey = ""
	}
	if action(c, "image-actual", "image", "原始大小", p, !m.fit && m.zoom == 100) {
		m.fit = false
		m.zoom = 100
		m.bitmap = nil
		m.wantedRenderKey = ""
	}
	if miniButton(c, p, "image-zoom-out", "minus", "缩小", false).Clicked() {
		if m.fit {
			m.fit = false
			m.zoom = 100
		}
		m.zoom = maxInt(m.zoom-10, 10)
		m.bitmap = nil
		m.wantedRenderKey = ""
	}
	if miniButton(c, p, "image-zoom-in", "plus", "放大", false).Clicked() {
		if m.fit {
			m.fit = false
			m.zoom = 100
		}
		m.zoom = minInt(m.zoom+10, 400)
		m.bitmap = nil
		m.wantedRenderKey = ""
	}
	zoom := m.zoom
	if m.fit {
		zoom = s.fitImageZoom(m)
	}
	label(c, fmt.Sprintf("%d%%", zoom), 11, p.muted).Width(42).NoWrap()
	if action(c, "image-expand", "expand", "放大查看图片", p, false) {
		s.openNativeImageViewer(m)
	}
	ui.Spacer(c)
	label(c, fmt.Sprintf("%d × %d · %s", m.imageInfo.Width, m.imageInfo.Height, strings.ToUpper(m.imageInfo.Format)), 11, p.muted).NoWrap()
}

func (s *app) renderNativeMedia(c *ui.Context, p palette, viewport ui.Rect) {
	m := s.nativeMedia
	if m == nil {
		label(c, "正在准备本地预览…", 13, p.muted)
		return
	}
	if m.previewError != "" {
		ui.Column(c).FillWidth().Padding(18).Gap(8).Background(p.amberBg).Border(1, p.line).Radius(10).Children(func() {
			label(c, "无法预览此文件", 14, p.amber).FontWeight(600)
			label(c, m.previewError, 12, p.text).Wrap()
			if action(c, "media-open-external", "arrow", "使用系统阅读器打开", p, false) {
				s.act("document.open", s.currentTask().id, "", "", s.documentPath, "", nil)
			}
		})
		return
	}
	if m.openLoading {
		label(c, "正在读取并解码文件…", 13, p.muted)
		return
	}
	width, height := viewport.W, viewport.H
	if width <= 0 || height <= 0 {
		fallbackWidth, fallbackHeight := c.Size()
		if width <= 0 {
			width = fallbackWidth
		}
		if height <= 0 {
			height = fallbackHeight
		}
	}
	m.viewerWidth, m.viewerHeight = width, height
	if m.kind == "pdf" {
		s.renderContinuousPDF(c, p, m, width, height)
		return
	}
	if m.bitmap == nil || m.bitmapKey != m.wantedRenderKey {
		s.requestNativeMediaRender(m, width, height)
		ui.Column(c).FillWidth().Height(220).Center().Gap(9).Children(func() {
			label(c, "正在生成页面图像…", 13, p.muted)
			if m.kind == "pdf" {
				label(c, fmt.Sprintf("PDF 第 %d 页", m.page+1), 11, p.muted)
			}
		})
		return
	}
	if width <= 0 {
		width = 1000
	}
	if height <= 0 {
		height = 700
	}
	s.renderImageCanvas(c, p, m, width, height)
	s.renderNativeImageViewer(c, p, m)
}

func (s *app) renderImageCanvas(c *ui.Context, p palette, m *nativeMediaState, width, height float32) {
	scale := float32(m.zoom) / 100
	if m.fit {
		scale = float32(s.fitImageZoom(m)) / 100
	}
	if scale <= 0 {
		scale = 1
	}
	displayW := float32(m.imageInfo.Width) * scale
	displayH := float32(m.imageInfo.Height) * scale
	if displayW < 1 || displayH < 1 {
		label(c, "图片尺寸无效。", 12, p.amber)
		return
	}
	ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).HandleInput(func(event ui.InputEvent) bool {
		if event.Kind == ui.InputScroll && event.Mods&ui.Cmd != 0 {
			m.fit = false
			if event.DY > 0 {
				m.zoom = minInt(m.zoom+10, 400)
			} else if event.DY < 0 {
				m.zoom = maxInt(m.zoom-10, 10)
			}
			m.bitmap = nil
			m.wantedRenderKey = ""
			return true
		}
		if event.Kind == ui.InputKeyDown && event.Mods&ui.Cmd != 0 {
			switch event.Key {
			case ui.KeyMinus:
				m.fit = false
				m.zoom = maxInt(m.zoom-10, 10)
			case ui.KeyEqual:
				m.fit = false
				m.zoom = minInt(m.zoom+10, 400)
			case ui.Key0:
				m.fit = true
			}
			m.bitmap = nil
			m.wantedRenderKey = ""
			return true
		}
		return false
	}).Children(func() {
		ui.Box(c).Width(maxFloat32(displayW, width)).Height(maxFloat32(displayH, height)).Center().Children(func() {
			ui.Image(c, m.bitmap).Width(displayW).Height(displayH)
		})
	})
}

func (s *app) requestPDFTextIndex(m *nativeMediaState) {
	if m == nil || m.kind != "pdf" || m.textIndex != nil || m.textLoading {
		return
	}
	m.mu.Lock()
	if m.closed || m.pdf == nil {
		m.mu.Unlock()
		return
	}
	m.textLoading = true
	m.textError = ""
	m.wg.Add(1)
	pdfDoc, ctx := m.pdf, m.ctx
	m.mu.Unlock()
	go func() {
		defer m.wg.Done()
		index, err := pdfDoc.TextIndex(ctx)
		s.updateNativeMedia(m, func() {
			m.textLoading = false
			if err != nil {
				if ctx.Err() == nil {
					m.textError = err.Error()
				}
				return
			}
			m.textIndex = &index
			m.searchKey = ""
			if m.search != "" {
				s.updatePDFSearch(m)
			}
		})
	}()
}

func (s *app) renderPDFText(c *ui.Context, p palette, m *nativeMediaState) {
	ui.Column(c).FillWidth().Padding(12).Gap(8).Background(p.soft).Border(1, p.line).Radius(10).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			input := ui.TextInput(c.Key("pdf-text-search"), &m.search).Grow(1).Height(32).Placeholder("查找 PDF 文本")
			input.OnChange(func() { s.changePDFDocumentFindQuery(m, m.search) })
			input.OnSubmit(func() { s.movePDFDocumentFind(m, 1) })
			input.OnShortcut(0, ui.KeyEscape, func() { s.closeDocumentFind() })
			if action(c, "pdf-search", "search", "查找", p, false) {
				s.changePDFDocumentFindQuery(m, m.search)
			}
			if action(c, "pdf-match-prev", "chevron-left", "上一个匹配", p, false) && len(m.searchResults) > 0 {
				s.movePDFDocumentFind(m, -1)
			}
			if action(c, "pdf-match-next", "chevron", "下一个匹配", p, false) && len(m.searchResults) > 0 {
				s.movePDFDocumentFind(m, 1)
			}
			if len(m.searchResults) > 0 {
				match := m.searchResults[m.searchResult]
				label(c, fmt.Sprintf("%d / %d · 第 %d 页", m.searchResult+1, len(m.searchResults), match.page+1), 11, p.muted).NoWrap()
				if action(c, "pdf-search-go", "arrow", "跳转到匹配", p, false) {
					s.restorePDFSearchMatch(m, match.page)
				}
			}
		})
		if m.textLoading {
			label(c, "正在提取 PDF 文本…", 11, p.muted)
		} else if m.textError != "" {
			label(c, m.textError, 11, p.amber).Wrap()
			if action(c, "pdf-text-retry", "refresh", "重试读取文本", p, false) {
				s.requestPDFTextIndex(m)
			}
		} else if m.textIndex != nil && m.page >= 0 && m.page < len(m.textIndex.Pages) {
			text := m.textIndex.Pages[m.page].Text
			if text == "" {
				label(c, "此页没有可搜索的文本层。", 11, p.muted)
			} else {
				ui.Row(c).Gap(8).Children(func() {
					copyLabel := "复制本页文本"
					copyText := text
					if selected := selectedPDFText(m); selected != "" {
						copyLabel = "复制所选文本"
						copyText = selected
						label(c, fmt.Sprintf("已选 %d 个字符", len([]rune(selected))), 11, p.blue)
					}
					if action(c, "pdf-copy-page-text", "copy", copyLabel, p, false) {
						s.copyTextToClipboard(c.Services(), copyText, "")
					}
					label(c, "在页面文字上拖动选择；扫描页需要 OCR 文本层才可搜索。", 11, p.muted).Wrap()
				})
				ui.Scroll(c).FillWidth().Height(132).Padding(8).Background(p.surface).Border(1, p.line).Radius(8).Children(func() {
					label(c, text, 12, p.text).Font("Consolas").LineHeight(1.55).Wrap().Selectable()
				})
			}
		}
	})
}

func (s *app) updatePDFSearch(m *nativeMediaState) {
	if m == nil || m.closed {
		return
	}
	if m.materialsHit != nil && m.materialsHit.Query != m.search {
		m.materialsHit = nil
	}
	if m.textIndex == nil {
		if pdfTextIndexNeedsRead(m) {
			s.requestPDFTextIndex(m)
		}
		return
	}
	m.searchKey = m.search
	m.searchResults = findPDFMatches(*m.textIndex, m.search)
	m.searchResult = 0
	s.applyMaterialsPDFHit(m)
	if len(m.searchResults) > 0 {
		s.restorePDFSearchMatch(m, m.searchResults[m.searchResult].page)
	}
}

func findPDFMatches(index media.TextIndex, query string) []pdfTextMatch {
	if query == "" {
		return nil
	}
	needle := []rune(strings.ToLower(query))
	if len(needle) == 0 {
		return nil
	}
	var matches []pdfTextMatch
	for pageIndex, page := range index.Pages {
		text := []rune(page.Text)
		lower := make([]rune, len(text))
		for i, r := range text {
			lower[i] = unicode.ToLower(r)
		}
		for _, start := range findRuneMatches(lower, needle) {
			matches = append(matches, pdfTextMatch{page: pageIndex, start: start, end: start + len(needle), text: query})
			if len(matches) >= documentFindMatchLimit {
				return matches
			}
		}
	}
	return matches
}

func findRuneMatches(source, needle []rune) []int {
	if len(needle) == 0 || len(needle) > len(source) {
		return nil
	}
	prefix := make([]int, len(needle))
	for i, matched := 1, 0; i < len(needle); i++ {
		for matched > 0 && needle[i] != needle[matched] {
			matched = prefix[matched-1]
		}
		if needle[i] == needle[matched] {
			matched++
		}
		prefix[i] = matched
	}
	var matches []int
	for i, matched := 0, 0; i < len(source); i++ {
		for matched > 0 && source[i] != needle[matched] {
			matched = prefix[matched-1]
		}
		if source[i] == needle[matched] {
			matched++
		}
		if matched == len(needle) {
			matches = append(matches, i-len(needle)+1)
			matched = 0
		}
	}
	return matches
}

func (s *app) drawPDFSearchHighlights(painter *ui.Painter, rect ui.Rect, m *nativeMediaState, pageIndex int) {
	if painter == nil || m == nil || m.textIndex == nil || pageIndex < 0 || pageIndex >= len(m.textIndex.Pages) {
		return
	}
	page := m.textIndex.Pages[pageIndex]
	searchActive := m.searchKey != "" && m.searchKey == m.search
	type pageMatch struct {
		pdfTextMatch
		index int
	}
	var matches []pageMatch
	if searchActive {
		for matchIndex, match := range m.searchResults {
			if match.page == pageIndex {
				matches = append(matches, pageMatch{pdfTextMatch: match, index: matchIndex})
			}
		}
	}
	matchIndex := 0
	for _, glyph := range page.Glyphs {
		for matchIndex < len(matches) && matches[matchIndex].end <= glyph.Start {
			matchIndex++
		}
		if matchIndex >= len(matches) || matches[matchIndex].start >= glyph.End {
			continue
		}
		color := ui.RGBA(255, 205, 76, .42)
		if matches[matchIndex].index == m.searchResult {
			color = ui.RGBA(53, 106, 230, .38)
		}
		left := rect.X + float32(glyph.Left)*rect.W
		top := rect.Y + float32(glyph.Top)*rect.H
		right := rect.X + float32(glyph.Right)*rect.W
		bottom := rect.Y + float32(glyph.Bottom)*rect.H
		if right > left && bottom > top {
			painter.Fill(ui.Rect{X: left, Y: top, W: right - left, H: bottom - top}, color, 1)
		}
	}
	if start, end, selected := m.pdfSelection.rangeForPage(pageIndex, len([]rune(page.Text))); selected {
		for _, glyph := range page.Glyphs {
			if glyph.End <= start || glyph.Start >= end {
				continue
			}
			left := rect.X + float32(glyph.Left)*rect.W
			top := rect.Y + float32(glyph.Top)*rect.H
			right := rect.X + float32(glyph.Right)*rect.W
			bottom := rect.Y + float32(glyph.Bottom)*rect.H
			if right > left && bottom > top {
				painter.Fill(ui.Rect{X: left, Y: top, W: right - left, H: bottom - top}, ui.RGBA(80, 145, 255, .38), 1)
			}
		}
	}
}

func (s *app) requestNativeMediaRender(m *nativeMediaState, viewportWidth, viewportHeight float32) {
	if m == nil || m.openLoading || m.previewError != "" {
		return
	}
	width := int(maxFloat32(viewportWidth-56, 320))
	zoom := m.zoom
	if m.fit {
		zoom = s.fitImageZoom(m)
	}
	if zoom < 100 {
		width = int(float32(m.imageInfo.Width) * float32(zoom) / 100)
	} else {
		width = 0
	}
	if width < 1 {
		width = 1
	}
	key := fmt.Sprintf("%s:%d", m.kind, width)
	if m.wantedRenderKey == key || m.bitmapKey == key {
		return
	}
	m.wantedRenderKey = key
	m.renderLoading = true
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	if m.renderCancel != nil {
		m.renderCancel()
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.renderCancel = cancel
	m.wg.Add(1)
	imageDoc := m.image
	m.mu.Unlock()
	go func() {
		defer m.wg.Done()
		var rendered media.RenderedPage
		var err error
		if imageDoc != nil {
			rendered, err = imageDoc.RenderPNG(ctx, width)
		} else {
			err = fmt.Errorf("媒体文档未打开")
		}
		var bitmap *ui.Bitmap
		if err == nil {
			bitmap, err = ui.DecodeBitmap(rendered.PNG)
		}
		s.updateNativeMedia(m, func() {
			if m.wantedRenderKey != key {
				return
			}
			m.renderLoading = false
			if err != nil {
				if ctx.Err() == nil {
					m.previewError = err.Error()
				}
				return
			}
			m.bitmap = bitmap
			m.bitmapKey = key
		})
	}()
	_ = viewportHeight
}

func (s *app) fitImageZoom(m *nativeMediaState) int {
	if m == nil || m.imageInfo.Width <= 0 || m.imageInfo.Height <= 0 {
		return 100
	}
	w, h := m.viewerWidth-64, m.viewerHeight-96
	if w < 300 {
		w = 900
	}
	if h < 200 {
		h = 620
	}
	scale := minFloat64(float64(w)/float64(m.imageInfo.Width), float64(h)/float64(m.imageInfo.Height))
	if scale > 1 {
		scale = 1
	}
	return maxInt(1, int(math.Round(scale*100)))
}

func (s *app) imageDocuments() []string {
	var paths []string
	var walk func([]application.Document)
	walk = func(items []application.Document) {
		for _, item := range items {
			if item.Kind == "image" || documentKindForPath(item.Path) == "image" {
				paths = append(paths, item.Path)
			}
			walk(item.Children)
		}
	}
	walk(s.documents)
	return paths
}

func documentKindForPath(path string) string {
	return documents.Kind(path)
}

func maxFloat32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func minFloat64(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
