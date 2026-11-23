package editor

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// Backspace takes combining marks one at a time, but not the selectors of
// an emoji or an ideograph.
func TestBackspaceCombiningMarks(t *testing.T) {
	ct := newEditorTest(t, "", "นี้|")
	ct.key(0, ui.KeyBackspace)
	ct.want("นี|")
	ct.key(0, ui.KeyBackspace)
	ct.want("น|")
	ct.key(0, ui.KeyBackspace)
	ct.want("|")
	for _, c := range []struct{ from, want string }{
		{"ทำ|", "ท|"},
		{"❤️|", "|"},
		{"葛\U000E0100|", "|"},
	} {
		ct := newEditorTest(t, "", c.from)
		ct.key(0, ui.KeyBackspace)
		ct.want(c.want)
	}
}

// A click never puts the caret between a letter and its marks, not even
// past the end of a wrapped row.
func TestClickBetweenGraphemes(t *testing.T) {
	ct := newEditorTest(t, "", "กนี้ดีx")
	ok := map[int]bool{0: true, 3: true, 12: true, 18: true, 19: true}
	for x := float32(0); x < 120; x++ {
		if off := ct.st.ed.offsetAt(x, 5); !ok[off] {
			t.Errorf("x=%v: offset %d is inside a cluster", x, off)
		}
	}
	ct = newEditorTest(t, "", strings.Repeat("นี้", 400))
	ct.st.SoftWrap = true
	ct.tt.Frame()
	if off := ct.st.ed.offsetAt(590, 5); off%9 != 0 {
		t.Fatalf("clicked past the end of a row at %d, inside a cluster", off)
	}
}
