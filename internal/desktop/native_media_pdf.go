package desktop

import (
	"context"
	"fmt"
	"math"

	"silverwing-go/internal/media"

	"github.com/egoist/mygo/ui"
)

const (
	pdfPageVerticalPadding = float32(16)
	pdfPageGap             = float32(16)
	pdfPageRenderCacheSize = 16
)

type pdfPageBitmap struct {
	key    string
	bitmap *ui.Bitmap
}

func (s *app) initializePDFReadingPosition(m *nativeMediaState) {
	if m == nil || m.kind != "pdf" {
		return
	}
	m.page = 0
	m.pageInput = "1"
	m.pdfRestoreIndex, m.pdfRestoreOffset = 0, 0
	m.pdfRestoreAnchor, m.pdfRestoreOffsetWait = false, false
	m.pdfRestoreScrollRequested = false
	m.pdfPositionReady = false
	position, _ := s.currentReadingPosition(false)
	if saved, ok := position.Lists["pdf-pages"]; ok && len(saved) > 0 {
		m.pdfRestoreIndex = clampInt(saved[0], 0, maxInt(0, m.pdfInfo.PageCount-1))
		if len(saved) > 1 {
			m.pdfRestoreOffset = clampInt(saved[1], 0, 100_000_000)
		}
		m.page = m.pdfRestoreIndex
		m.pageInput = fmt.Sprint(m.page + 1)
		m.pdfRestoreAnchor = m.pdfInfo.PageCount > 0
		m.pdfRestoreOffsetWait = false
		m.pdfPagePosition, m.pdfPageOffset = m.pdfRestoreIndex, m.pdfRestoreOffset
		m.pdfPositionReady = true
	}
	if m.pdfInfo.PageCount > 0 {
		m.pdfPageBitmaps = make(map[int]pdfPageBitmap)
		m.pdfPageErrors = make(map[int]string)
		m.pdfRenderTraceMu.Lock()
		if m.pdfRenderTraces == nil {
			m.pdfRenderTraces = make(map[string]pdfPageRenderJobTrace)
		}
		m.pdfRenderTraceMu.Unlock()
	}
}

