package application

import (
	"context"
	"errors"
	"testing"
)

func TestRequirementListProgressRejectsUnrelatedOrCanceledActions(t *testing.T) {
	a := &App{}
	for _, kind := range []string{"requirements.comment", "task.create", "production.build-tag"} {
		_, err := a.DispatchRequirementList(context.Background(), Action{Kind: kind}, func(RequirementListProgress) { t.Fatal("unauthorized progress callback") })
		if err == nil {
			t.Fatalf("accepted unrelated action %s", kind)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := a.DispatchRequirementList(ctx, Action{Kind: "requirements.refresh"}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read was executed: %v", err)
	}
	if _, err := a.DispatchRequirementList(nil, Action{Kind: "requirements.refresh"}, nil); err == nil {
		t.Fatal("nil context accepted")
	}
}
