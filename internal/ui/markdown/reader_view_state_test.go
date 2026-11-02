package markdownui

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	code "github.com/egoist/mygo/ui/editor"
)

func TestReaderViewStateRoundTripAcrossResourceRelease(t *testing.T) {
	document := Document{ID: "task-files:a:note", Path: "note.md", Source: strings.Repeat("line with content\n\n", 300)}
	state := NewReaderState(document)
	state.scroll.X, state.scroll.Y, state.zoom = 12, 180, 1.5
	state.SetMode(Source)
	state.ensureSourceView()
	state.sourceView.SetScrollPosition(code.ScrollPosition{X: 24, Y: 240})
	position := state.GetViewState()
	state.Close()
	restored := NewReaderState(document)
	defer restored.Close()
	if err := restored.RestoreViewState(position); err != nil {
		t.Fatal(err)
	}
	if got := restored.GetViewState(); got != position {
		t.Fatalf("reader lost an independent viewport on reopen: got %+v want %+v", got, position)
	}
	restored.ensureSourceView()
	if got := restored.sourceView.ScrollPosition(); got.X != 24 || got.Y != 240 || !restored.sourceView.ReadOnly || restored.sourceView.Buffer.OriginalText() != document.Source {
		t.Fatal("source viewport was not applied before its first layout", got)
	}
	restored.SetMode(Preview)
	if got := restored.GetViewState(); got.ScrollX != 12 || got.ScrollY != 180 || got.SourceScrollY != 240 {
		t.Fatal("rendered/source viewport states were conflated", got)
	}
}

func TestReaderViewStateRejectsStaleClosedAndInvalidWithoutMutation(t *testing.T) {
	state := NewReaderState(Document{ID: "task-a", Source: "unchanged\r\ntext"})
	defer state.Close()
	before := state.GetViewState()
	for _, kind := range []string{"identity", "live", "nan", "inf", "zoom"} {
		t.Run(kind, func(t *testing.T) {
			value := before
			switch kind {
			case "identity":
				value.DocumentID = "task-b"
			case "live":
				value.Mode = Live
			case "nan":
				value.SourceScrollY = float32(math.NaN())
			case "inf":
				value.ScrollX = float32(math.Inf(1))
			case "zoom":
				value.Zoom = 0
			}
			if err := state.RestoreViewState(value); err == nil || state.GetViewState() != before || state.document.Source != "unchanged\r\ntext" {
				t.Fatal("invalid restore changed the readonly document or viewport", err)
			}
		})
	}
	state.Close()
	if err := state.RestoreViewState(before); !errors.Is(err, ErrClosed) {
		t.Fatal("closed reader accepted a position", err)
	}
}

func TestReaderViewStateExplicitRevealWinsAfterRestore(t *testing.T) {
	state := NewReaderState(Document{ID: "task-file", Source: strings.Repeat("read-only line\n", 200)})
	defer state.Close()
	position := state.GetViewState()
	position.Mode, position.SourceScrollY = Source, 900
	if err := state.RestoreViewState(position); err != nil {
		t.Fatal(err)
	}
	state.RevealLocation(2, "")
	_ = ui.NewTester(func(c *ui.Context) { MarkdownReader(c, state, ReaderOptions{}).Fill() }, 600, 300)
	selection := state.sourceView.Selection()
	if selection.Anchor != len("read-only line\n") || selection.Head != selection.Anchor || !state.sourceView.ReadOnly {
		t.Fatal("restored viewport overrode an explicit file location", selection)
	}
}
