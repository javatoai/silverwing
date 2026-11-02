package parser

import "testing"

func TestLiteralLinkDestinationRoundTrip(t *testing.T) {
	for _, target := range []string{"https://example.com/a&copy;?x=1&y=2", "doc.assets/a&copy;.png", `D:\tasks\a[1]&copy;.java:75`, "资料/空 格&#32;.png"} {
		d := Parse("[label](<" + EscapeLinkDestination(target) + ">)")
		found := false
		for b := 1; b < d.Len(); b++ {
			for _, in := range d.Inlines(b) {
				if in.Kind == Link && in.Dest == target {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("literal destination changed: %q => %q", target, d.Source())
		}
	}
}
