package desktop

import (
	"context"
	"testing"
	"time"
)

func TestTextEditorJSONProductionCommandsAndProjection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	commands, err := checkJSONSourceCommands(ctx)
	if err != nil {
		t.Fatalf("source commands: %v; %v", err, commands.Observations)
	}
	t.Logf("source commands: %d checks", len(commands.Checks))
	folds, err := checkJSONInlineFold(ctx)
	if err != nil {
		t.Fatalf("inline folding: %v; %v", err, folds.Observations)
	}
	t.Logf("inline folding: %d checks", len(folds.Checks))
	search, err := checkJSONSearchGutter(ctx)
	if err != nil {
		t.Fatalf("search/gutter: %v; %v", err, search.Failures)
	}
	t.Logf("search/gutter: %d checks", len(search.Checks))
}
