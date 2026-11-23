package genbuapi

import (
	"reflect"
	"strings"
	"testing"
)

func TestEditConfigPropertiesPreservesOtherContent(t *testing.T) {
	original := "\uFEFF# configured locally\r\n" +
		"  feature.enabled : false\r\n" +
		"unchanged=some\\\n    continued\r\n" +
		"obsolete\tvalue\n" +
		"! footer\r\n"
	edits := []PropertyEdit{
		{Operation: "set", Key: "feature.enabled", Value: "true"},
		{Operation: "unset", Key: "obsolete"},
		{Operation: "add", Key: "service.url", Value: "https://example.invalid/path?a=b"},
	}
	want := "\uFEFF# configured locally\r\n" +
		"  feature.enabled : true\r\n" +
		"unchanged=some\\\n    continued\r\n" +
		"! footer\r\n" +
		"service.url=https://example.invalid/path?a=b\r\n"
	wantChanges := []ConfigEditChange{
		{Operation: "set", Key: "feature.enabled"},
		{Operation: "unset", Key: "obsolete"},
		{Operation: "add", Key: "service.url"},
	}
	got, changes, err := editConfigProperties(original, edits)
	if err != nil || got != want || !reflect.DeepEqual(changes, wantChanges) {
		t.Fatalf("got %q with changes %#v and error %v, want %q", got, changes, err, want)
	}
}

func TestEditConfigPropertiesMatchesLogicalKeysAndValues(t *testing.T) {
	original := "port=123\\\r\n    45\n" +
		"feature\\ name\\:\\= : old\\ value\r\n" +
		"\\u540d\\u5b57=\\uD83D\\uDE00\r\n" +
		"key\\\r\n\t.suffix=old\r\n" +
		"without.delimiter"
	got, changes, err := editConfigProperties(original, []PropertyEdit{
		{Operation: "set", Key: "port", Value: "12345"},
		{Operation: "set", Key: "feature name:=", Value: " leading\\path=x"},
		{Operation: "set", Key: "名字", Value: "😀"},
		{Operation: "set", Key: "key.suffix", Value: ""},
		{Operation: "set", Key: "without.delimiter", Value: "now"},
	})
	want := "port=123\\\r\n    45\n" +
		"feature\\ name\\:\\= : \\ leading\\\\path=x\r\n" +
		"\\u540d\\u5b57=\\uD83D\\uDE00\r\n" +
		"key.suffix=\r\n" +
		"without.delimiter=now"
	wantChanges := []ConfigEditChange{
		{Operation: "set", Key: "feature name:="},
		{Operation: "set", Key: "key.suffix"},
		{Operation: "set", Key: "without.delimiter"},
	}
	if err != nil || got != want || !reflect.DeepEqual(changes, wantChanges) {
		t.Fatalf("got %q with changes %#v and error %v, want %q", got, changes, err, want)
	}
}

func TestEditConfigPropertiesAppendsWithoutChangingLastProperty(t *testing.T) {
	for _, test := range []struct {
		name     string
		original string
		want     string
	}{
		{name: "BOM only", original: "\uFEFF", want: "\uFEFFnew=ok\n"},
		{name: "no final newline", original: "keep=x", want: "keep=x\nnew=ok"},
		{name: "dangling at EOF", original: "keep=x\\", want: "keep=x\\\n\nnew=ok"},
		{name: "dangling CR in mixed endings", original: "first=ok\nkeep=x\\\r", want: "first=ok\nkeep=x\\\r\r\nnew=ok\n"},
		{name: "comment ending backslash", original: "! note\\\r", want: "! note\\\rnew=ok\r"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, _, err := editConfigProperties(test.original, []PropertyEdit{{Operation: "add", Key: "new", Value: "ok"}})
			if err != nil || got != test.want {
				t.Fatalf("got %q and error %v, want %q", got, err, test.want)
			}
			same, changes, err := editConfigProperties(got, []PropertyEdit{{Operation: "set", Key: "new", Value: "ok"}})
			if err != nil || same != got || len(changes) != 0 {
				t.Fatalf("added key did not round trip, got %q with changes %#v and error %v", same, changes, err)
			}
		})
	}
}

