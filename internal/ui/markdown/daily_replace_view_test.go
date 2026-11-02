package markdownui

import (
	"github.com/egoist/mygo/ui"
	"strings"
	"testing"
)

func TestDailyReplaceAllOnlyCurrentOriginalSourceAndOneUndo(t *testing.T) {
	const source = "\ufeff# Needle\r\n\r\nNeedle 中文😀 [label](https://example.com/needle)\r\n\r\n<!-- untouched -->"
	s, v := dailyPublicEditor(source)
	find := s.reader.find
	find.Query = "needle"
	other := NewReaderState(Document{ID: "other", Source: "needle in another document"})
	other.find = find
	find.update("other", []findMatch{{documentID: "other", start: 0, end: 6}})
	if err := s.ReplaceAll("替换😀"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(s.Snapshot().Source), "needle") || other.Snapshot().Source != "needle in another document" || !strings.HasSuffix(s.Snapshot().Source, "\r\n\r\n<!-- untouched -->") {
		t.Fatal("replace scope/EOL failed")
	}
	v.Command("undo")
	if s.Snapshot().Source != source {
		t.Fatal("replace all not one undo")
	}
}

func TestDailyViewStateRestorePreservesSelectionScrollAndExplicitNavigationWins(t *testing.T) {
	s, v := dailyPublicEditor(strings.Repeat("paragraph 中文😀\n\n", 100))
	selectionStart := len("paragraph 中文😀\n\n") * 10
	selectionEnd := selectionStart + len("paragraph 中文😀")
	s.native.SetSelection(selectionStart, selectionEnd)
	v.Frame()
	state := s.GetViewState()
	state.ScrollY = 500
	source, version := s.Snapshot().Source, s.Snapshot().Version
	if err := s.RestoreViewState(state); err != nil {
		t.Fatal(err)
	}
	v.Frame()
	v.Frame()
	got := s.GetViewState()
	if got.Anchor != selectionStart || got.Caret != selectionEnd || got.ScrollY < 490 {
		t.Fatalf("restore revealed caret instead of saved viewport: %+v", got)
	}
	if s.Snapshot().Source != source || s.Snapshot().Version != version {
		t.Fatal("view restore changed source")
	}
	if err := s.RestoreViewState(state); err != nil {
		t.Fatal(err)
	}
	s.native.SetSelection(0, 0)
	s.native.Focus()
	v.Frame()
	v.Frame()
	if a, z := s.native.Selection(); a != 0 || z != 0 {
		t.Fatal("restore overwrote explicit selection")
	}
	state.SourceHash = "stale"
	if err := s.RestoreViewState(state); err != ErrStale {
		t.Fatal("stale content accepted")
	}
	s.native.SetSelection(0, 0)
	v.Frame()
	v.Key(ui.Ctrl, ui.KeyEnd)
}
