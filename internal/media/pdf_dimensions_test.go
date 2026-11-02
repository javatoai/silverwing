package media

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"math"
	"testing"
)

func TestPDFPixelDimensionsFitRoundedBudget(t *testing.T) {
	pages := []struct {
		name string
		w, h float64
	}{
		{"reported-page", 595.3499755859375, 841.9500122070312},
		{"reported-landscape", 841.9500122070312, 595.3499755859375},
		{"A4", 595.28, 841.89},
		{"letter", 612, 792},
		{"square", 2000, 2000},
		{"tall", 1, 100000},
		{"wide", 100000, 1},
	}
	for _, page := range pages {
		for _, requested := range []int{1, 64, 1200, 1681, 1682, 2048, 4096, 8192, math.MaxInt} {
			t.Run(fmt.Sprintf("%s/%d", page.name, requested), func(t *testing.T) {
				w, h, err := pdfPixelDimensions(page.w, page.h, requested)
				if err != nil {
					t.Fatal("valid page failed preview", err)
				}
				if w < 1 || h < 1 || w > maxPDFPreviewEdge || h > maxPDFPreviewEdge || int64(w)*int64(h) > maxPDFPreviewPixels {
					t.Fatalf("unbounded rounded bitmap %dx%d", w, h)
				}
			})
		}
	}
}

func TestPDFPixelDimensionsPreserveOrdinaryAndExactSizes(t *testing.T) {
	for _, tc := range []struct {
		w, h                  float64
		request, wantW, wantH int
	}{
		{595.3499755859375, 841.9500122070312, 1200, 1200, 1698},
		{595.3499755859375, 841.9500122070312, 1681, 1681, 2378},
		{640, 480, 320, 320, 240},
		{1000, 1000, 2000, 2000, 2000},
	} {
		w, h, err := pdfPixelDimensions(tc.w, tc.h, tc.request)
		if err != nil || w != tc.wantW || h != tc.wantH {
			t.Fatalf("ordinary/exact size changed: %dx%d %v", w, h, err)
		}
	}
}

func TestPDFPixelDimensionsRejectInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		w, h    float64
		request int
	}{
		{0, 1, 100}, {-1, 1, 100}, {1, 0, 100}, {1, -1, 100},
		{math.NaN(), 1, 100}, {1, math.NaN(), 100},
		{math.Inf(1), 1, 100}, {1, math.Inf(1), 100},
		{1, 1, 0}, {1, 1, -1},
	} {
		if _, _, err := pdfPixelDimensions(tc.w, tc.h, tc.request); err == nil {
			t.Fatalf("invalid dimensions accepted: %+v", tc)
		}
	}
}

func TestPDFClampedRGBAAndPNGRenderWithinBudget(t *testing.T) {
	doc, native := fakePerformancePDF(1)
	defer doc.Close()
	doc.info.Pages[0] = PageInfo{Index: 0, WidthPoints: 595.3499755859375, HeightPoints: 841.9500122070312, AspectRatio: 595.3499755859375 / 841.9500122070312}
	rgba, err := doc.RenderPageRGBA(context.Background(), 0, maxPDFPreviewWidth)
	if err != nil {
		t.Fatal(err)
	}
	if rgba.Width != rgba.RGBA.Bounds().Dx() || rgba.Height != rgba.RGBA.Bounds().Dy() || int64(rgba.Width)*int64(rgba.Height) > maxPDFPreviewPixels {
		t.Fatal("RGBA does not match bounded dimensions")
	}
	encoded, err := doc.RenderPagePNG(context.Background(), 0, maxPDFPreviewWidth)
	if err != nil {
		t.Fatal(err)
	}
	config, err := png.DecodeConfig(bytes.NewReader(encoded.PNG))
	if err != nil || config.Width != rgba.Width || config.Height != rgba.Height || encoded.Width != rgba.Width || encoded.Height != rgba.Height {
		t.Fatal("PNG and RGBA dimensions differ", err)
	}
	if native.pages != 0 || rgba.AspectRatio != doc.info.Pages[0].AspectRatio {
		t.Fatal("render leaked page or changed logical aspect")
	}
}
