package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

type fakePDFState struct {
	mu            sync.Mutex
	events        []string
	pages, texts  int
	width, height int32
	pixels        []byte
	counts        int32
	onUnicode     func(uintptr, int32)
	unicodeCalls  atomic.Int64
}

func fakePerformancePDF(pageCount int) (*PDFDocument, *fakePDFState) {
	f := &fakePDFState{counts: 1}
	api := &pdfiumAPI{}
	api.loadPage = func(_ uintptr, index int32) uintptr {
		f.mu.Lock()
		f.pages++
		f.events = append(f.events, fmt.Sprintf("page-%d", index))
		f.mu.Unlock()
		return uintptr(index + 10)
	}
	api.closePage = func(uintptr) { f.mu.Lock(); f.pages--; f.mu.Unlock() }
	api.closeDocument = func(uintptr) {}
	api.textLoadPage = func(page uintptr) uintptr {
		f.mu.Lock()
		f.texts++
		f.events = append(f.events, fmt.Sprintf("text-%d", int(page)-10))
		f.mu.Unlock()
		return page + 100
	}
	api.textClosePage = func(uintptr) { f.mu.Lock(); f.texts--; f.mu.Unlock() }
	api.textCountChars = func(uintptr) int32 { return f.counts }
	api.textGetUnicode = func(page uintptr, index int32) uint32 {
		f.unicodeCalls.Add(1)
		if f.onUnicode != nil {
			f.onUnicode(page, index)
		}
		return uint32('A' + page - 110)
	}
	api.textGetCharBox = func(_ uintptr, _ int32, left, right, bottom, top *float64) int32 {
		*left, *right, *bottom, *top = 0, 1, 0, 1
		return 1
	}
	api.createBitmap = func(w, h, _ int32) uintptr { f.width, f.height = w, h; f.pixels = make([]byte, int(w*h)*4); return 1 }
	api.fillRect = func(uintptr, int32, int32, int32, int32, uint32) int32 { return 1 }
	api.renderPageBitmap = func(_ uintptr, page uintptr, _, _, _, _, _, _ int32) {
		f.mu.Lock()
		f.events = append(f.events, fmt.Sprintf("render-%d", int(page)-10))
		f.mu.Unlock()
		for i := 0; i < len(f.pixels); i += 4 {
			f.pixels[i], f.pixels[i+1], f.pixels[i+2], f.pixels[i+3] = 30, 20, 10, 255
		}
	}
	api.getBitmapBuffer = func(uintptr) unsafe.Pointer { return unsafe.Pointer(&f.pixels[0]) }
	api.getBitmapWidth = func(uintptr) int32 { return f.width }
	api.getBitmapHeight = func(uintptr) int32 { return f.height }
	api.getBitmapStride = func(uintptr) int32 { return f.width * 4 }
	api.getBitmapFormat = func(uintptr) int32 { return 4 }
	api.destroyBitmap = func(uintptr) { clear(f.pixels) }
	d := &PDFDocument{api: api, document: 1, info: DocumentInfo{PageCount: pageCount}, cache: newPNGCache(1024), rgbaCache: newRGBACache(1024)}
	for page := 0; page < pageCount; page++ {
		d.info.Pages = append(d.info.Pages, PageInfo{Index: page, WidthPoints: 1, HeightPoints: 1, AspectRatio: 1})
		d.cropBoxes = append(d.cropBoxes, cropBox{right: 1, top: 1})
	}
	return d, f
}

