package desktop

import (
	"context"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/tools"
)

func TestJSONDiffToolTypedResultAndInputScope(t *testing.T) {
	state := &toolPageState{
		diffLeft: `{"traceId":1,"amount":0}`, diffRight: `{"traceId":2,"amount":null}`,
		diffJSONMode: true, diffJSONIgnore: "traceId", diffRevision: 3,
	}
	action := application.Action{Kind: "tool.diff", Payload: map[string]any{
		"mode": application.JSONDiffMode, "ignore": state.diffJSONIgnore,
		"left": state.diffLeft, "right": state.diffRight, "revision": state.diffRevision,
	}}
	if !developerToolActionInputMatches(action, state) {
		t.Fatal("current JSON comparison was rejected")
	}
	result, err := executeToolOperation(context.Background(), &application.App{}, action)
	if err != nil {
		t.Fatal(err)
	}
	state.handleResult(action, result)
	if !state.diffHasData || len(state.diffJSONResult.Entries) != 1 || state.diffJSONResult.Entries[0].Kind != tools.JSONDiffChanged {
		t.Fatalf("typed JSON result was not applied: %+v", state.diffJSONResult)
	}
	state.diffJSONIgnore = "amount"
	if developerToolActionInputMatches(action, state) {
		t.Fatal("the earlier ignore rules still match the current request")
	}
	state.diffJSONIgnore = "traceId"
	state.diffJSONMode = false
	if developerToolActionInputMatches(action, state) {
		t.Fatal("a JSON reply still matches after switching to plain text")
	}
}
