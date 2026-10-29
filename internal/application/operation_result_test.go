package application

import (
	"errors"
	"reflect"
	"testing"
)

type nativeResultWithoutJSON struct{ Value string }

func (nativeResultWithoutJSON) MarshalJSON() ([]byte, error) {
	return nil, errors.New("native values must not use JSON")
}

func TestResultDataPrefersNativeValueAndPropagatesCodecErrors(t *testing.T) {
	expected := nativeResultWithoutJSON{Value: "native"}
	for _, raw := range []any{expected, &expected} {
		actual, err := ResultData[nativeResultWithoutJSON](ActionResult{Data: raw})
		if err != nil || actual != expected {
			t.Fatalf("native result used codec: %+v %v", actual, err)
		}
	}
	actual, err := ResultData[struct {
		Value string `json:"value"`
	}](ActionResult{Data: map[string]any{"value": "compatibility"}})
	if err != nil || actual.Value != "compatibility" {
		t.Fatalf("map compatibility: %+v %v", actual, err)
	}
	for _, raw := range []any{nil, map[string]any{"value": []string{"malformed"}}, make(chan int)} {
		if _, err := ResultData[struct {
			Value string `json:"value"`
		}](ActionResult{Data: raw}); err == nil {
			t.Fatalf("malformed result accepted: %#v", raw)
		}
	}
}

func TestOperationResultRetainsLegacyNoDataAndNativeJSONShape(t *testing.T) {
	if got := (OperationResult[struct{}]{Message: "done"}).ToActionResult(); got.Data != nil || got.Message != "done" {
		t.Fatalf("unit result: %+v", got)
	}
	if got := (OperationResult[RequirementContent]{}).ToActionResult(); got.Data != nil {
		t.Fatalf("empty failed result grew a data payload: %+v", got)
	}
	var absent *RequirementContent
	if got := (OperationResult[*RequirementContent]{Message: "done", Data: absent}).ToActionResult(); got.Data != nil {
		t.Fatalf("optional result: %+v", got)
	}
	content := RequirementContent{Title: "title"}
	if got := (OperationResult[*RequirementContent]{Data: &content}).ToActionResult(); !reflect.DeepEqual(got.Data, content) {
		t.Fatalf("native compatibility value: %+v", got)
	}
}