func (s *app) renderContinuousPDF(c *ui.Context, p palette, m *nativeMediaState, viewportWidth, viewportHeight float32) {
	if m.pdfInfo.PageCount <= 0 {
		label(c, "PDF 中没有可显示的页面。", 12, p.muted)
		return
	}
	if viewportWidth <= 0 {
		viewportWidth = 1000
	}
	if viewportHeight <= 0 {
		viewportHeight = 700
	}
	m.viewerWidth, m.viewerHeight = viewportWidth, viewportHeight
	pageWidth := pdfPageDisplayWidth(viewportWidth, m.zoom)
	m.pdfPageBounds = make(map[int]ui.Rect)
	contentWidth := maxFloat32(viewportWidth, pageWidth+32)
	widthPx := maxInt(1, int(math.Round(float64(pageWidth))))

	first, last := m.pdfPages.Visible()
	layoutHasVisibleRows := last >= first
	if m.pdfRestoreAnchor {
		anchorReached := layoutHasVisibleRows && (first == m.pdfRestoreIndex || m.pdfRestoreScrollRequested && pdfRestorationClampedAtEnd(m, first, last, pageWidth))
		if anchorReached {
			// Visible reports the completed previous layout. Now that it has
			// reached the anchor, apply the saved offset before this frame builds
			// the List, so TrackScroll adopts it into the next layout.
			m.pdfRestoreAnchor = false
			m.pdfRestoreScrollRequested = false
			m.pdfPageScroll.Y = maxFloat32(0, pdfPageTop(m.pdfInfo, m.pdfRestoreIndex, pageWidth)+float32(m.pdfRestoreOffset))
			m.pdfRestoreOffsetWait = true
		} else if !m.pdfRestoreScrollRequested {
			// ScrollTo is consumed by a later layout. Do not enqueue it again on
			// each frame or the saved offset would keep losing to Start alignment.
			m.pdfPages.ScrollTo(m.pdfRestoreIndex, ui.Start)
			m.pdfRestoreScrollRequested = true
		}
	} else if m.pdfRestoreOffsetWait {
		pageTop := pdfPageTop(m.pdfInfo, m.pdfRestoreIndex, pageWidth)
		actualOffset := int(math.Round(float64(m.pdfPageScroll.Y - pageTop)))
		exactOffset := first == m.pdfRestoreIndex && absInt(actualOffset-m.pdfRestoreOffset) <= 4
		if layoutHasVisibleRows && (exactOffset || pdfRestorationClampedAtEnd(m, first, last, pageWidth)) {
			// Only leave restoration after a completed List layout reflects the
			// requested page/offset or its valid clamp at the document's end.
			// Position persistence resumes below.
			m.pdfRestoreOffsetWait = false
			m.pdfRestoreScrollRequested = false
		} else {
			m.pdfPageScroll.Y = maxFloat32(0, pageTop+float32(m.pdfRestoreOffset))
		}
	}
	if !m.pdfRestoreAnchor && !m.pdfRestoreOffsetWait {
		s.syncPDFVisiblePosition(m, viewportWidth)
	}
	if m.showPDFText {
		s.renderPDFText(c, p, m)
	}

	scroll := ui.ScrollHorizontal(c.Key("native-pdf-horizontal")).FillWidth().Grow(1).MinHeight(0).
		TrackScroll(s.readingScrollState("pdf-horizontal")).HandleInput(func(event ui.InputEvent) bool {
		if event.Kind != ui.InputScroll || event.Mods&ui.Ctrl == 0 || event.DY == 0 {
			return false
		}
		step := 25
		if event.DY < 0 {
			step = -25
		}
		s.changePDFZoom(m, m.zoom+step)
		return true
	})
	scroll.Children(func() {
		list := ui.List(c.Key("native-pdf-pages"), &m.pdfPages, m.pdfInfo.PageCount).
			Width(contentWidth).Grow(1).MinHeight(0).AlignSelf(ui.Stretch).Gap(pdfPageGap).Padding(pdfPageVerticalPadding, 0).
			TrackScroll(&m.pdfPageScroll)
		list.Rows(func(row ui.ListRow) {
			index := row.Index
			services := row.Context.Services()
			aspect := pdfPageAspect(m.pdfInfo, index)
			pageHeight := pageWidth / aspect
			if !nativeMediaFinitePositive(pageHeight) {
				pageHeight = pageWidth * float32(math.Sqrt2)
			}
			ui.Box(row.Context).Width(contentWidth).Height(pageHeight).Center().Children(func() {
				bitmap, cached := m.pdfPageBitmaps[index]
				page := ui.Box(row.Context).Width(pageWidth).Height(pageHeight).Background(ui.RGBA(255, 255, 255, 1))
				page.Children(func() {
					if cached && bitmap.key == pdfPageRenderKey(index, widthPx) && bitmap.bitmap != nil {
						image := ui.Image(row.Context, bitmap.bitmap).Width(pageWidth).Height(pageHeight).Focusable().
							HandleInput(func(event ui.InputEvent) bool {
								return s.handlePDFPageInput(m, index, event, pageWidth, pageHeight, s.clipboardTextWriter(services))
							}).ContextMenu(func(menu *ui.Menu) {
							selected := selectedPDFText(m)
							if menu.Item("复制").Disabled(selected == "").Chosen() {
								s.copyTextToClipboard(services, selected, "")
							}
						})
						if m.textIndex != nil && index < len(m.textIndex.Pages) {
							image.DrawOver(func(painter *ui.Painter, rect ui.Rect) {
								m.pdfPageBounds[index] = rect
								s.drawPDFSearchHighlights(painter, rect, m, index)
							})
						}
						return
					}
					if message := pdfPageErrorForWidth(m, index, widthPx); message != "" {
						ui.Column(row.Context).FillWidth().Padding(18).Gap(8).Center().Children(func() {
							label(row.Context, fmt.Sprintf("第 %d 页加载失败", index+1), 12, p.amber).FontWeight(600)
							label(row.Context, message, 11, p.muted).Wrap()
							if action(row.Context, fmt.Sprintf("pdf-page-retry-%d", index), "refresh", "重试此页", p, false) {
								delete(m.pdfPageErrors, index)
								s.requestPDFPageRender(m, index, widthPx)
							}
						})
						return
					}
					s.requestPDFPageRender(m, index, widthPx)
					ui.Column(row.Context).FillWidth().Padding(18).Gap(7).Center().Children(func() {
						label(row.Context, fmt.Sprintf("正在加载第 %d 页…", index+1), 12, p.muted)
					})
				})
			})
		})
	})
	first, last = m.pdfPages.Visible()
	if m.pdfRenders != nil {
		m.pdfRenders.viewport(first, last, widthPx)
	}
	// Queue visible rendering first; page-sized text turns yield to it.
	if s.win != nil && !s.win.IsDestroyed() && pdfTextIndexNeedsRead(m) {
		s.requestPDFTextIndex(m)
	}
	s.prunePDFPageBitmaps(m, first, last)
}

