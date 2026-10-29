package desktop

import (
	"context"
	"sync"
)

const pdfRenderQueueLimit = 8

type pdfQueuedRender struct {
	page, width int
	ctx         context.Context
	request     *pdfPageRenderRequest
	run         func(context.Context, *pdfPageRenderRequest)
}

// One worker per open PDF. The latest map also retains completed-but-not-yet
// applied requests, so a resize away and back cannot revive an older callback.
type pdfRenderQueue struct {
	mu                 sync.Mutex
	ctx                context.Context
	cancel             context.CancelFunc
	wake               chan struct{}
	latest             map[int]*pdfQueuedRender
	pending            map[int]*pdfQueuedRender
	active             *pdfQueuedRender
	first, last, width int
	closed             bool
}

func newPDFRenderQueue(parent context.Context) *pdfRenderQueue {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	return &pdfRenderQueue{ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), latest: make(map[int]*pdfQueuedRender), pending: make(map[int]*pdfQueuedRender), last: -1}
}

func (q *pdfRenderQueue) enqueue(page, width int, run func(context.Context, *pdfPageRenderRequest)) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed || q.ctx.Err() != nil {
		return
	}
	if previous := q.latest[page]; previous != nil {
		if previous.width == width && !previous.request.invalidated.Load() {
			return
		}
		previous.request.invalidate()
	}
	ctx, cancel := context.WithCancel(q.ctx)
	job := &pdfQueuedRender{page: page, width: width, ctx: ctx, request: &pdfPageRenderRequest{cancel: cancel}, run: run}
	q.latest[page], q.pending[page] = job, job
	for len(q.latest) > pdfRenderQueueLimit {
		var farthest *pdfQueuedRender
		for _, item := range q.latest {
			if farthest == nil || q.distance(item.page) > q.distance(farthest.page) || q.distance(item.page) == q.distance(farthest.page) && item.page > farthest.page {
				farthest = item
			}
		}
		farthest.request.invalidate()
		delete(q.latest, farthest.page)
		delete(q.pending, farthest.page)
	}
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *pdfRenderQueue) distance(page int) int {
	if q.last < q.first {
		return absInt(page - q.first)
	}
	if page < q.first {
		return q.first - page
	}
	if page > q.last {
		return page - q.last
	}
	return 0
}

func (q *pdfRenderQueue) viewport(first, last, width int) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.first, q.last, q.width = first, last, width
	if last < first {
		return
	}
	for page, job := range q.latest {
		if page < first || page > last || job.width != width {
			job.request.invalidate()
			delete(q.latest, page)
			delete(q.pending, page)
		}
	}
}

func (q *pdfRenderQueue) ack(page int, request *pdfPageRenderRequest) {
	q.mu.Lock()
	if job := q.latest[page]; job != nil && job.request == request {
		delete(q.latest, page)
	}
	q.mu.Unlock()
}

func (q *pdfRenderQueue) invalidateAll() {
	if q == nil {
		return
	}
	q.mu.Lock()
	for _, job := range q.latest {
		job.request.invalidate()
	}
	q.latest, q.pending = make(map[int]*pdfQueuedRender), make(map[int]*pdfQueuedRender)
	q.mu.Unlock()
}

func (q *pdfRenderQueue) close() {
	if q == nil {
		return
	}
	q.mu.Lock()
	q.closed = true
	q.cancel()
	for _, job := range q.latest {
		job.request.invalidate()
	}
	q.latest, q.pending = nil, nil
	q.mu.Unlock()
}

func (q *pdfRenderQueue) run() {
	defer q.close()
	for {
		q.mu.Lock()
		var job *pdfQueuedRender
		for _, candidate := range q.pending {
			if job == nil || q.distance(candidate.page) < q.distance(job.page) || q.distance(candidate.page) == q.distance(job.page) && candidate.page < job.page {
				job = candidate
			}
		}
		if job != nil {
			delete(q.pending, job.page)
			q.active = job
		}
		q.mu.Unlock()
		if job == nil {
			select {
			case <-q.ctx.Done():
				return
			case <-q.wake:
				continue
			}
		}
		if job.ctx.Err() == nil {
			job.run(job.ctx, job.request)
		} else {
			q.ack(job.page, job.request)
		}
		job.request.cancel()
		q.mu.Lock()
		q.active = nil
		q.mu.Unlock()
		if q.ctx.Err() != nil {
			return
		}
	}
}
