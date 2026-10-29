package desktop

import "time"

const (
	materialsNativeWatchQuiet   = 50 * time.Millisecond
	materialsNativeWatchCeiling = 200 * time.Millisecond
)

// This batch owns only a native watcher's structural-event deadline. Regular
// file writes retain their local path handling. The ceiling prevents a busy
// directory from indefinitely postponing safe catalog/descriptor capture.
type materialsWatchRebuildBatch struct {
	first, last time.Time
}

func (b *materialsWatchRebuildBatch) record(now time.Time) {
	if b.first.IsZero() {
		b.first = now
	}
	b.last = now
}

func (b *materialsWatchRebuildBatch) deadline() (time.Time, bool) {
	if b.first.IsZero() {
		return time.Time{}, false
	}
	return minMaterialsWatchTime(b.last.Add(materialsNativeWatchQuiet), b.first.Add(materialsNativeWatchCeiling)), true
}

func (b *materialsWatchRebuildBatch) ready(now time.Time) bool {
	deadline, pending := b.deadline()
	return pending && !now.Before(deadline)
}

func (b *materialsWatchRebuildBatch) reset() { *b = materialsWatchRebuildBatch{} }

func minMaterialsWatchTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
