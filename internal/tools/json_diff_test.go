package tools

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestCompareJSONStructure(t *testing.T) {
	left := `{"trace":{"id":"first"},"removed":null,"data":{"values":[3,4,5],"amount":9007199254740992},"same":{"b":true,"a":1}}`
	right := `{"same":{"a":1.0,"b":true},"data":{"amount":9007199254740993,"values":[4,3]},"added":null,"trace":{"id":"first"}}`
	got, err := CompareJSON(context.Background(), left, right, "")
	if err != nil {
		t.Fatal(err)
	}
	want := JSONDiffResult{
		Entries: []JSONDiffEntry{
			{Path: "$.added", Kind: JSONDiffAdded, Right: "null"},
			{Path: "$.data.amount", Kind: JSONDiffChanged, Left: "9007199254740992", Right: "9007199254740993"},
			{Path: "$.data.values[0]", Kind: JSONDiffChanged, Left: "3", Right: "4"},
			{Path: "$.data.values[1]", Kind: JSONDiffChanged, Left: "4", Right: "3"},
			{Path: "$.data.values[2]", Kind: JSONDiffRemoved, Left: "5"},
			{Path: "$.removed", Kind: JSONDiffRemoved, Left: "null"},
		},
		Added: 1, Removed: 2, Changed: 3,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CompareJSON() = %#v, want %#v", got, want)
	}
	wantCopy := "+ $.added = null\n" +
		"- $.data.amount = 9007199254740992\n+ $.data.amount = 9007199254740993\n" +
		"- $.data.values[0] = 3\n+ $.data.values[0] = 4\n" +
		"- $.data.values[1] = 4\n+ $.data.values[1] = 3\n" +
		"- $.data.values[2] = 5\n- $.removed = null\n"
	if copied := got.DifferencesText(); copied != wantCopy {
		t.Fatalf("DifferencesText() = %q, want %q", copied, wantCopy)
	}
}

func TestCompareJSONIgnoreRules(t *testing.T) {
	left := `{"data":{"timestamp":1,"traceId":"old","traceIdentifier":"first","a,b":0,"named.value":1},"else":{"timestamp":1},"rows":[0,{"traceId":1,"keep":1}],"a\"b,c":1}`
	right := `{"data":{"timestamp":2,"traceId":"new","traceIdentifier":"second","a,b":1,"named.value":2},"else":{"timestamp":2},"rows":[9,{"traceId":2,"keep":2}],"a\"b,c":2}`
	ignore := "traceId,\r\n $.data.timestamp, $.rows[0], " + `$["data"]["a,b"], $["a\"b,c"]`
	got, err := CompareJSON(context.Background(), left, right, ignore)
	if err != nil {
		t.Fatal(err)
	}
	want := []JSONDiffEntry{
		{Path: `$.data["named.value"]`, Kind: JSONDiffChanged, Left: "1", Right: "2"},
		{Path: "$.data.traceIdentifier", Kind: JSONDiffChanged, Left: `"first"`, Right: `"second"`},
		{Path: "$.else.timestamp", Kind: JSONDiffChanged, Left: "1", Right: "2"},
		{Path: "$.rows[1].keep", Kind: JSONDiffChanged, Left: "1", Right: "2"},
	}
	if !reflect.DeepEqual(got.Entries, want) {
		t.Fatalf("CompareJSON() entries = %#v, want %#v", got.Entries, want)
	}
}

func TestCompareJSONFiltersReportedContainers(t *testing.T) {
	left := `{"change":{"traceId":"secret","z":2,"a":1},"remove":{"traceId":"secret","nested":[{"timestamp":"secret","keep":2}]}}`
	right := `{"change":[{"traceId":"secret","z":4,"a":3}],"add":{"list":[{"traceId":"secret","visible":null},"secret",3]}}`
	ignore := "traceId, $.remove.nested[0].timestamp, $.add.list[1]"
	got, err := CompareJSON(context.Background(), left, right, ignore)
	if err != nil {
		t.Fatal(err)
	}
	want := []JSONDiffEntry{
		{Path: "$.add", Kind: JSONDiffAdded, Right: `{"list":[{"visible":null},3]}`},
		{Path: "$.change", Kind: JSONDiffChanged, Left: `{"a":1,"z":2}`, Right: `[{"a":3,"z":4}]`},
		{Path: "$.remove", Kind: JSONDiffRemoved, Left: `{"nested":[{"keep":2}]}`},
	}
	if !reflect.DeepEqual(got.Entries, want) {
		t.Fatalf("CompareJSON() entries = %#v, want %#v", got.Entries, want)
	}
}

