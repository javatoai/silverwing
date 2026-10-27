package application

import (
	"encoding/json"
	"math"
	"testing"
)

func TestNumberPayloadUsesStrictIntegerForms(t *testing.T) {
	tests := []struct {
		name    string
		value   any
		want    int
		wantErr bool
	}{
		{name: "float lower bound", value: float64(1), want: 1},
		{name: "float middle", value: float64(80), want: 80},
		{name: "float upper bound", value: float64(65535), want: 65535},
		{name: "integer", value: 80, want: 80},
		{name: "json number", value: json.Number("80"), want: 80},
		{name: "digit string", value: "80", want: 80},
		{name: "leading zero string", value: "00080", want: 80},
		{name: "fractional float", value: float64(80.9), wantErr: true},
		{name: "fractional json number", value: json.Number("80.9"), wantErr: true},
		{name: "exponent json number", value: json.Number("8e1"), wantErr: true},
		{name: "signed json number", value: json.Number("+80"), wantErr: true},
		{name: "negative json number", value: json.Number("-80"), wantErr: true},
		{name: "nonfinite json number", value: json.Number("NaN"), wantErr: true},
		{name: "signed string", value: "+80", wantErr: true},
		{name: "negative string", value: "-80", wantErr: true},
		{name: "fraction string", value: "80.0", wantErr: true},
		{name: "float below range", value: float64(0), wantErr: true},
		{name: "float above range", value: float64(65536), wantErr: true},
		{name: "NaN float", value: math.NaN(), wantErr: true},
		{name: "positive infinity", value: math.Inf(1), wantErr: true},
		{name: "negative infinity", value: math.Inf(-1), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := numberPayload(Action{Payload: map[string]any{"port": tt.value}}, "port")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("numberPayload(%#v) succeeded with %d, want an error", tt.value, got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("numberPayload(%#v) = %d, %v; want %d, nil", tt.value, got, err, tt.want)
			}
		})
	}
}
