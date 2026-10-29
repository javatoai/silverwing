package desktop

import (
	"context"
	"errors"
	"testing"

	"silverwing-go/internal/documents"
)

func TestMaterialsSearchProgressCoalescesFiveThousandAndDeliversTerminalImmediately(t *testing.T) {
	var queued []func()
	var updates []documents.MaterialsSearchProgress
	delivery := newMaterialsSearchProgressDelivery(context.Background(), func(callback func()) bool { queued = append(queued, callback); return true }, func(progress documents.MaterialsSearchProgress) { updates = append(updates, progress) })
	for i := 0; i < 5000; i++ {
		delivery.progress(documents.MaterialsSearchProgress{CompletedFiles: i, TotalFiles: 5000})
	}
	if len(queued) != 1 {
		t.Fatalf("5000 candidates queued %d ordinary callbacks", len(queued))
	}
	queued[0]()
	if len(updates) != 1 || updates[0].CompletedFiles != 4999 {
		t.Fatal("pending callback did not read the latest value")
	}
	delivery.progress(documents.MaterialsSearchProgress{CompletedFiles: 5000, TotalFiles: 5000})
	terminal := errors.New("terminal failure")
	var finalError error
	delivery.finish(func(progress documents.MaterialsSearchProgress) {
		updates = append(updates, progress)
		finalError = terminal
	})
	if len(queued) != 3 {
		t.Fatal("terminal result was delayed behind a progress timer")
	}
	queued[1]() // invalidated pending progress must not overwrite completion
	queued[2]()
	delivery.progress(documents.MaterialsSearchProgress{})
	if len(updates) != 2 || updates[1].CompletedFiles != 5000 || finalError != terminal || len(queued) != 3 {
		t.Fatalf("final progress/error was lost or overwritten: updates=%+v error=%v queued=%d", updates, finalError, len(queued))
	}
}

func TestMaterialsSearchProgressSnapshotsPathAndDropsCancelledOrRejectedDelivery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var queued []func()
	writes := 0
	delivery := newMaterialsSearchProgressDelivery(ctx, func(callback func()) bool { queued = append(queued, callback); return true }, func(progress documents.MaterialsSearchProgress) {
		writes++
		if *progress.CurrentPath != "original.txt" {
			t.Fatal("observer mutated queued path value")
		}
	})
	path := "original.txt"
	delivery.progress(documents.MaterialsSearchProgress{CurrentPath: &path})
	path = "changed.txt"
	queued[0]()
	delivery.progress(documents.MaterialsSearchProgress{})
	cancel()
	queued[1]()
	delivery.finish(func(documents.MaterialsSearchProgress) { writes++ })
	if writes != 1 || len(queued) != 2 {
		t.Fatal("cancelled progress or completion wrote back")
	}
	posts := 0
	rejected := newMaterialsSearchProgressDelivery(context.Background(), func(func()) bool { posts++; return false }, func(documents.MaterialsSearchProgress) { t.Fatal("closed queue applied progress") })
	rejected.progress(documents.MaterialsSearchProgress{})
	rejected.progress(documents.MaterialsSearchProgress{})
	rejected.finish(func(documents.MaterialsSearchProgress) { t.Fatal("closed queue applied terminal result") })
	if posts != 1 {
		t.Fatal("closed queue continued accepting progress work")
	}
}

func TestMaterialsSearchDeliveryIdentityRejectsStaleClosedAndCancelled(t *testing.T) {
	state := &materialsPickerState{searchOpen: true, searchGeneration: 8}
	identity := materialsSearchDeliveryIdentity{state: state, generation: 8, taskID: "task"}
	ctx, cancel := context.WithCancel(context.Background())
	if !identity.current(ctx, state, "task", false) {
		t.Fatal("active search rejected")
	}
	if identity.current(ctx, state, "other", false) || identity.current(ctx, state, "task", true) || identity.current(ctx, &materialsPickerState{searchOpen: true, searchGeneration: 8}, "task", false) {
		t.Fatal("stale task/window/state accepted")
	}
	state.searchGeneration++
	if identity.current(ctx, state, "task", false) {
		t.Fatal("stale generation accepted")
	}
	state.searchGeneration = 8
	state.searchOpen = false
	if identity.current(ctx, state, "task", false) {
		t.Fatal("closed search accepted")
	}
	state.searchOpen = true
	cancel()
	if identity.current(ctx, state, "task", false) {
		t.Fatal("cancelled search accepted")
	}
}
