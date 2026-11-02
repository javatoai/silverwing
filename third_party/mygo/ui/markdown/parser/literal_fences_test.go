package parser

import (
	"strings"
	"testing"
)

func TestLiteralFencePolicyUsesOriginalOffsetsAndDoesNotSuppressCloser(t *testing.T) {
	const source = "before\n\n```sql\n\n```python\nx\n```\n\nafter"
	d := New(Default)
	at := strings.Index(source, "```sql")
	d.SetWithLiteralFenceLines(source, []int{at})
	if d.Source() != source {
		t.Fatal("policy rewrote source")
	}
	count := 0
	for i := 1; i < d.Len(); i++ {
		if d.Block(i).Kind == CodeBlock {
			count++
			if d.Block(i).Language() != "python" {
				t.Fatal("draft consumed following card")
			}
			syntax, _ := d.CodeSource(i)
			if syntax.ClosingStart < 0 {
				t.Fatal("closer lost")
			}
		}
	}
	if count != 1 {
		t.Fatal("following code missing")
	}
	d.Set(source)
	if d.Block(2).Kind != CodeBlock || d.Block(2).Language() != "sql" {
		t.Fatal("same-source policy change did not reparse")
	}
	const closed = "```sql\nx\n```"
	d.SetWithLiteralFenceLines(closed, []int{strings.LastIndex(closed, "```")})
	syntax, _ := d.CodeSource(1)
	if syntax.ClosingStart < 0 {
		t.Fatal("opening-only policy suppressed real closer")
	}
}
