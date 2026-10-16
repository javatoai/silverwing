package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONFormatRetainsOnlyExistingTrailingCommas(t *testing.T) {
	for _, test := range []struct{ name, source, want string }{
		{"object", "{\n  \"a\": \"a\",\n}", "{\n    \"a\": \"a\",\n}"},
		{"array", "[1, \r\n\t]", "[\n    1,\n]"},
		{"without-comma", `{"a":[1,{"b":2}],"empty":{}}`, "{\n    \"a\": [\n        1,\n        {\n            \"b\": 2\n        }\n    ],\n    \"empty\": {}\n}"},
		{"mixed-nested", `{"a":[{"b":2,},3],"c":[4,],}`, "{\n    \"a\": [\n        {\n            \"b\": 2,\n        },\n        3\n    ],\n    \"c\": [\n        4,\n    ],\n}"},
		{"adjacent-closings", `[[{"a":1},],{"b":[2,]},]`, "[\n    [\n        {\n            \"a\": 1\n        },\n    ],\n    {\n        \"b\": [\n            2,\n        ]\n    },\n]"},
		{"empty-object", " \t{}\r\n", "{}"},
		{"empty-array", "[] \n", "[]"},
		{"scalar", " 90071992547409931234567890 \n", "90071992547409931234567890"},
	} {
		t.Run(test.name, func(t *testing.T) {
			doc, err := ParseJSON(test.source)
			if err != nil {
				t.Fatal(err)
			}
			if doc.Formatted != test.want {
				t.Fatalf("formatted = %q, want %q", doc.Formatted, test.want)
			}
			if !json.Valid([]byte(doc.Compact)) {
				t.Fatalf("compact stopped emitting standard JSON: %q", doc.Compact)
			}
			again, err := ParseJSON(doc.Formatted)
			if err != nil || again.Formatted != doc.Formatted || again.Compact != doc.Compact {
				t.Fatalf("format was not idempotent: %+v, %v", again, err)
			}
		})
	}
}

func TestJSONFormatPreservesLexemesOrderAndDuplicateKeys(t *testing.T) {
	source := `{"z":",} ,]","quote":"\\\",]","unicode":"\u4e2d\u6587🪴","url":"https:\/\/example.invalid\/a,b","n":90071992547409931234567890,"e":1.2300e+50,"z":false,}`
	doc, err := ParseJSON(source)
	if err != nil {
		t.Fatal(err)
	}
	previous := -1
	for _, lexeme := range []string{
		`"z": ",} ,]"`, `"quote": "\\\",]"`, `"unicode": "\u4e2d\u6587🪴"`,
		`"url": "https:\/\/example.invalid\/a,b"`, `"n": 90071992547409931234567890`, `"e": 1.2300e+50`, `"z": false,`,
	} {
		index := strings.Index(doc.Formatted, lexeme)
		if index <= previous {
			t.Fatalf("source lexeme/order lost at %q in %q", lexeme, doc.Formatted)
		}
		previous = index
	}
	if strings.Count(doc.Formatted, `"z":`) != 2 {
		t.Fatal("format collapsed duplicate object keys")
	}
	again, err := ParseJSON(doc.Formatted)
	if err != nil || again.Formatted != doc.Formatted {
		t.Fatal("lexical format was not idempotent", err)
	}
}
