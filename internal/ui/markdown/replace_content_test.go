package markdownui

import "testing"

func TestPublicConfirmedReplacementPreservesHistorySourceAndGuards(t *testing.T) {
	const before = "old\r\ntrailing  \r\n"
	state, view := dailyPublicEditor(before)
	defer state.Close()
	buffer := state.native.Buffer()
	const after = "\ufeff# template\r\n\r\n中文😀\r\n"
	version := state.Snapshot().Version
	if err := state.ReplaceContent(after, version); err != nil {
		t.Fatal(err)
	}
	if state.Snapshot().Source != after || state.native.Buffer() != buffer {
		t.Fatalf("replacement changed source bytes/owner: %q", state.Snapshot().Source)
	}
	state.native.Focus()
	view.Frame()
	view.Command("undo")
	if state.Snapshot().Source != before {
		t.Fatalf("replacement was not one undoable edit: %q", state.Snapshot().Source)
	}
	view.Command("redo")
	if state.Snapshot().Source != after {
		t.Fatal("replacement redo failed")
	}
	if err := state.ReplaceContent("stale", version); err == nil || state.Snapshot().Source != after {
		t.Fatal("stale replacement overwrote source")
	}
	state.options.ReadOnly = true
	if err := state.ReplaceContent("readonly", state.Snapshot().Version); err == nil {
		t.Fatal("read-only replacement allowed")
	}
}
