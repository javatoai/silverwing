package desktop

import (
	"container/list"
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/egoist/mygo/ui"
)

var errNativeImageQueueFull = errors.New("图片加载队列已满")
var nativeImagePoolSequence atomic.Uint64

const nativeImagePoolWorkers = 2
const nativeImagePoolPending = 64
const nativeImagePoolBytes = 32 << 20
const nativeImagePoolItems = 32

type nativeImageSubscriber struct {
	ctx  context.Context
	done func(*ui.Bitmap, error)
}

type nativeImageFlight struct {
	key                    string
	ctx                    context.Context
	cancel                 context.CancelFunc
	load                   func(context.Context) (*ui.Bitmap, error)
	subscribers            map[uint64]nativeImageSubscriber
	bitmap                 *ui.Bitmap
	ready, queued, running bool
	bytes                  int64
	lru                    *list.Element
}

// A pool belongs to one validated document/reader scope, never to a URL or to
// the process. Published bitmaps are immutable: consumers only display/share
// them and never call Update or Pixelated. Subscribers own cancellation and
// their separate UI/auth/generation checks; the final unsubscribe retires work.
type nativeImagePool struct {
	mu       sync.Mutex
	id       uint64
	ctx      context.Context
	cancel   context.CancelFunc
	wake     chan struct{}
	done     chan struct{}
	workers  int
	entries  map[string]*nativeImageFlight
	queue    []*nativeImageFlight
	order    list.List
	bytes    int64
	sequence uint64
	closed   bool
}

func newNativeImagePool() *nativeImagePool {
	ctx, cancel := context.WithCancel(context.Background())
	p := &nativeImagePool{id: nativeImagePoolSequence.Add(1), ctx: ctx, cancel: cancel, wake: make(chan struct{}, nativeImagePoolWorkers), entries: make(map[string]*nativeImageFlight), done: make(chan struct{}), workers: nativeImagePoolWorkers}
	for range nativeImagePoolWorkers {
		go func() {
			defer func() {
				p.mu.Lock()
				p.workers--
				if p.workers == 0 {
					close(p.done)
				}
				p.mu.Unlock()
			}()
			p.run()
		}()
	}
	return p
}

func (p *nativeImagePool) subscribe(ctx context.Context, key string, load func(context.Context) (*ui.Bitmap, error), done func(*ui.Bitmap, error)) (context.CancelFunc, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || ctx.Err() != nil {
		return nil, context.Canceled
	}
	f := p.entries[key]
	if len(p.queue) >= nativeImagePoolPending && (f == nil || !f.running && !f.queued) {
		return nil, errNativeImageQueueFull
	}
	if f == nil {
		child, cancel := context.WithCancel(p.ctx)
		f = &nativeImageFlight{key: key, ctx: child, cancel: cancel, load: load, subscribers: make(map[uint64]nativeImageSubscriber)}
		p.entries[key] = f
	}
	p.sequence++
	id := p.sequence
	f.subscribers[id] = nativeImageSubscriber{ctx, done}
	if !f.queued && !f.running {
		f.queued = true
		p.queue = append(p.queue, f)
		select {
		case p.wake <- struct{}{}:
		default:
		}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			delete(f.subscribers, id)
			if len(f.subscribers) == 0 && !f.ready {
				f.cancel()
				if p.entries[key] == f {
					delete(p.entries, key)
				}
			}
			p.mu.Unlock()
		})
	}, nil
}

func (p *nativeImagePool) run() {
	for {
		p.mu.Lock()
		var f *nativeImageFlight
		if len(p.queue) > 0 {
			f = p.queue[0]
			p.queue[0] = nil
			p.queue = p.queue[1:]
			f.queued, f.running = false, true
		}
		p.mu.Unlock()
		if f == nil {
			select {
			case <-p.ctx.Done():
				return
			case <-p.wake:
				continue
			}
		}
		var bitmap *ui.Bitmap
		var err error
		if f.ctx.Err() != nil {
			err = f.ctx.Err()
		} else if f.ready {
			bitmap = f.bitmap
		} else {
			bitmap, err = f.load(f.ctx)
		}
		p.mu.Lock()
		f.running = false
		if !p.closed && f.ctx.Err() == nil && err == nil && bitmap != nil {
			if !f.ready {
				w, h := bitmap.Size()
				f.bytes = int64(w) * int64(h) * 6 // pixels plus lazily generated mip levels
				f.bitmap, f.ready = bitmap, true
				if f.bytes <= nativeImagePoolBytes {
					f.lru = p.order.PushFront(f)
					p.bytes += f.bytes
				}
			} else if f.lru != nil {
				p.order.MoveToFront(f.lru)
			}
		}
		subscribers := f.subscribers
		f.subscribers = make(map[uint64]nativeImageSubscriber)
		if !f.ready || f.lru == nil {
			if p.entries[f.key] == f {
				delete(p.entries, f.key)
			}
		}
		for p.bytes > nativeImagePoolBytes || p.order.Len() > nativeImagePoolItems {
			last := p.order.Back()
			old := last.Value.(*nativeImageFlight)
			p.order.Remove(last)
			old.lru = nil
			p.bytes -= old.bytes
			if p.entries[old.key] == old {
				delete(p.entries, old.key)
			}
			if !old.running && !old.queued && len(old.subscribers) == 0 {
				old.cancel()
			}
		}
		closed := p.closed
		p.mu.Unlock()
		if !closed && f.ctx.Err() == nil {
			for _, subscriber := range subscribers {
				if subscriber.ctx.Err() == nil {
					subscriber.done(bitmap, err)
				}
			}
		}
		p.mu.Lock()
		if f.lru == nil {
			f.cancel()
		}
		p.mu.Unlock()
		if p.ctx.Err() != nil {
			return
		}
	}
}

func (p *nativeImagePool) close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.closed = true
	p.cancel()
	for _, entry := range p.entries {
		entry.cancel()
	}
	p.entries, p.queue = nil, nil
	p.order.Init()
	p.bytes = 0
	p.mu.Unlock()
}

func (p *nativeImagePool) wait(ctx context.Context) error {
	if p == nil {
		return nil
	}
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
