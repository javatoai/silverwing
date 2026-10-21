package production

import "context"

// Progress is an in-process observation, never a write authorization or a
// persisted result. The request owner decides whether an observation is current.
type Progress struct {
	Selection Selection
	BatchID   string
	Step      int
	State     string
	Message   string
}

type progressKey struct{}

func WithProgress(ctx context.Context, observe func(Progress)) context.Context {
	return context.WithValue(ctx, progressKey{}, observe)
}

func EmitProgress(ctx context.Context, progress Progress) {
	if ctx.Err() != nil {
		return
	}
	if observe, ok := ctx.Value(progressKey{}).(func(Progress)); ok && observe != nil {
		observe(progress)
	}
}

func batchProgress(ctx context.Context, b *Batch, step int, state, message string) {
	EmitProgress(ctx, Progress{Selection: b.Target.Selection, BatchID: b.ID, Step: step, State: state, Message: message})
}
