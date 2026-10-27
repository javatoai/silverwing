package application

import (
	"context"
	"strings"
	"testing"
)

func TestRetiredCloudActionsCannotExecute(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"materials.ensure", "materials.resolve-directory", "materials.resolve-entry", "materials.plan", "materials.confirm", "materials.sync", "materials.status", "materials.cancel", "materials.recover", "materials.comment"} {
		if _, err := a.Dispatch(context.Background(), Action{Kind: kind}); err == nil {
			t.Fatalf("retired action executable: %s", kind)
		}
	}
	for _, kind := range []string{"cli.status", "cli.auth.start", "cli.auth.logout"} {
		if _, err := a.Dispatch(context.Background(), Action{Kind: kind, Payload: map[string]any{"tool": "lark"}}); err == nil || !strings.Contains(strings.ToLower(err.Error()), "cli") && !strings.Contains(err.Error(), "Meegle") {
			t.Fatalf("retired Lark %s: %v", kind, err)
		}
	}
}
