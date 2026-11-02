package parser

import "testing"

func TestListItemSourceKeepsActualMarkerAndPaddingColumns(t *testing.T) {
	for _, fixture := range []struct {
		source, marker string
		padding        int
	}{
		{"- item", "-", 2}, {"-\titem", "-", 4}, {"9) item", "9)", 3},
		{"009. item", "009.", 5}, {"> - [x] item", "-", 2}, {"- [x] ", "-", 2},
	} {
		d := Parse(fixture.source)
		found := false
		for i := 1; i < d.Len(); i++ {
			if d.Block(i).Kind != Item {
				continue
			}
			source, ok := d.ListItemSource(i)
			if !ok || d.Source()[source.MarkerStart:source.MarkerEnd] != fixture.marker || source.Padding != fixture.padding {
				t.Fatalf("item source for %q = %+v, ok=%v", fixture.source, source, ok)
			}
			found = true
		}
		if !found {
			t.Fatalf("fixture has no parser-confirmed Item: %q", fixture.source)
		}
		if _, ok := d.ListItemSource(-1); ok {
			t.Fatal("invalid index exposed Item metadata")
		}
		if _, ok := d.ListItemSource(0); ok {
			t.Fatal("Root exposed Item metadata")
		}
	}
}
