package markdownui

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestPublicEditorOrderedContinuationSharesDraftAndUndo(t *testing.T) {
	const source = "\ufeff1. 第一项\r\n2. 第二项😀"
	state := NewEditorState(Document{ID: "writing", Source: source})
	t.Cleanup(state.Close)
	buffer := state.native.Buffer()
	var drafts []Snapshot
	view := ui.NewTester(func(c *ui.Context) {
		MarkdownEditor(c, state, EditorOptions{OnChange: func(value Snapshot) { drafts = append(drafts, value) }}).Fill()
	}, 700, 420)
	at := len(buffer.String())
	state.native.SetSelection(at, at)
	state.native.Focus()
	view.Frame()
	view.Key(0, ui.KeyEnter)
	if got := state.Snapshot().Source; got != source+"\r\n3. " {
		t.Fatalf("public live editor failed to continue ordered list: %q", got)
	}
	if len(drafts) != 1 || drafts[0].Source != state.Snapshot().Source || state.native.Buffer() != buffer {
		t.Fatalf("continuation bypassed shared host draft buffer: %+v", drafts)
	}
	view.Type("第三项")
	if dir := os.Getenv("SILVERWING_MD_WRITING_SCREENSHOT_DIR"); dir != "" {
		view.Frame()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(filepath.Join(dir, "ordered-writing.png"))
		if err != nil {
			t.Fatal(err)
		}
		encodeErr := png.Encode(file, view.Image())
		closeErr := file.Close()
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	view.Command("undo")
	if state.Snapshot().Source != source+"\r\n3. " {
		t.Fatal("typing and the automatic prefix did not retain separate undo steps")
	}
	view.Command("undo")
	if state.Snapshot().Source != source {
		t.Fatal("one undo did not restore all original source bytes")
	}
	view.Command("redo")
	if state.Snapshot().Source != source+"\r\n3. " {
		t.Fatal("redo did not restore the full list continuation")
	}
}

func TestPublicEditorContinuationKeepsReadOnlyAndSourceEscapeHatch(t *testing.T) {
	const source = "1. first\n2. second"
	state := NewEditorState(Document{ID: "writing-modes", Source: source})
	t.Cleanup(state.Close)
	readOnly := true
	view := ui.NewTester(func(c *ui.Context) {
		MarkdownEditor(c, state, EditorOptions{ReadOnly: readOnly}).Fill()
	}, 700, 420)
	state.native.SetSelection(len(source), len(source))
	state.native.Focus()
	view.Frame()
	view.Key(0, ui.KeyEnter)
	view.Key(0, ui.KeyTab)
	if state.Snapshot().Source != source {
		t.Fatal("read-only editor accepted writing aids")
	}
	readOnly = false
	if err := state.SetMode(Source); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	state.native.Focus()
	view.Frame()
	view.Key(0, ui.KeyEnter)
	if state.Snapshot().Source != source+"\n" || strings.Contains(state.Snapshot().Source, "3. ") {
		t.Fatal("explicit source mode did not retain literal input")
	}
}
