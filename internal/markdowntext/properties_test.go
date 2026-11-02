package markdowntext

import (
	"slices"
	"strings"
	"testing"
)

func propertyCandidate(source string, edits []PropertySourceEdit) string {
	slices.SortFunc(edits, func(a, b PropertySourceEdit) int { return b.Start - a.Start })
	for _, e := range edits {
		source = source[:e.Start] + e.Text + source[e.End:]
	}
	return source
}

func TestPropertiesTagEditsPreserveUnrelatedSource(t *testing.T) {
	for _, test := range []struct {
		name, tags string
		action     TagAction
		index      int
		value      string
		want       []string
	}{
		{"block-rename", "tags:\n  - 业务/订单支付 # keep\n  - 环境/prod\n", RenameTag, 0, "业务/签约", []string{"业务/签约", "环境/prod"}},
		{"block-add", "tags:\n- 业务/订单支付\n", AddTag, 0, "平台/android", []string{"业务/订单支付", "平台/android"}},
		{"block-remove-comment", "tags: # group\n  - '业务/支付' # keep\n", RemoveTag, 0, "", nil},
		{"flow-add-tail-comma", "tags: ['业务/支付', ] # keep\n", AddTag, 0, "平台/android", []string{"业务/支付", "平台/android"}},
		{"flow-remove-first", "tags: ['类型/问题', '业务/支付']\n", RemoveTag, 0, "", []string{"业务/支付"}},
		{"flow-remove-last", "tags: ['类型/问题', '业务/支付',]\n", RemoveTag, 1, "", []string{"类型/问题"}},
		{"null", "tags: null # keep\n", AddTag, 0, "业务/支付", []string{"业务/支付"}},
		{"missing", "", AddTag, 0, "业务/支付", []string{"业务/支付"}},
		{"scalar", "'tags': '业务/支付' # keep\n", AddTag, 0, "工具/sls", []string{"业务/支付", "工具/sls"}},
		{"unicode-quotes", "\"tags\": [\"中文😀/a,b]c\", '业务/支付']\n", RenameTag, 1, "工具/日志", []string{"中文😀/a,b]c", "工具/日志"}},
	} {
		for _, eol := range []string{"\n", "\r\n"} {
			t.Run(test.name+eol, func(t *testing.T) {
				before := "\ufeff---\n# untouched header\n" + test.tags + "created_at: '2026-08-28 11:30:39'\nunknown: &config\n  nested: [one, two]\nai_thread_id: abc-def\n---\n\n# 正文\n正文 <!-- keep -->\n"
				before = strings.ReplaceAll(before, "\n", eol)
				p := ParseProperties(before)
				if p == nil {
					t.Fatal("mapping not recognized")
				}
				edits, err := p.TagEdits(test.action, test.index, test.value)
				if err != nil {
					t.Fatal(err)
				}
				after := propertyCandidate(before, edits)
				next := ParseProperties(after)
				if next == nil || !slices.Equal(next.Tags, test.want) {
					t.Fatalf("tags=%v want=%v", next, test.want)
				}
				if after[next.End:] != before[p.End:] || !strings.Contains(after, strings.ReplaceAll("created_at: '2026-08-28 11:30:39'\nunknown: &config\n  nested: [one, two]\nai_thread_id: abc-def\n", "\n", eol)) {
					t.Fatal("unrelated source rewritten")
				}
				if strings.Contains(test.tags, "# keep") && !strings.Contains(after, "# keep") {
					t.Fatal("tag comment lost")
				}
				if eol == "\r\n" && strings.Contains(strings.ReplaceAll(after, "\r\n", ""), "\n") {
					t.Fatal("introduced LF into CRLF source")
				}
			})
		}
	}
}
func TestPropertiesRejectAmbiguousTagEdits(t *testing.T) {
	for _, source := range []string{"---\ntags: [a\n---\nbody", "---\ntags: [a]\ntags: [b]\n---\nbody", "---\nnot closed"} {
		if ParseProperties(source) != nil {
			t.Fatal("invalid metadata accepted", source)
		}
	}
	for _, tags := range []string{"tags: &tags [a]", "tags: [1, a]", "tags: |\n  a\n  b", "tags: [[a]]", "tags: [\"line one\n line two\"]"} {
		p := ParseProperties("---\n" + tags + "\n---\nbody")
		if p == nil {
			continue
		}
		if _, err := p.TagEdits(AddTag, 0, "新标签"); err == nil {
			t.Fatal("unsafe tag edit accepted", tags)
		}
	}
	p := ParseProperties("---\ntags: [业务/支付]\n---\nbody")
	for _, value := range []string{"", "业务 支付", "业务//支付", "业务/支付"} {
		if _, err := p.TagEdits(AddTag, 0, value); err == nil {
			t.Fatal("invalid/duplicate tag accepted", value)
		}
	}
}

func TestPropertiesReviewUnicodeLinesTailCommaCustomTagAndCommentBytes(t *testing.T) {
	for _, line := range []string{"\u0085", "\u2028", "\u2029"} {
		if ParseProperties("---\nfirst: [a]"+line+"tags: [b]\n---\nbody") != nil {
			t.Fatal("unsupported YAML physical line accepted")
		}
	}
	p := ParseProperties("---\ntags: [a,] # keep\n---\nbody")
	edits, err := p.TagEdits(RemoveTag, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	after := propertyCandidate(p.Source, edits)
	if !strings.Contains(after, "tags: [] # keep") {
		t.Fatal(after)
	}
	p = ParseProperties("---\ntags: !custom\n  - a\n---\nbody")
	if p == nil || p.TagsEditable {
		t.Fatal("custom sequence editable")
	}
	if _, err := p.TagEdits(AddTag, 0, "新"); err == nil {
		t.Fatal("custom tags mutation accepted")
	}
	p = ParseProperties("---\ntags:\n  - a # keep trailing   \n---\nbody")
	edits, err = p.TagEdits(RemoveTag, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(propertyCandidate(p.Source, edits), "# keep trailing   \n") {
		t.Fatal("comment trailing bytes lost")
	}
}
