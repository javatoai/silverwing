package desktop

import (
	"context"
	"math"

	"github.com/egoist/mygo/ui"
)

func (s *app) openNativeImageViewer(m *nativeMediaState) {
	if m == nil || m.kind != "image" {
		return
	}
	m.imageExpanded = true
	m.imageZoom = 1
	m.imageOffsetX, m.imageOffsetY = 0, 0
	m.imageDragging = false
	if m.imageExpandedBitmap != nil || m.imageExpandedLoading {
		return
	}
	s.requestNativeImageViewerBitmap(m)
}

func (s *app) requestNativeImageViewerBitmap(m *nativeMediaState) {
	if viewer := s.markdownBitmapViewer; viewer != nil && viewer.media == m && viewer.sourcePath != "" {
		s.requestMarkdownLocalImageViewer(viewer)
		return
	}
	if m == nil || m.kind != "image" {
		return
	}
	m.mu.Lock()
	if m.closed || m.image == nil || m.imageExpandedLoading {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.imageExpandedCancel = cancel
	m.imageExpandedGeneration++
	generation := m.imageExpandedGeneration
	m.imageExpandedLoading = true
	m.imageExpandedError = ""
	m.wg.Add(1)
	document := m.image
	m.mu.Unlock()
	go func() {
		defer m.wg.Done()
		rendered, err := document.RenderPNG(ctx, 0)
		var bitmap *ui.Bitmap
		if err == nil {
			bitmap, err = ui.DecodeBitmap(rendered.PNG)
		}
		cancelled := ctx.Err() != nil
		cancel()
		s.updateNativeMedia(m, func() {
			if m.imageExpandedGeneration != generation {
				return
			}
			m.imageExpandedLoading = false
			m.imageExpandedCancel = nil
			if cancelled || !m.imageExpanded {
				return
			}
			if err != nil {
				m.imageExpandedError = err.Error()
				return
			}
			m.imageExpandedBitmap = bitmap
		})
	}()
}

func (s *app) closeNativeImageViewer(m *nativeMediaState) {
	if m == nil {
		return
	}
	m.imageExpandedGeneration++
	if m.imageExpandedCancel != nil {
		m.imageExpandedCancel()
		m.imageExpandedCancel = nil
	}
	m.imageExpanded = false
	m.imageExpandedLoading = false
	m.imageExpandedBitmap = nil
	m.imageExpandedError = ""
	m.imageDragging = false
	m.imageOffsetX, m.imageOffsetY = 0, 0
}

func (s *app) renderNativeImageViewer(c *ui.Context, p palette, m *nativeMediaState) {
	if m == nil || m.kind != "image" {
		return
	}
	dialog := ui.DialogBase(c.Key("native-image-viewer"), &m.imageExpanded, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .72))
		windowWidth, windowHeight := c.Size()
		panel.Width(maxFloat32(1, windowWidth)).Height(maxFloat32(1, windowHeight)).Padding(16).Clip().Label("图片查看器")
		// The panel's padding is still backdrop space. Keep the same drag
		// threshold there while leaving canvas and close-button input alone.
		panel.HandleInput(func(event ui.InputEvent) bool {
			if event.Kind == ui.InputPointerDown && event.X >= 16 && event.Y >= 16 && event.X < windowWidth-16 && event.Y < windowHeight-16 {
				return false
			}
			if event.Kind != ui.InputPointerDown && !m.imageDragging {
				return false
			}
			event.X, event.Y = event.X-16, event.Y-16
			return s.handleNativeImageViewerInput(m, event)
		})
		panel.OnShortcut(0, ui.KeyEscape, func() { m.imageExpanded = false })
		panel.OnShortcut(0, ui.KeyEqual, func() { s.scaleNativeImageViewer(m, 1.25) })
		panel.OnShortcut(0, ui.KeyMinus, func() { s.scaleNativeImageViewer(m, 1/1.25) })
		panel.OnShortcut(0, ui.Key0, func() { s.fitNativeImageViewer(m) })
		panel.OnShortcut(0, ui.Key1, func() { s.originalNativeImageViewer(m) })
		panel.Children(func() {
			canvas := ui.Box(c.Key("native-image-canvas")).Fill().MinHeight(0).Clip().Focusable().Label("图片画布")
			canvas.HandleInput(func(event ui.InputEvent) bool {
				return s.handleNativeImageViewerInput(m, event)
			})
			canvas.Draw(func(painter *ui.Painter, viewport ui.Rect) {
				changed := m.imageViewportWidth != viewport.W || m.imageViewportHeight != viewport.H
				previousWidth, previousHeight := m.imageViewportWidth, m.imageViewportHeight
				m.imageViewportWidth, m.imageViewportHeight = viewport.W, viewport.H
				if changed {
					c.Invalidate()
				}
				bitmap := m.imageExpandedBitmap
				if bitmap == nil || m.imageExpandedLoading || m.imageExpandedError != "" {
					return
				}
				imageWidth, imageHeight := bitmap.Size()
				fit := nativeImageFitScale(float32(imageWidth), float32(imageHeight), viewport.W, viewport.H)
				if changed && previousWidth > 0 && previousHeight > 0 && m.imageZoom != 1 {
					previousFit := nativeImageFitScale(float32(imageWidth), float32(imageHeight), previousWidth, previousHeight)
					m.imageZoom *= previousFit / fit
				}
				m.imageZoom = boundNativeImageZoom(m.imageZoom, fit)
				m.imageOffsetX, m.imageOffsetY = boundNativeImageOffset(m.imageOffsetX, m.imageOffsetY,
					float32(imageWidth), float32(imageHeight), fit, m.imageZoom, viewport.W, viewport.H)
				displayWidth := float32(imageWidth) * fit * m.imageZoom
				displayHeight := float32(imageHeight) * fit * m.imageZoom
				bounds := ui.Rect{
					X: viewport.X + (viewport.W-displayWidth)/2 + m.imageOffsetX,
					Y: viewport.Y + (viewport.H-displayHeight)/2 + m.imageOffsetY,
					W: displayWidth, H: displayHeight,
				}
				// Element.Clip clips children, not its own Draw callback.
				painter.Clip(viewport, 0, func() { painter.Image(bitmap, bounds, ui.Contain) })
			})
			canvas.Children(func() {
				if m.imageExpandedLoading {
					ui.Column(c).FillWidth().Center().Children(func() {
						label(c, "正在读取图片…", 12, ui.Hex("#FFFFFF"))
					})
					return
				}
				if m.imageExpandedError != "" {
					ui.Column(c).FillWidth().Padding(16).Center().Gap(8).Children(func() {
						label(c, m.imageExpandedError, 12, ui.Hex("#FFFFFF")).Wrap()
						if ui.Button(c.Key("image-viewer-retry"), "重试读取").HandleInput(func(ui.InputEvent) bool { return false }).Clicked() {
							s.requestNativeImageViewerBitmap(m)
						}
					})
					return
				}
				bitmap := m.imageExpandedBitmap
				if bitmap == nil {
					label(c, "图片尚未准备完成。", 12, ui.Hex("#FFFFFF"))
					return
				}
			})
			close := ui.ButtonBase(c.Key("image-viewer-close")).Absolute().Right(12).Top(12).Size(34, 34).Center().Radius(17).
				Background(ui.RGBA(0, 0, 0, .28)).Label("关闭图片").Tooltip("关闭图片 · Esc")
			close.Children(func() { icon(c, "close", 17, ui.Hex("#FFFFFF")) })
			if close.Clicked() {
				m.imageExpanded = false
			}
		})
	})
	if dialog.Dismissed() || !m.imageExpanded {
		if m.imageExpandedBitmap != nil || m.imageExpandedLoading || m.imageExpandedError != "" {
			s.closeNativeImageViewer(m)
		}
	}
}

