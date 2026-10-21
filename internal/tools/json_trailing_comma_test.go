package tools

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestJSONToolAllowsOneTrailingComma(t *testing.T) {
	for _, source := range []string{
		"{\n    \"a\": \"2\",\n\n}", `[1,]`, `{"a":[1,{"b":2,},],}`, "[1, \r\n\t]",
		`{"comma":"hi,}","array":",]","escaped":"\\\"", "url":"https://example.invalid/a,b?q=\"中文\"",}`,
		`{"n":123456789012345678901234567890,"float":1.2300e+50,}`,
	} {
		t.Run(source, func(t *testing.T) {
			doc, err := ParseJSON(source)
			if err != nil {
				t.Fatal(err)
			}
			if !json.Valid([]byte(normalizeJSONToolTrailingCommas(doc.Formatted))) || !json.Valid([]byte(doc.Compact)) {
				t.Fatalf("formatted output exceeds trailing-comma syntax or compact is not standard JSON: %q %q", doc.Formatted, doc.Compact)
			}
			if strings.Contains(source, "123456789012345678901234567890") && (!strings.Contains(doc.Compact, "123456789012345678901234567890") || !strings.Contains(doc.Compact, "1.2300e+50")) {
				t.Fatal("number lexemes changed")
			}
		})
	}
}

func TestJSONToolTrailingCommaRejectsOtherSyntaxAndKeepsOffsets(t *testing.T) {
	for _, source := range []string{`[,]`, `{,}`, `{"a":,}`, `[1,,]`, `{"a":1,,}`, `{"a":1 "b":2,}`, `{"a":1,]`, `{'a':1,}`, `// comment
{"a":1,}`, `{"a":NaN,}`, `{"a":1,} extra`, `{"a":"unterminated,}`} {
		if _, err := ParseJSON(source); err == nil {
			t.Fatalf("unsupported syntax accepted: %q", source)
		}
	}
	source := "{\n \"a\": [\"🪴\",],\n \"b\": @\n}"
	_, err := ParseJSON(source)
	var detail *JSONError
	if !errors.As(err, &detail) || detail.Line != 3 || detail.Column != 7 {
		t.Fatalf("diagnostic did not retain original location: %+v %v", detail, err)
	}
	valid := "{\n \"a\": [\"🪴\",],\n \"b\": 2,\n}"
	doc, err := ParseJSON(valid)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range doc.Nodes {
		fragment := valid[UTF16OffsetToByte(valid, node.StartOffset):UTF16OffsetToByte(valid, node.EndOffset)]
		if _, err := ParseJSON(fragment); err != nil {
			t.Fatalf("original source range changed for %s: %q %v", node.Pointer, fragment, err)
		}
	}
	text := `中文: "quote", \slash,},]`
	escaped, err := EscapeJSON(text)
	if err != nil {
		t.Fatal(err)
	}
	unescaped, err := UnescapeJSON(escaped)
	if err != nil || unescaped != text {
		t.Fatalf("escape roundtrip changed: %q %v", unescaped, err)
	}
}

func TestJSONToolTrailingCommaPreservesStringsExactly(t *testing.T) {
	want := []string{",}", ",]", `quote\":,}`, `\\`, "https://example.invalid/a,b?q=中文🪴", `\\\",]`}
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	source := string(encoded[:len(encoded)-1]) + ",\n]"
	doc, err := ParseJSON(source)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := json.Unmarshal([]byte(doc.Compact), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatal("string count changed")
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("string %d changed: %q != %q", i, got[i], want[i])
		}
	}
}
