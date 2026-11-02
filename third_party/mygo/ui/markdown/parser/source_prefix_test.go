package parser

import (
	"strings"
	"testing"
)

func TestOpaqueSourcePrefixKeepsAbsoluteBodyOffsetsAndPolicy(t *testing.T) {
	source := "\ufeff---\ntags: [a]\n[fake]: https://wrong.test\n---\n# 正文\n\n[fake]\n"
	end := strings.Index(source, "# 正文")
	d := New(Default)
	d.SetWithPolicy(source, end, nil)
	if d.Source() != source || d.Block(1).Kind != SourcePrefix || d.Block(1).End != end || d.Block(2).Start != end {
		t.Fatal("source offsets changed")
	}
	for i := 1; i < d.Len(); i++ {
		for _, in := range d.Inlines(i) {
			if in.Kind == Link {
				t.Fatal("opaque metadata defined a body reference")
			}
		}
	}
	changed := strings.Replace(source, "[a]", "[longer]", 1)
	end += len("longer") - 1
	d.SetWithPolicy(changed, end, nil)
	if d.Block(1).End != end || d.Block(2).Start != end {
		t.Fatal("prefix edit reused stale body offsets")
	}
	d.Set(changed)
	if d.Block(1).Kind == SourcePrefix {
		t.Fatal("standard parser retained opt-in policy")
	}
	d.SetWithPolicy(changed, end, []int{end})
	if d.Block(1).Kind != SourcePrefix {
		t.Fatal("literal-fence policy lost opaque prefix")
	}
}