func TestCompareJSONNumberEquality(t *testing.T) {
	large := strings.Repeat("9", 512)
	cases := []struct {
		left, right string
		equal       bool
	}{
		{"1e+00", "1.00", true},
		{"1e-100000000", "0.1e-99999999", true},
		{"10e" + large, "1e1" + strings.Repeat("0", len(large)), true},
		{"0.1e-" + large, "1e-1" + strings.Repeat("0", len(large)), true},
		{"10e-1" + strings.Repeat("0", len(large)), "1e-" + large, true},
		{"0.1e1" + strings.Repeat("0", len(large)), "1e" + large, true},
		{"20e" + large, "1e1" + strings.Repeat("0", len(large)), false},
		{"1e" + large, "1e" + large[:len(large)-1] + "8", false},
		{"0.0000e+" + large, "-0e-" + large, true},
		{"1e-99999999", "0.1e-99999999", false},
		{"1e-0", "0.1e+1", true},
		{"0.1e-0", "1e-1", true},
		{"100e-0", "1e+2", true},
		{"-0.00001e05", "-1", true},
		{"-10", "10", false},
		{"1", `"1"`, false},
		{"null", "false", false},
	}
	for _, tc := range cases {
		got, err := CompareJSON(context.Background(), tc.left, tc.right, "")
		if err != nil {
			t.Errorf("CompareJSON(%q, %q): %v", tc.left, tc.right, err)
			continue
		}
		if equal := len(got.Entries) == 0; equal != tc.equal {
			t.Errorf("CompareJSON(%q, %q) equality = %v, want %v", tc.left, tc.right, equal, tc.equal)
		}
		if !tc.equal {
			want := JSONDiffEntry{Path: "$", Kind: JSONDiffChanged, Left: tc.left, Right: tc.right}
			if len(got.Entries) != 1 || got.Entries[0] != want {
				t.Errorf("CompareJSON(%q, %q) entries = %#v, want [%#v]", tc.left, tc.right, got.Entries, want)
			}
		}
	}
}

func TestCompareJSONValidation(t *testing.T) {
	tooDeep := strings.Repeat("[", MaxJSONDepth+1) + "0" + strings.Repeat("]", MaxJSONDepth+1)
	tooBig := strings.Repeat(" ", MaxTextBytes+1)
	cases := []struct {
		name, left, right, ignore, message string
	}{
		{"LeftSyntax", `{"left":}`, "null", "", "左侧 JSON"},
		{"RightSyntax", "null", `{"right":}`, "", "右侧 JSON"},
		{"TrailingValue", "null", "null false", "", "多余内容"},
		{"LeftSize", tooBig, "null", "", "2 MiB"},
		{"RightDepth", "null", tooDeep, "", "256 层"},
		{"DuplicateKey", `{"a":0,"\u0061":1}`, "null", "", "重复键"},
		{"InvalidIgnore", "null", "null", `$["a"`, "忽略规则"},
		{"TrailingComma", `[0,]`, "null", "", "左侧 JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CompareJSON(context.Background(), tc.left, tc.right, tc.ignore)
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("CompareJSON() error = %v, want %q", err, tc.message)
			}
			if len(got.Entries) != 0 {
				t.Fatalf("CompareJSON() returned a partial result: %#v", got)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CompareJSON(ctx, "null", "null", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("CompareJSON() error = %v, want context.Canceled", err)
	}
}

func TestCompareJSONRenderedSizeLimit(t *testing.T) {
	key := strings.Repeat("a", 1<<17)
	left := `{"` + key + `":[` + strings.Repeat("0,", 99) + "0]}"
	right := `{"` + key + `":[` + strings.Repeat("1,", 99) + "1]}"
	got, err := CompareJSON(context.Background(), left, right, "")
	if err == nil || !strings.Contains(err.Error(), "8 MiB") {
		t.Fatalf("CompareJSON() error = %v, want output size error", err)
	}
	if len(got.Entries) != 0 {
		t.Fatalf("CompareJSON() returned %d partial entries", len(got.Entries))
	}
}