func (s *app) requestPDFPageRender(m *nativeMediaState, pageIndex, widthPx int) {
	if m == nil || m.kind != "pdf" || pageIndex < 0 || pageIndex >= m.pdfInfo.PageCount || widthPx < 1 {
		return
	}
	// NewWindow may draw its first frame before returning to the caller. Wait
	// for its assignment before launching work whose result needs Window.Update.
	window := s.win
	if window == nil || window.IsDestroyed() {
		return
	}
	key := pdfPageRenderKey(pageIndex, widthPx)
	if cached, ok := m.pdfPageBitmaps[pageIndex]; ok && cached.key == key && cached.bitmap != nil {
		return
	}
	m.mu.Lock()
	if m.closed || m.pdf == nil {
		m.mu.Unlock()
		return
	}
	startWorker := m.pdfRenders == nil
	if startWorker {
		m.pdfRenders = newPDFRenderQueue(m.ctx)
		m.wg.Add(1)
	}
	queue, document := m.pdfRenders, m.pdf
	m.mu.Unlock()
	if startWorker {
		go func() { defer m.wg.Done(); queue.run() }()
	}
	queue.enqueue(pageIndex, widthPx, func(ctx context.Context, request *pdfPageRenderRequest) {
		recordPDFPageRenderStage(m, key, pageIndex, widthPx, "render-start", nil)
		rendered, err := document.RenderPageRGBA(ctx, pageIndex, widthPx)
		recordPDFPageRenderStage(m, key, pageIndex, widthPx, "render-return", err)
		var bitmap *ui.Bitmap
		if err == nil {
			recordPDFPageRenderStage(m, key, pageIndex, widthPx, "decode-start", nil)
			bitmap = ui.NewBitmap(rendered.RGBA)
			recordPDFPageRenderStage(m, key, pageIndex, widthPx, "decode-return", err)
		}
		cancelled := ctx.Err() != nil
		recordPDFPageRenderStage(m, key, pageIndex, widthPx, "update-post", err)
		posted := s.updateNativeMedia(m, func() {
			recordPDFPageRenderStage(m, key, pageIndex, widthPx, "update-closure-enter", nil)
			queue.ack(pageIndex, request)
			// Cancellation can happen after this result was queued. Recheck the
			// request, window and current layout before publishing either a
			// bitmap or an error for the page.
			if !request.accepts(s, m, window, pageIndex, widthPx, cancelled) {
				recordPDFPageRenderStage(m, key, pageIndex, widthPx, "update-stale", nil)
				return
			}
			if err != nil {
				if m.pdfPageErrors == nil {
					m.pdfPageErrors = make(map[int]string)
				}
				if m.pdfPageBitmaps == nil {
					m.pdfPageBitmaps = make(map[int]pdfPageBitmap)
				}
				// A page error belongs to this render width, just like its bitmap.
				// Resizing must be able to request a fresh render for the new size.
				m.pdfPageBitmaps[pageIndex] = pdfPageBitmap{key: key}
				m.pdfPageErrors[pageIndex] = err.Error()
				recordPDFPageRenderStage(m, key, pageIndex, widthPx, "update-applied-error", err)
				return
			}
			if m.pdfPageBitmaps == nil {
				m.pdfPageBitmaps = make(map[int]pdfPageBitmap)
			}
			m.pdfPageBitmaps[pageIndex] = pdfPageBitmap{key: key, bitmap: bitmap}
			delete(m.pdfPageErrors, pageIndex)
			recordPDFPageRenderStage(m, key, pageIndex, widthPx, "update-applied-bitmap", nil)
		})
		if !posted {
			queue.ack(pageIndex, request)
			recordPDFPageRenderStage(m, key, pageIndex, widthPx, "update-window-unavailable", nil)
		}
	})
}

