package parser

import (
	"strings"
	"testing"
)

func TestCodeSourceUsesParsedBodyAndRawLanguageBoundaries(t *testing.T) {
	for _, fixture := range []struct {
		source, language, rawLanguage, close string
	}{
		{"```sql&#x20;title=x\nx\n```", "sql", "sql", "```"},
		{"~~~  s&#113;l&#9;title=x  \r\nx\r\n~~~~~  \r\n", "sql", "s&#113;l", "~~~~~"},
		{"```sql&Tab;title=x\nx\n```", "sql", "sql", "```"},
		{"```sql\n>```\n", "sql", "sql", ""},
		{"```sql\n    ```\n", "sql", "sql", ""},
		{"> ```sql\n> x\n> \t```\n", "sql", "sql", "```"},
		{"> > ```sql\n> > x\n> ```\n", "sql", "sql", ""},
		{"```sql", "sql", "sql", ""},
		{"```\n```", "", "", "```"},
	} {
		t.Run(fixture.source, func(t *testing.T) {
			d := Parse(fixture.source)
			index := -1
			for i := 1; i < d.Len(); i++ {
				if d.Block(i).Kind == CodeBlock && d.Block(i).Language() == fixture.language {
					index = i
					break
				}
			}
			syntax, ok := d.CodeSource(index)
			if !ok || d.Source()[syntax.LanguageStart:syntax.LanguageEnd] != fixture.rawLanguage {
				t.Fatalf("raw language range = %+v, ok=%v", syntax, ok)
			}
			if fixture.close == "" {
				if syntax.ClosingStart != -1 {
					t.Fatalf("literal body or container termination was treated as a closer: %+v", syntax)
				}
			} else if syntax.ClosingStart < 0 || d.Source()[syntax.ClosingStart:syntax.ClosingEnd] != fixture.close || syntax.ClosingStart != strings.LastIndex(fixture.source, fixture.close) {
				t.Fatalf("actual closing fence range = %+v", syntax)
			}
		})
	}
	for _, source := range []string{"    code", "paragraph"} {
		if syntax, ok := Parse(source).CodeSource(1); ok {
			t.Fatalf("non-fenced block exposed syntax ranges: %+v", syntax)
		}
	}
}
