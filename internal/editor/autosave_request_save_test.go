package editor

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestAutosaveExplicitPristineSaveDoesNotTurnOpeningIntoWrite(t *testing.T) {
	var calls atomic.Int32
	a := NewAutosave(func(_ context.Context, _ string, snapshot SaveSnapshot) error {
		if snapshot.Content != "" {
			t.Errorf("explicit empty save changed source: %q", snapshot.Content)
		}
		calls.Add(1)
		return nil
	}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	defer a.Close(ctx)
	a.Seed("missing.md", "")
	if failures := a.PrepareClose(ctx); len(failures) != 0 || calls.Load() != 0 {
		t.Fatal("opening a pristine entry wrote a file")
	}
	a.SetComposing("missing.md", true)
	if a.RequestSave("missing.md") {
		t.Fatal("saved unfinished composition")
	}
	a.SetComposing("missing.md", false)
	if !a.RequestSave("missing.md") {
		t.Fatal("explicit empty creation rejected")
	}
	if failures := a.PrepareClose(ctx); len(failures) != 0 || calls.Load() != 1 || a.Status("missing.md").Dirty {
		t.Fatalf("explicit creation not saved once: %v calls=%d", failures, calls.Load())
	}
}
