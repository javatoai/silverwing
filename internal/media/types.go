// Package media provides native, WebView-free image and PDF previews.
package media

import "errors"

const (
	DefaultMaxPDFBytes    int64 = 64 << 20
	DefaultMaxImageBytes  int64 = 32 << 20
	DefaultMaxImagePixels int64 = 20_000_000
	DefaultMaxPDFPages          = 10_000
	DefaultCacheBytes     int64 = 32 << 20
)

var (
	ErrClosed          = errors.New("媒体预览已关闭")
	ErrCancelled       = errors.New("媒体预览已取消")
	ErrUnsupportedType = errors.New("该文件类型不支持预览")
)

// Options bounds input, decoded pixels, PDF page count, and cached PNG data.
// Zero fields select the documented defaults. PDFiumPath overrides runtime
// discovery for the native PDFium library.
type Options struct {
	MaxPDFBytes    int64
	MaxImageBytes  int64
	MaxImagePixels int64
	MaxPDFPages    int
	CacheBytes     int64
	PDFiumPath     string
}

func (o Options) normalized() (Options, error) {
	if o.MaxPDFBytes == 0 {
		o.MaxPDFBytes = DefaultMaxPDFBytes
	}
	if o.MaxImageBytes == 0 {
		o.MaxImageBytes = DefaultMaxImageBytes
	}
	if o.MaxImagePixels == 0 {
		o.MaxImagePixels = DefaultMaxImagePixels
	}
	if o.MaxPDFPages == 0 {
		o.MaxPDFPages = DefaultMaxPDFPages
	}
	if o.CacheBytes == 0 {
		o.CacheBytes = DefaultCacheBytes
	}
	if o.MaxPDFBytes < 1 || o.MaxImageBytes < 1 || o.MaxImagePixels < 1 || o.MaxPDFPages < 1 || o.CacheBytes < 0 {
		return Options{}, errors.New("媒体预览限制必须为正数，缓存限制不能为负数")
	}
	return o, nil
}

// RenderedPage contains an opaque PNG suitable for decoding by the native UI.
// For an image preview PageIndex is -1 and PageCount is 1.
type RenderedPage struct {
	PageIndex   int
	PageCount   int
	Width       int
	Height      int
	AspectRatio float64
	PNG         []byte
}

// PageInfo describes the visible page area after the PDF's page rotation.
// WidthPoints and HeightPoints are PDF points (1 point = 1/72 inch).
type PageInfo struct {
	Index        int
	WidthPoints  float64
	HeightPoints float64
	Rotation     int
	AspectRatio  float64
}

type OutlineEntry struct {
	Title     string
	PageIndex int
	Depth     int
}

type DocumentInfo struct {
	PageCount int
	Pages     []PageInfo
	Outline   []OutlineEntry
}

// TextIndex contains extracted UTF-8 text and normalized character geometry.
// Glyph Start and End are half-open rune offsets into Text; bounds use the
// page's displayed top-left coordinate system and range from 0 to 1.
type TextIndex struct {
	Pages   []TextPage
	Outline []OutlineEntry
}

type TextPage struct {
	Text   string
	Glyphs []Glyph
}

type Glyph struct {
	Start  int
	End    int
	Left   float32
	Top    float32
	Right  float32
	Bottom float32
}

type ImageInfo struct {
	Width  int
	Height int
	Format string
}
