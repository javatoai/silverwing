package media

import (
	"container/list"
	"sync"
)

type cacheEntry struct {
	key string
	png []byte
}

// pngCache is a byte-budgeted LRU. Values are copied on both ingress and
// egress so a UI consumer cannot mutate data still owned by the cache.
type pngCache struct {
	mu      sync.Mutex
	limit   int64
	bytes   int64
	entries map[string]*list.Element
	order   *list.List
}

func newPNGCache(limit int64) *pngCache {
	return &pngCache{limit: limit, entries: make(map[string]*list.Element), order: list.New()}
}

func (c *pngCache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(element)
	entry := element.Value.(cacheEntry)
	return append([]byte(nil), entry.png...), true
}

func (c *pngCache) put(key string, png []byte) {
	if c == nil || c.limit <= 0 || int64(len(png)) > c.limit {
		return
	}
	copyPNG := append([]byte(nil), png...)
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.entries[key]; ok {
		c.bytes -= int64(len(old.Value.(cacheEntry).png))
		c.order.Remove(old)
		delete(c.entries, key)
	}
	element := c.order.PushFront(cacheEntry{key: key, png: copyPNG})
	c.entries[key] = element
	c.bytes += int64(len(copyPNG))
	for c.bytes > c.limit {
		last := c.order.Back()
		if last == nil {
			break
		}
		entry := last.Value.(cacheEntry)
		c.bytes -= int64(len(entry.png))
		delete(c.entries, entry.key)
		c.order.Remove(last)
	}
}

func (c *pngCache) clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.entries = make(map[string]*list.Element)
	c.order.Init()
	c.bytes = 0
	c.mu.Unlock()
}
