package media

import (
	"container/list"
	"image"
	"sync"
)

// RenderedRGBA transfers an independently owned, premultiplied RGBA image to
// the caller. Its pixels never alias PDFium memory, a document or its cache.
// A UI may transfer that ownership to a bitmap and then leave the pixels alone.
type RenderedRGBA struct {
	PageIndex, PageCount int
	Width, Height        int
	AspectRatio          float64
	RGBA                 *image.RGBA
}

type rgbaEntry struct {
	key string
	img *image.RGBA
}

type rgbaCache struct {
	mu           sync.Mutex
	limit, bytes int64
	items        map[string]*list.Element
	order        list.List
}

func newRGBACache(limit int64) *rgbaCache {
	return &rgbaCache{limit: limit, items: make(map[string]*list.Element)}
}

func cloneRGBA(img *image.RGBA) *image.RGBA {
	if img == nil {
		return nil
	}
	cloned := image.NewRGBA(img.Rect)
	for y := 0; y < img.Rect.Dy(); y++ {
		copy(cloned.Pix[y*cloned.Stride:], img.Pix[y*img.Stride:y*img.Stride+img.Rect.Dx()*4])
	}
	return cloned
}

func (c *rgbaCache) get(key string) (*image.RGBA, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el := c.items[key]
	if el == nil {
		return nil, false
	}
	c.order.MoveToFront(el)
	return cloneRGBA(el.Value.(rgbaEntry).img), true
}

func (c *rgbaCache) put(key string, img *image.RGBA) {
	if c == nil || img == nil || c.limit <= 0 || int64(len(img.Pix)) > c.limit {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if old := c.items[key]; old != nil {
		c.bytes -= int64(len(old.Value.(rgbaEntry).img.Pix))
		c.order.Remove(old)
	}
	owned := cloneRGBA(img)
	c.items[key] = c.order.PushFront(rgbaEntry{key, owned})
	c.bytes += int64(len(owned.Pix))
	for c.bytes > c.limit {
		last := c.order.Back()
		entry := last.Value.(rgbaEntry)
		c.bytes -= int64(len(entry.img.Pix))
		delete(c.items, entry.key)
		c.order.Remove(last)
	}
}

func (c *rgbaCache) clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.items = make(map[string]*list.Element)
	c.order.Init()
	c.bytes = 0
	c.mu.Unlock()
}
