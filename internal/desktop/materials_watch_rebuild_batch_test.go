package desktop

import (
	"testing"
	"time"
)

func TestMaterialsWatchRebuildBatchMergesBurstAndCapsContinuousEvents(t *testing.T) {
	start := time.Unix(100, 0)
	var batch materialsWatchRebuildBatch
	for i := 0; i < 40; i++ {
		batch.record(start.Add(time.Duration(i) * time.Millisecond))
	}
	if batch.ready(start.Add(88*time.Millisecond)) || !batch.ready(start.Add(89*time.Millisecond)) {
		t.Fatal("structural burst did not use its final quiet deadline")
	}
	batch.reset()
	if _, pending := batch.deadline(); pending {
		t.Fatal("flushed batch remained pending")
	}
	captures := 0
	for i := 0; i < 1000; i++ {
		now := start.Add(time.Duration(i) * time.Millisecond)
		batch.record(now)
		if batch.ready(now) {
			captures++
			batch.reset()
		}
	}
	if batch.ready(start.Add(1100 * time.Millisecond)) {
		captures++
		batch.reset()
	}
	if captures != 5 {
		t.Fatalf("1000 continuous batches produced %d captures; ceiling must bound captures and latency", captures)
	}
}
