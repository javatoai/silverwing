package parser

// ListItemSource describes the marker bytes and the columns actually consumed
// for an Item. Indent precedes its marker within its container; Padding includes
// the marker and following gap, excluding an optional task checkbox.
type ListItemSource struct {
	MarkerStart, MarkerEnd int
	Indent, Padding        int
}

// ListItemSource returns only parser-confirmed list items, never text that just
// resembles a marker. Invalid indexes and other block kinds return false.
func (d *Document) ListItemSource(i int) (ListItemSource, bool) {
	if i <= 0 || i >= len(d.blocks) || d.blocks[i].Kind != Item {
		return ListItemSource{}, false
	}
	b := &d.blocks[i]
	end := b.Start
	if b.Ordered {
		for end < len(d.src) && d.src[end] >= '0' && d.src[end] <= '9' {
			end++
		}
	}
	end++ // the parser already confirmed the bullet or ordered delimiter
	return ListItemSource{b.Start, end, int(b.parse.markerOffset), int(b.parse.padding)}, true
}