func (s *app) cancelPDFPageRenders(m *nativeMediaState) {
	if m == nil {
		return
	}
	m.pdfRenders.invalidateAll()
}

func (s *app) prunePDFPageBitmaps(m *nativeMediaState, first, last int) {
	if m == nil || len(m.pdfPageBitmaps) <= pdfPageRenderCacheSize {
		return
	}
	if last < first {
		first, last = maxInt(0, m.page-2), minInt(m.pdfInfo.PageCount-1, m.page+2)
	}
	first, last = maxInt(0, first-2), minInt(m.pdfInfo.PageCount-1, last+2)
	for index := range m.pdfPageBitmaps {
		if index < first || index > last {
			delete(m.pdfPageBitmaps, index)
		}
	}
	for index := range m.pdfPageErrors {
		if index < first || index > last {
			delete(m.pdfPageErrors, index)
		}
	}
}

func (s *app) syncPDFVisiblePosition(m *nativeMediaState, viewportWidth float32) {
	if m == nil || m.pdfInfo.PageCount == 0 {
		return
	}
	first, last := m.pdfPages.Visible()
	if first < 0 || first >= m.pdfInfo.PageCount || last < first {
		return
	}
	page := first
	if m.pdfPages.AtEnd() && m.pdfPageScroll.Y > 0 {
		page = m.pdfInfo.PageCount - 1
	}
	page = clampInt(page, 0, m.pdfInfo.PageCount-1)
	if m.page != page {
		m.page = page
		m.pageInput = fmt.Sprint(page + 1)
	}
	width := pdfPageDisplayWidth(viewportWidth, m.zoom)
	offset := int(math.Round(float64(m.pdfPageScroll.Y - pdfPageTop(m.pdfInfo, first, width))))
	if offset < 0 {
		offset = 0
	}
	if m.pdfPositionReady && m.pdfPagePosition == first && m.pdfPageOffset == offset {
		return
	}
	position, save := s.currentReadingPosition(true)
	if save == nil {
		return
	}
	position.SetListPosition("pdf-pages", first, offset)
	save(position)
	m.pdfPagePosition, m.pdfPageOffset = first, offset
	m.pdfPositionReady = true
	s.observeReadingSession()
}

func (s *app) navigatePDFPage(m *nativeMediaState, page int) {
	if m == nil || m.pdfInfo.PageCount <= 0 {
		return
	}
	page = clampInt(page, 0, m.pdfInfo.PageCount-1)
	m.page = page
	m.pageInput = fmt.Sprint(page + 1)
	m.pdfRestoreIndex, m.pdfRestoreOffset = page, 0
	m.pdfRestoreAnchor, m.pdfRestoreOffsetWait = true, false
	m.pdfRestoreScrollRequested = false
}

func (s *app) changePDFZoom(m *nativeMediaState, requested int) {
	if m == nil || m.kind != "pdf" {
		return
	}
	requested = clampInt(requested, 25, 200)
	if requested == m.zoom {
		return
	}
	page := m.page
	first, _ := m.pdfPages.Visible()
	if m.pdfPages.AtEnd() && m.pdfPageScroll.Y > 0 {
		page = m.pdfInfo.PageCount - 1
	} else if first >= 0 && first < m.pdfInfo.PageCount {
		page = first
	}
	viewportWidth := m.viewerWidth
	oldWidth := pdfPageDisplayWidth(viewportWidth, m.zoom)
	pageOffset := float32(0)
	if page == first {
		pageOffset = m.pdfPageScroll.Y - pdfPageTop(m.pdfInfo, page, oldWidth)
	}
	if pageOffset < 0 {
		pageOffset = 0
	}
	m.pdfRestoreIndex = clampInt(page, 0, maxInt(0, m.pdfInfo.PageCount-1))
	m.pdfRestoreOffset = int(math.Round(float64(pageOffset) * float64(requested) / float64(maxInt(1, m.zoom))))
	m.pdfRestoreAnchor = true
	m.pdfRestoreOffsetWait = false
	m.pdfRestoreScrollRequested = false
	m.zoom = requested
	m.pdfPageBitmaps = make(map[int]pdfPageBitmap)
	m.pdfPageErrors = make(map[int]string)
	s.cancelPDFPageRenders(m)
	s.setReadingZoom(requested)
}

