package text

import "testing"

// Fixed geometry isolates stop construction from platform font shaping.
func BenchmarkVisualCaretStopsLongLine(b *testing.B) {
	const n = 4096
	l := &Layout{Runes: make([]rune, n), Lines: []Line{{End: n, Width: n * 8, Height: 20, Glyphs: make([]Glyph, n)}}}
	for i := range l.Runes {
		l.Runes[i] = 'a'
		l.Lines[0].Glyphs[i] = Glyph{X: float32(i * 8), Advance: 8, Cluster: i, Runes: 1}
	}
	l.visualStops(0)
	b.ReportAllocs()
	for b.Loop() {
		l.Lines[0].stops = nil
		if got := len(l.visualStops(0)); got != n*2 {
			b.Fatalf("got %d caret edges, want %d", got, n*2)
		}
	}
}
