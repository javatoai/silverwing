package editor

import (
	"math/rand/v2"
	"strings"
	"testing"
)

func TestBufferPreservingMixedEndingsUndoRedo(t *testing.T) {
	random := rand.New(rand.NewPCG(2026, 1119))
	pieces := []string{"", "a", "界", "\r", "\n", "\r\n", "\r\r\n", "x\ny\r\nz"}
	for range 100 {
		source := "甲\r\nb\rc\n尾"
		b := NewBufferPreservingLineEndings(source)
		versions := []string{source}
		for range 20 {
			// Compute source boundaries independently of the buffer's rope.
			boundaries := []int{0}
			var logical []int
			for i, r := range source {
				if i > 0 && (r != '\n' || source[i-1] != '\r') {
					boundaries = append(boundaries, i)
				}
			}
			if len(source) != 0 {
				boundaries = append(boundaries, len(source))
			}
			for _, boundary := range boundaries {
				logical = append(logical, len(normalizeSnapshotSource(source[:boundary])))
			}
			start := random.IntN(len(boundaries))
			end := start + random.IntN(len(boundaries)-start)
			replacement := pieces[random.IntN(len(pieces))]
			b.Edit(Edit{Start: logical[start], End: logical[end], Text: replacement})
			next := source[:boundaries[start]] + replacement + source[boundaries[end]:]
			if next != source {
				versions = append(versions, next)
			}
			source = next
			checkPreservingSource(t, b, source)
		}
		for i := len(versions) - 2; i >= 0; i-- {
			if !b.Undo() {
				t.Fatal("missing undo step")
			}
			checkPreservingSource(t, b, versions[i])
		}
		if b.CanUndo() || b.Modified() {
			t.Fatal("undo did not restore the original version")
		}
		for _, source := range versions[1:] {
			if !b.Redo() {
				t.Fatal("missing redo step")
			}
			checkPreservingSource(t, b, source)
		}
	}
}

func TestBufferPreservingRemoteSourceOnlyEdit(t *testing.T) {
	b := NewBufferPreservingLineEndings("a\r\nb\rc\n")
	b.Edit(Edit{Start: 1, End: 2, Text: "\n"})
	b.EditRemote(Edit{Start: b.Len(), End: b.Len(), Text: "R\r\n"})
	if !b.Undo() {
		t.Fatal("unrelated remote edit erased exact undo")
	}
	checkPreservingSource(t, b, "a\r\nb\rc\nR\r\n")
	if !b.Redo() {
		t.Fatal("unrelated remote edit erased exact redo")
	}
	checkPreservingSource(t, b, "a\nb\rc\nR\r\n")
	version := b.Version()
	b.EditRemote(Edit{Start: 1, End: 2, Text: "\r"})
	changes, _, ok := b.ChangesSince(version)
	if !ok || len(changes) != 1 || len(changes[0]) != 0 || b.CanUndo() {
		t.Fatalf("raw-only remote change log %v, available %v, can undo %v", changes, ok, b.CanUndo())
	}
	checkPreservingSource(t, b, "a\rb\rc\nR\r\n")
}

func TestBufferPreservingRemoteLineEndingKeepsHistoryCaret(t *testing.T) {
	for _, dropStep := range []bool{false, true} {
		b := NewBufferPreservingLineEndings("a\nb")
		caret := []sel{{anchor: 1, head: 1, goal: -1}}
		b.ApplyTransaction(Transaction{Version: b.Version(), Edits: []Edit{{Start: b.Len(), End: b.Len(), Text: "x"}},
			SelectionBefore: caret, SelectionAfter: caret})
		if dropStep {
			b.Edit(Edit{Start: 1, End: 2, Text: "\r\n"})
		}
		b.EditRemote(Edit{Start: 1, End: 2, Text: "\r"})
		result := b.UndoTransaction()
		selection, ok := result.Selection.([]sel)
		if !result.Applied || !ok || len(selection) != 1 || selection[0].head != 1 {
			t.Fatalf("drop step %v, undo selection = %+v", dropStep, result.Selection)
		}
		checkPreservingSource(t, b, "a\rb")
	}
}

func TestBufferPreservingMoveLinesKeepsAnEmptyLine(t *testing.T) {
	const source = "a\rb\n\nc"
	ct := newEditorTest(t, "", "")
	b := NewBufferPreservingLineEndings(source)
	ct.st.Buffer = b
	ct.tt.Frame()
	ct.st.Select(0, 0)
	ct.tt.Frame()
	ct.run(MoveLinesDown)
	checkPreservingSource(t, b, "b\na\r\r\nc")
	ct.run(Undo)
	checkPreservingSource(t, b, source)
	ct.run(Redo)
	checkPreservingSource(t, b, "b\na\r\r\nc")
}

func checkPreservingSource(t *testing.T, b *Buffer, want string) {
	t.Helper()
	s := b.Snapshot()
	if s.OriginalText() != want || s.String() != normalizeSnapshotSource(want) {
		t.Fatalf("source %q, text %q, want the projection of %q", s.OriginalText(), s.String(), want)
	}
	var rebuilt strings.Builder
	if _, err := writeText(s.t, s.ends, &rebuilt); err != nil || rebuilt.String() != want {
		t.Fatalf("line codes reconstruct %q (error %v), want %q", rebuilt.String(), err, want)
	}
	checkEnds(t, b)
}
