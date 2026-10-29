package application

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"silverwing-go/internal/tools"
)

func TestTypedDeveloperToolsAndActionAdapterHaveIdenticalResults(t *testing.T) {
	app := &App{}
	source := `{"a":[1,2],"s":"中文"}`
	for _, operation := range []string{"format", "compact", "escape", "unescape"} {
		input := source
		if operation == "unescape" {
			input = `"{\"a\":1}"`
		}
		request := JSONToolRequest{Input: input, Operation: operation}
		typed, typedErr := app.ProcessJSON(context.Background(), request)
		legacy, legacyErr := app.Dispatch(context.Background(), Action{Kind: "tool.json", Text: input, Payload: map[string]any{"operation": operation}})
		left, _ := json.Marshal(typed.ToActionResult())
		right, _ := json.Marshal(legacy)
		if (typedErr == nil) != (legacyErr == nil) || string(left) != string(right) {
			t.Fatalf("%s typed/action mismatch: %s %v; %s %v", operation, left, typedErr, right, legacyErr)
		}
	}
	request := TimeToolRequest{Input: "1700000000", Unit: "seconds", Zone: "UTC"}
	typed, typedErr := app.ConvertTime(context.Background(), request)
	legacy, legacyErr := app.Dispatch(context.Background(), Action{Kind: "tool.time", Text: request.Input, Payload: map[string]any{"unit": request.Unit, "zone": request.Zone}})
	converted, decodeErr := ResultData[tools.TimeConversion](legacy)
	if typedErr != nil || legacyErr != nil || decodeErr != nil || !reflect.DeepEqual(typed.Data, converted) {
		t.Fatalf("time conversion mismatch: %+v %v; %+v %v %v", typed, typedErr, converted, legacyErr, decodeErr)
	}
}
func TestTypedDeveloperToolCancellationAndInvalidCompatibilityPayload(t *testing.T) {
	app := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := app.ProcessJSON(ctx, JSONToolRequest{Input: `{}`}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := app.ReadRequirementList(ctx, RequirementListRequest{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := app.ReadRequirement(ctx, RequirementReadRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := JSONToolRequestFromAction(Action{Payload: map[string]any{"operation": "node-value", "nodeStart": 0.5, "nodeEnd": 1}}); err == nil {
		t.Fatal("fractional JSON node boundary accepted")
	}
	if _, err := PortQueryRequestFromAction(Action{Payload: map[string]any{"port": "-1"}}); err == nil {
		t.Fatal("signed port accepted")
	}
}
