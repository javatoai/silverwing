package application

import (
	"context"
	"testing"
)

func TestWorkspaceDiffSessionCloseDoesNotRequireTaskOrConfiguration(t *testing.T) {
	app := &App{}
	result, err := app.Dispatch(context.Background(), Action{
		Kind: "workspace.diff-session-close",
		Payload: map[string]any{"sessionId": "opaque-session-token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Message == "" {
		t.Fatal("expected close acknowledgement")
	}
	if err := app.CloseWorkspaceDiffSessions(context.Background()); err != nil {
		t.Fatal(err)
	}
}

