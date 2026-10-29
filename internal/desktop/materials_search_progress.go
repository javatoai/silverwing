package desktop

import (
	"context"
	"sync"

	"silverwing-go/internal/documents"
)

// A search owns one delivery. At most one ordinary callback is queued; it
// reads the latest value when it runs. Finish retires that callback and posts
// the final value/result immediately, without waiting for a progress interval.
type materialsSearchProgressDelivery struct {
	mu                sync.Mutex
	ctx               context.Context
	post              func(func()) bool
	apply             func(documents.MaterialsSearchProgress)
	latest            documents.MaterialsSearchProgress
	pending, finished bool
}

type materialsSearchDeliveryIdentity struct {
	state      *materialsPickerState
	generation uint64
	taskID     string
}

// Called only on the UI thread, for both progress and terminal delivery.
func (id materialsSearchDeliveryIdentity) current(ctx context.Context, state *materialsPickerState, taskID string, closed bool) bool {
	return ctx.Err() == nil && !closed && state == id.state && state != nil &&
		state.searchGeneration == id.generation && state.searchOpen && taskID == id.taskID
}

func newMaterialsSearchProgressDelivery(ctx context.Context, post func(func()) bool, apply func(documents.MaterialsSearchProgress)) *materialsSearchProgressDelivery {
	return &materialsSearchProgressDelivery{ctx: ctx, post: post, apply: apply}
}

func (d *materialsSearchProgressDelivery) progress(progress documents.MaterialsSearchProgress) {
	d.mu.Lock()
	if d.finished || d.ctx.Err() != nil {
		d.mu.Unlock()
		return
	}
	// The observer's pointer need not outlive this call.
	if progress.CurrentPath != nil {
		path := *progress.CurrentPath
		progress.CurrentPath = &path
	}
	d.latest = progress
	if d.pending {
		d.mu.Unlock()
		return
	}
	d.pending = true
	d.mu.Unlock()
	if !d.post(d.deliver) {
		d.mu.Lock()
		d.finished, d.pending = true, false
		d.mu.Unlock()
	}
}

func (d *materialsSearchProgressDelivery) deliver() {
	d.mu.Lock()
	if d.finished || d.ctx.Err() != nil {
		d.pending = false
		d.mu.Unlock()
		return
	}
	latest := d.latest
	d.pending = false
	d.mu.Unlock()
	if d.ctx.Err() == nil {
		d.apply(latest)
	}
}

func (d *materialsSearchProgressDelivery) finish(applyResult func(documents.MaterialsSearchProgress)) {
	d.mu.Lock()
	if d.finished || d.ctx.Err() != nil {
		d.finished = true
		d.mu.Unlock()
		return
	}
	d.finished = true
	latest := d.latest
	d.mu.Unlock()
	d.post(func() {
		if d.ctx.Err() == nil {
			applyResult(latest)
		}
	})
}