func (s *app) restorePDFSearchMatch(m *nativeMediaState, page int) {
	if m == nil || m.pdfInfo.PageCount == 0 {
		return
	}
	s.navigatePDFPage(m, page)
	if m.searchKey != m.search || m.searchResult < 0 || m.searchResult >= len(m.searchResults) {
		return
	}
	match := m.searchResults[m.searchResult]
	if match.page != m.page {
		return
	}
	glyph, ok := pdfSearchMatchGlyph(m.textIndex, match)
	if !ok {
		return
	}
	pageWidth := pdfPageDisplayWidth(m.viewerWidth, m.zoom)
	pageHeight := pageWidth / pdfPageAspect(m.pdfInfo, m.page)
	m.pdfRestoreOffset = int(math.Round(float64(pageHeight * glyph.Top)))
	if horizontal := s.readingScrollState("pdf-horizontal"); horizontal != nil {
		contentWidth := maxFloat32(m.viewerWidth, pageWidth+32)
		left := (contentWidth-pageWidth)/2 + glyph.Left*pageWidth
		right := (contentWidth-pageWidth)/2 + glyph.Right*pageWidth
		horizontal.X = pdfFindHorizontalOffset(left, right, horizontal.X, m.viewerWidth)
	}
}

func pdfTextIndexNeedsRead(m *nativeMediaState) bool {
	return m != nil && m.kind == "pdf" && !m.closed && !m.openLoading && m.textIndex == nil && !m.textLoading && m.textError == ""
}

func pdfSearchMatchGlyph(index *media.TextIndex, match pdfTextMatch) (media.Glyph, bool) {
	if index == nil || match.page < 0 || match.page >= len(index.Pages) || match.end <= match.start {
		return media.Glyph{}, false
	}
	for _, glyph := range index.Pages[match.page].Glyphs {
		if glyph.Start < match.end && glyph.End > match.start {
			return glyph, true
		}
	}
	return media.Glyph{}, false
}

func pdfFindHorizontalOffset(left, right, current, viewportWidth float32) float32 {
	switch {
	case left < current:
		return maxFloat32(0, float32(math.Round(float64(left-16))))
	case right > current+viewportWidth:
		return maxFloat32(0, float32(math.Round(float64(right-viewportWidth+16))))
	default:
		return current
	}
}

func pdfRestorationClampedAtEnd(m *nativeMediaState, first, last int, pageWidth float32) bool {
	if !m.pdfPages.AtEnd() || first > m.pdfRestoreIndex || last < m.pdfRestoreIndex {
		return false
	}
	requestedY := pdfPageTop(m.pdfInfo, m.pdfRestoreIndex, pageWidth) + float32(m.pdfRestoreOffset)
	return m.pdfPageScroll.Y <= requestedY+4
}

func pdfPageRenderKey(pageIndex, widthPx int) string {
	return fmt.Sprintf("pdf:%d:%d", pageIndex, widthPx)
}

func pdfPageErrorForWidth(m *nativeMediaState, pageIndex, widthPx int) string {
	if m == nil || m.pdfPageBitmaps[pageIndex].key != pdfPageRenderKey(pageIndex, widthPx) {
		return ""
	}
	return m.pdfPageErrors[pageIndex]
}

func pdfPageDisplayWidth(viewportWidth float32, zoom int) float32 {
	fitWidth := maxFloat32(64, viewportWidth-32)
	width := fitWidth * float32(clampInt(zoom, 25, 200)) / 100
	if width < 1 || math.IsNaN(float64(width)) || math.IsInf(float64(width), 0) {
		return 64
	}
	return width
}

func pdfPageAspect(info media.DocumentInfo, index int) float32 {
	if index >= 0 && index < len(info.Pages) {
		aspect := info.Pages[index].AspectRatio
		if aspect > 0 && !math.IsNaN(aspect) && !math.IsInf(aspect, 0) {
			return float32(aspect)
		}
	}
	return float32(1 / math.Sqrt2)
}

func pdfPageTop(info media.DocumentInfo, pageIndex int, pageWidth float32) float32 {
	pageIndex = clampInt(pageIndex, 0, maxInt(0, info.PageCount))
	top := pdfPageVerticalPadding
	for index := 0; index < pageIndex; index++ {
		top += pageWidth/pdfPageAspect(info, index) + pdfPageGap
	}
	return top
}

func nativeMediaFinitePositive(value float32) bool {
	return value > 0 && !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0)
}