func (s *app) handleNativeImageViewerInput(m *nativeMediaState, event ui.InputEvent) bool {
	if m == nil || !m.imageExpanded {
		return false
	}
	fit := nativeImageFitScale(float32(m.imageInfo.Width), float32(m.imageInfo.Height), m.imageViewportWidth, m.imageViewportHeight)
	zoom := boundNativeImageZoom(m.imageZoom, fit)
	imageContains := func(x, y float32) bool {
		if m.imageExpandedBitmap == nil {
			return false
		}
		width, height := float32(m.imageInfo.Width)*fit*zoom, float32(m.imageInfo.Height)*fit*zoom
		left, top := (m.imageViewportWidth-width)/2+m.imageOffsetX, (m.imageViewportHeight-height)/2+m.imageOffsetY
		return x >= 0 && y >= 0 && x < m.imageViewportWidth && y < m.imageViewportHeight &&
			x >= left && y >= top && x < left+width && y < top+height
	}
	switch event.Kind {
	case ui.InputPointerDown:
		if event.Button != 0 {
			return false
		}
		m.imageDragging = true
		m.imageDragX, m.imageDragY = event.X, event.Y
		m.imagePressX, m.imagePressY = event.X, event.Y
		m.imagePressOutside, m.imageMoved = !imageContains(event.X, event.Y), false
		return true
	case ui.InputPointerMove:
		if !m.imageDragging {
			return false
		}
		m.imageMoved = m.imageMoved || math.Hypot(float64(event.X-m.imagePressX), float64(event.Y-m.imagePressY)) >= 4
		if m.imagePressOutside {
			return true
		}
		m.imageOffsetX += event.X - m.imageDragX
		m.imageOffsetY += event.Y - m.imageDragY
		m.imageDragX, m.imageDragY = event.X, event.Y
		m.imageOffsetX, m.imageOffsetY = boundNativeImageOffset(m.imageOffsetX, m.imageOffsetY,
			float32(m.imageInfo.Width), float32(m.imageInfo.Height), fit, zoom, m.imageViewportWidth, m.imageViewportHeight)
		return true
	case ui.InputPointerUp:
		if !m.imageDragging {
			return false
		}
		m.imageDragging = false
		if m.imagePressOutside && !m.imageMoved && !imageContains(event.X, event.Y) {
			m.imageExpanded = false
		}
		return true
	case ui.InputScroll:
		if m.imageExpandedBitmap == nil {
			return true
		}
		if event.DY == 0 {
			return false
		}
		factor := float32(1.15)
		if event.DY > 0 {
			factor = 1 / factor
		}
		next := boundNativeImageZoom(zoom*factor, fit)
		if zoom > 0 {
			pointX, pointY := event.X-m.imageViewportWidth/2, event.Y-m.imageViewportHeight/2
			m.imageOffsetX = pointX + (m.imageOffsetX-pointX)*(next/zoom)
			m.imageOffsetY = pointY + (m.imageOffsetY-pointY)*(next/zoom)
		}
		m.imageZoom = next
		m.imageOffsetX, m.imageOffsetY = boundNativeImageOffset(m.imageOffsetX, m.imageOffsetY,
			float32(m.imageInfo.Width), float32(m.imageInfo.Height), fit, next, m.imageViewportWidth, m.imageViewportHeight)
		return true
	default:
		return false
	}
}