func TestPDFTextTurnsYieldToVisibleRenderingAndKeepPageOrder(t *testing.T) {
	doc, fake := fakePerformancePDF(3)
	defer doc.Close()
	started, proceed := make(chan struct{}), make(chan struct{})
	var once sync.Once
	fake.onUnicode = func(_ uintptr, _ int32) { once.Do(func() { close(started); <-proceed }) }
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	indexed := make(chan TextIndex, 1)
	fail := make(chan error, 2)
	go func() {
		index, err := doc.TextIndexPrioritized(ctx, []int{2, 2, -1})
		if err != nil {
			fail <- err
		} else {
			indexed <- index
		}
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("text never started")
	}
	rendered := make(chan struct{}, 1)
	go func() {
		_, err := doc.RenderPageRGBA(ctx, 0, 1)
		if err != nil {
			fail <- err
		}
		rendered <- struct{}{}
	}()
	for {
		doc.turn.mu.Lock()
		waiting := doc.turn.renders > 0
		doc.turn.mu.Unlock()
		if waiting {
			break
		}
		if ctx.Err() != nil {
			close(proceed)
			t.Fatal("render did not queue")
		}
		runtime.Gosched()
	}
	close(proceed)
	select {
	case <-rendered:
	case err := <-fail:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case index := <-indexed:
		if len(index.Pages) != 3 || index.Pages[0].Text != "A" || index.Pages[1].Text != "B" || index.Pages[2].Text != "C" {
			t.Fatal("priority extraction changed the complete page-ordered result")
		}
	case err := <-fail:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	joined := strings.Join(fake.events, ",")
	if strings.Index(joined, "render-0") > strings.Index(joined, "text-0") || !strings.Contains(joined, "text-2") || fake.pages != 0 || fake.texts != 0 {
		t.Fatalf("render did not take the page boundary or native handles leaked: %s", joined)
	}
}

func TestPDFTextCancelCloseAndPositionBudget(t *testing.T) {
	t.Run("cancel", func(t *testing.T) {
		doc, fake := fakePerformancePDF(2)
		fake.counts = 1000
		ctx, cancel := context.WithCancel(context.Background())
		fake.onUnicode = func(uintptr, int32) { cancel() }
		if _, err := doc.TextIndex(ctx); err == nil {
			t.Fatal("cancelled extraction returned a complete index")
		}
		if fake.pages != 0 || fake.texts != 0 {
			t.Fatal("cancelled extraction left native handles open")
		}
		if err := doc.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := doc.TextIndex(context.Background()); !errors.Is(err, ErrClosed) {
			t.Fatalf("closed extraction = %v", err)
		}
	})
	t.Run("budget", func(t *testing.T) {
		doc, fake := fakePerformancePDF(6)
		defer doc.Close()
		fake.counts = maxPDFPagePositions
		if _, err := doc.TextIndex(context.Background()); err == nil {
			t.Fatal("more than 500,000 native positions were accepted")
		}
		if fake.unicodeCalls.Load() != maxPDFTextPositions || fake.pages != 0 || fake.texts != 0 {
			t.Fatal("position budget was checked after reading another page or leaked handles")
		}
	})
}

func TestPDFRGBAOwnsPixelsAndLegacyPNGRemainsIndependent(t *testing.T) {
	doc, fake := fakePerformancePDF(1)
	first, err := doc.RenderPageRGBA(context.Background(), 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if first.RGBA.RGBAAt(0, 0) != (color.RGBA{10, 20, 30, 255}) {
		t.Fatal("returned RGBA aliases the destroyed native bitmap or changed BGR conversion")
	}
	first.RGBA.Pix[0] = 200
	second, err := doc.RenderPageRGBA(context.Background(), 0, 2)
	if err != nil || second.RGBA.Pix[0] != 10 {
		t.Fatal("caller mutation reached the RGBA cache")
	}
	encoded, err := doc.RenderPagePNG(context.Background(), 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(encoded.PNG))
	if err != nil || color.RGBAModel.Convert(decoded.At(0, 0)) != (color.RGBA{10, 20, 30, 255}) {
		t.Fatal("legacy PNG changed pixels")
	}
	clear(encoded.PNG)
	again, err := doc.RenderPagePNG(context.Background(), 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(again.PNG)); err != nil {
		t.Fatal("caller mutation reached PNG cache")
	}
	if err := doc.Close(); err != nil {
		t.Fatal(err)
	}
	if second.RGBA.Pix[0] != 10 || fake.pages != 0 || doc.rgbaCache.bytes != 0 || doc.cache.bytes != 0 {
		t.Fatal("close invalidated owned output or retained cached pixels")
	}
}

func TestImageRGBAOwnershipAlphaLimitsAndCacheBudget(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	src.SetNRGBA(0, 0, color.NRGBA{200, 100, 50, 128})
	doc := &ImageDocument{info: ImageInfo{Width: 4, Height: 2}, image: src, cache: newPNGCache(128), rgbaCache: newRGBACache(128)}
	first, err := doc.RenderRGBA(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, a := first.RGBA.At(0, 0).RGBA()
	wr, wg, wb, wa := src.At(0, 0).RGBA()
	if abs32(r, wr) > 257 || abs32(g, wg) > 257 || abs32(b, wb) > 257 || a != wa {
		t.Fatal("RGBA transfer changed alpha")
	}
	first.RGBA.Pix[0] = 0
	second, _ := doc.RenderRGBA(context.Background(), 0)
	if second.RGBA.Pix[0] == 0 {
		t.Fatal("returned image aliases the source or cached pixels")
	}
	if _, err := doc.RenderRGBA(context.Background(), -1); err == nil {
		t.Fatal("negative width accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := doc.RenderRGBA(ctx, 2); err == nil {
		t.Fatal("cancelled resize accepted")
	}
	encoded, err := doc.RenderPNG(context.Background(), 0)
	if err != nil || len(encoded.PNG) == 0 || doc.cache.bytes+doc.rgbaCache.bytes > 256 {
		t.Fatal("legacy PNG or cache budget failed")
	}
	_ = doc.Close()
	if _, err := doc.RenderRGBA(context.Background(), 0); !errors.Is(err, ErrClosed) {
		t.Fatal("closed image rendered")
	}
}

func abs32(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}

var mediaBenchImage image.Image

func BenchmarkImageTransfer(b *testing.B) {
	for _, mode := range []string{"rgba", "png-roundtrip"} {
		b.Run(mode, func(b *testing.B) {
			doc := &ImageDocument{info: ImageInfo{Width: 1200, Height: 800}, image: image.NewRGBA(image.Rect(0, 0, 1200, 800)), cache: newPNGCache(16 << 20), rgbaCache: newRGBACache(16 << 20)}
			defer doc.Close()
			_, _ = doc.RenderRGBA(context.Background(), 0)
			_, _ = doc.RenderPNG(context.Background(), 0)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if mode == "rgba" {
					page, err := doc.RenderRGBA(context.Background(), 0)
					if err != nil {
						b.Fatal(err)
					}
					mediaBenchImage = page.RGBA
				} else {
					page, err := doc.RenderPNG(context.Background(), 0)
					if err != nil {
						b.Fatal(err)
					}
					mediaBenchImage, err = png.Decode(bytes.NewReader(page.PNG))
					if err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
