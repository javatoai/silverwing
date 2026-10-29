package media

import (
	"context"
	"sync"
)

// PDFium remains serial. At a page boundary, queued visible rendering takes
// the next document turn before background text extraction can take another.
type pdfTurn struct {
	mu      sync.Mutex
	busy    bool
	renders int
	changed chan struct{}
}

func (g *pdfTurn) signal() {
	if g.changed != nil {
		close(g.changed)
	}
	g.changed = make(chan struct{})
}

func (g *pdfTurn) acquire(ctx context.Context, render bool) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	g.mu.Lock()
	if g.changed == nil {
		g.changed = make(chan struct{})
	}
	if render {
		g.renders++
	}
	for {
		if err := ctx.Err(); err != nil {
			if render {
				g.renders--
				g.signal()
			}
			g.mu.Unlock()
			return nil, err
		}
		if !g.busy && (render || g.renders == 0) {
			break
		}
		changed := g.changed
		g.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
		}
		g.mu.Lock()
	}
	if render {
		g.renders--
	}
	g.busy = true
	g.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { g.mu.Lock(); g.busy = false; g.signal(); g.mu.Unlock() }) }, nil
}