func (s *app) scaleNativeImageViewer(m *nativeMediaState, factor float32) {
	if m == nil {
		return
	}
	fit := nativeImageFitScale(float32(m.imageInfo.Width), float32(m.imageInfo.Height), m.imageViewportWidth, m.imageViewportHeight)
	m.imageZoom = boundNativeImageZoom(m.imageZoom*factor, fit)
	m.imageOffsetX, m.imageOffsetY = boundNativeImageOffset(m.imageOffsetX, m.imageOffsetY,
		float32(m.imageInfo.Width), float32(m.imageInfo.Height), fit, m.imageZoom, m.imageViewportWidth, m.imageViewportHeight)
}

func (s *app) fitNativeImageViewer(m *nativeMediaState) {
	if m == nil {
		return
	}
	m.imageZoom = 1
	m.imageOffsetX, m.imageOffsetY = 0, 0
}

func (s *app) originalNativeImageViewer(m *nativeMediaState) {
	if m == nil {
		return
	}
	fit := nativeImageFitScale(float32(m.imageInfo.Width), float32(m.imageInfo.Height), m.imageViewportWidth, m.imageViewportHeight)
	if fit > 0 {
		m.imageZoom = boundNativeImageZoom(1/fit, fit)
	}
	m.imageOffsetX, m.imageOffsetY = 0, 0
}

func nativeImageFitScale(imageWidth, imageHeight, viewportWidth, viewportHeight float32) float32 {
	if !nativeMediaFinitePositive(imageWidth) || !nativeMediaFinitePositive(imageHeight) || !nativeMediaFinitePositive(viewportWidth) || !nativeMediaFinitePositive(viewportHeight) {
		return 1
	}
	fit := minFloat64(float64(viewportWidth/imageWidth), float64(viewportHeight/imageHeight))
	if fit <= 0 || math.IsNaN(fit) || math.IsInf(fit, 0) {
		return 1
	}
	return float32(fit)
}

func boundNativeImageZoom(zoom, fit float32) float32 {
	if !nativeMediaFinitePositive(fit) {
		fit = 1
	}
	if math.IsNaN(float64(zoom)) || math.IsInf(float64(zoom), 0) || zoom <= 0 {
		zoom = 1
	}
	minimum := minFloat32(.1/fit, 1)
	maximum := maxFloat32(8/fit, 1)
	return minFloat32(maxFloat32(zoom, minimum), maximum)
}

func boundNativeImageOffset(x, y, imageWidth, imageHeight, fit, zoom, viewportWidth, viewportHeight float32) (float32, float32) {
	if !nativeMediaFinitePositive(viewportWidth) || !nativeMediaFinitePositive(viewportHeight) {
		return 0, 0
	}
	if !nativeMediaFinitePositive(fit) || !nativeMediaFinitePositive(zoom) {
		return 0, 0
	}
	maxX := maxFloat32(0, (imageWidth*fit*zoom-viewportWidth)/2)
	maxY := maxFloat32(0, (imageHeight*fit*zoom-viewportHeight)/2)
	return clampImageOffset(x, maxX), clampImageOffset(y, maxY)
}

func clampImageOffset(value, maximum float32) float32 {
	if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) || maximum <= 0 {
		return 0
	}
	return minFloat32(maxFloat32(value, -maximum), maximum)
}