func TestEditConfigPropertiesEscapedNewKeyAndValueRoundTrip(t *testing.T) {
	const key = "! key:name=part\\"
	const value = " a\\b=c\t\r\n\f"
	got, _, err := editConfigProperties("", []PropertyEdit{{Operation: "add", Key: key, Value: value}})
	want := "\\!\\ key\\:name\\=part\\\\=\\ a\\\\b=c\\t\\r\\n\\f\n"
	if err != nil || got != want {
		t.Fatalf("got %q and error %v, want %q", got, err, want)
	}
	same, changes, err := editConfigProperties(got, []PropertyEdit{{Operation: "set", Key: key, Value: value}})
	if err != nil || same != got || len(changes) != 0 {
		t.Fatalf("same value unexpectedly changed, got %q with changes %#v and error %v", same, changes, err)
	}
}

func TestEditConfigPropertiesPreservesLeadingValueSeparators(t *testing.T) {
	original := "equals old\r\ncolon\told\r\n"
	edits := []PropertyEdit{
		{Operation: "set", Key: "equals", Value: "=new"},
		{Operation: "set", Key: "colon", Value: ":new"},
	}
	got, _, err := editConfigProperties(original, edits)
	want := "equals \\=new\r\ncolon\t\\:new\r\n"
	if err != nil || got != want {
		t.Fatalf("got %q and error %v, want %q", got, err, want)
	}
	same, changes, err := editConfigProperties(got, edits)
	if err != nil || same != got || len(changes) != 0 {
		t.Fatalf("same value unexpectedly changed, got %q with changes %#v and error %v", same, changes, err)
	}
}

func TestEditConfigPropertiesEscapesLiteralBOMInFirstKey(t *testing.T) {
	const key = "\uFEFFproperty"
	got, _, err := editConfigProperties("", []PropertyEdit{{Operation: "add", Key: key, Value: "x"}})
	if err != nil || got != "\\uFEFFproperty=x\n" {
		t.Fatalf("unexpected property or error: %q, %v", got, err)
	}
	same, changes, err := editConfigProperties(got, []PropertyEdit{{Operation: "set", Key: key, Value: "x"}})
	if err != nil || same != got || len(changes) != 0 {
		t.Fatalf("could not find literal BOM key after adding it: %q, %v, %v", same, changes, err)
	}
}

func TestEditConfigPropertiesRejectsAmbiguousOrInvalidOperations(t *testing.T) {
	for _, test := range []struct {
		name     string
		original string
		edits    []PropertyEdit
		message  string
	}{
		{
			name: "duplicate encoded key", original: "foo=1\nf\\u006fo=2\n",
			edits: []PropertyEdit{{Operation: "set", Key: "foo", Value: "secret-sentinel"}}, message: "重复",
		},
		{
			name: "duplicate operations", original: "foo=1",
			edits: []PropertyEdit{{Operation: "set", Key: "foo", Value: "secret-sentinel"}, {Operation: "unset", Key: "foo"}}, message: "多项",
		},
		{
			name: "append existing", original: "foo=1",
			edits: []PropertyEdit{{Operation: "add", Key: "foo", Value: "secret-sentinel"}}, message: "已存在",
		},
		{
			name: "set missing", original: "foo=1",
			edits: []PropertyEdit{{Operation: "set", Key: "Foo", Value: "secret-sentinel"}}, message: "不存在",
		},
		{
			name: "atomic failure when unset missing", original: "foo=1\n",
			edits: []PropertyEdit{{Operation: "set", Key: "foo", Value: "secret-sentinel"}, {Operation: "unset", Key: "bar"}}, message: "不存在",
		},
		{
			name: "malformed Unicode", original: "foo=\\uSecret-sentinel\r\n",
			edits: []PropertyEdit{{Operation: "set", Key: "foo", Value: "secret-sentinel"}}, message: "无效转义",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, changes, err := editConfigProperties(test.original, test.edits)
			if err == nil || !strings.Contains(err.Error(), test.message) || strings.Contains(strings.ToLower(err.Error()), "secret-sentinel") || got != "" || len(changes) != 0 {
				t.Fatalf("expected an atomic failure without values, got changes %#v and error %v", changes, err)
			}
		})
	}
}
