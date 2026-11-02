package markdownui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestEditorHeadingNavigationPreservesBufferModeSourceAndHistory(t *testing.T) {
	for _, mode := range []Mode{Live, Source, Preview} {
		t.Run(fmt.Sprintf("mode-%d", mode), func(t *testing.T) {
			source := "# Original\r\n\r\n" + strings.Repeat("filler paragraph\r\n\r\n", 50) + "# API v2 — **notes**!\r\n\r\nfirst heading\r\n\r\n# API v2 — **notes**!\r\n\r\nsecond heading\r\n"
			state := NewEditorState(Document{ID: "heading", Source: source})
			defer state.Close()
			buffer := state.NativeState().Buffer()
			if err := state.NativeState().ReplaceRange(0, 0, "draft\r\n\r\n", buffer.Version()); err != nil {
				t.Fatal(err)
			}
			if err := state.SetMode(mode); err != nil {
				t.Fatal(err)
			}
			view := ui.NewTester(func(c *ui.Context) { MarkdownEditor(c, state, EditorOptions{}).Fill() }, 700, 350)
			before := state.Snapshot()
			if err := state.RevealLocation(0, 0, "api-v2--notes"); err != nil {
				t.Fatal(err)
			}
			view.Frame()
			want := strings.Index(buffer.Snapshot().String(), "# API")
			if a, z := state.NativeState().Selection(); a != want || z != want {
				t.Fatalf("heading selected %d:%d, want first matching heading at %d", a, z, want)
			}
			if state.NativeState().Buffer() != buffer || state.Mode() != mode || state.Snapshot() != before {
				t.Fatal("navigation replaced the buffer, mode, version or original bytes")
			}
			if mode == Preview && state.reader.scroll.Y <= 0 {
				t.Fatal("preview heading was not revealed in the viewport")
			}
			if !buffer.Undo() || buffer.OriginalText() != source {
				t.Fatalf("navigation lost the existing undo history: %q", buffer.OriginalText())
			}
		})
	}
}

func TestEditorLocationUsesNormalizedUnicodeColumnsAndRejectsMissingHeading(t *testing.T) {
	state := NewEditorState(Document{ID: "location", Source: "first\r\n甲😀乙\r\nlast"})
	defer state.Close()
	if err := state.RevealLocation(2, 3, ""); err != nil {
		t.Fatal(err)
	}
	want := len("first\n甲😀")
	if a, z := state.NativeState().Selection(); a != want || z != want {
		t.Fatalf("Unicode column selected %d:%d, want %d", a, z, want)
	}
	before := state.Snapshot()
	if err := state.RevealLocation(0, 0, "missing-heading"); err == nil {
		t.Fatal("missing heading accepted")
	}
	if a, z := state.NativeState().Selection(); a != want || z != want || state.Snapshot() != before {
		t.Fatal("missing heading changed the selection or document")
	}
	state.Close()
	if err := state.RevealLocation(2, 1, ""); err != ErrClosed {
		t.Fatalf("closed editor navigation: %v", err)
	}
}
