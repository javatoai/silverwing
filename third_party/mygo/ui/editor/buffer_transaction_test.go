package editor

import (
	"reflect"
	"testing"
	"time"
)

type transactionSelection struct{ anchor, head int }

func TestBufferTransactionRejectsAtomicLimitAndStaleEdits(t *testing.T) {
	source := "a\r\n中"
	b := NewBufferPreservingLineEndings(source)
	b.SetMaxSourceBytes(len(source) + 1)
	owner := new(int)
	before := transactionSelection{0, 1}
	after := transactionSelection{2, 2}
	initial := b.Snapshot()
	rejected := b.ApplyTransaction(Transaction{
		Version: initial.Version(), Owner: owner, Kind: EditTyping,
		Edits:           []Edit{{Start: 0, End: 0, Text: "x"}, {Start: b.Len(), End: b.Len(), Text: "y"}},
		SelectionBefore: before, SelectionAfter: after,
	})
	if rejected.Applied || !rejected.Rejected || rejected.Stale || rejected.Version != initial.Version() {
		t.Fatalf("limit result = %+v", rejected)
	}
	if b.OriginalText() != source || b.CanUndo() || b.CanRedo() || b.Modified() {
		t.Fatalf("rejection changed source/history: source=%q undo=%v redo=%v", b.OriginalText(), b.CanUndo(), b.CanRedo())
	}
	if changes, snapshot, ok := b.ChangesSince(initial.Version()); !ok || len(changes) != 0 || snapshot.OriginalText() != source {
		t.Fatalf("rejection changed log: changes=%v snapshot=%q ok=%v", changes, snapshot.OriginalText(), ok)
	}
	newline := b.ApplyTransaction(Transaction{Version: b.Version(), Edits: []Edit{{Text: "\r\n"}}})
	if !newline.Rejected || b.Version() != initial.Version() || b.OriginalText() != source {
		t.Fatalf("source limit counted LF projection instead of CRLF: result=%+v source=%q", newline, b.OriginalText())
	}
	accepted := b.ApplyTransaction(Transaction{
		Version: b.Version(), Owner: owner, Kind: EditOther,
		Edits:           []Edit{{Start: 0, End: 0, Text: "x"}},
		SelectionBefore: before, SelectionAfter: after,
	})
	if !accepted.Applied || accepted.Rejected || accepted.Stale {
		t.Fatalf("accepted result = %+v", accepted)
	}
	stale := b.ApplyTransaction(Transaction{Version: initial.Version(), Owner: owner, Edits: []Edit{{Text: "z"}}})
	if stale.Applied || !stale.Stale || stale.Rejected || stale.Version != accepted.Version || b.OriginalText() != "x"+source {
		t.Fatalf("stale result=%+v source=%q", stale, b.OriginalText())
	}
	undone := b.UndoTransaction()
	if !undone.Applied || undone.Owner != owner || undone.Selection != before || b.OriginalText() != source {
		t.Fatalf("undo result=%+v source=%q", undone, b.OriginalText())
	}
	rejected = b.ApplyTransaction(Transaction{Version: b.Version(), Owner: owner, Edits: []Edit{{Text: "xx"}}})
	if !rejected.Rejected || !b.CanRedo() || b.OriginalText() != source {
		t.Fatalf("rejection lost redo: result=%+v source=%q redo=%v", rejected, b.OriginalText(), b.CanRedo())
	}
	redone := b.RedoTransaction()
	if !redone.Applied || redone.Owner != owner || redone.Selection != after || b.OriginalText() != "x"+source {
		t.Fatalf("redo result=%+v source=%q", redone, b.OriginalText())
	}
}

func TestBufferTransactionTypingAndDeletingRestoreGroupedSelections(t *testing.T) {
	for _, kind := range []EditKind{EditTyping, EditDeleting} {
		t.Run(map[EditKind]string{EditTyping: "typing", EditDeleting: "deleting"}[kind], func(t *testing.T) {
			initial, final := "", "ab"
			if kind == EditDeleting {
				initial, final = final, initial
			}
			b := NewBuffer(initial)
			owner := new(int)
			first := transactionSelection{len(initial), len(initial)}
			last := transactionSelection{len(final), len(final)}
			for i := range 2 {
				edit := Edit{Start: i, End: i, Text: string(rune('a' + i))}
				before, after := transactionSelection{i, i}, transactionSelection{i + 1, i + 1}
				if kind == EditDeleting {
					edit = Edit{Start: 1 - i, End: 2 - i}
					before, after = transactionSelection{2 - i, 2 - i}, transactionSelection{1 - i, 1 - i}
				}
				result := b.ApplyTransaction(Transaction{Version: b.Version(), Owner: owner, Kind: kind,
					Edits: []Edit{edit}, SelectionBefore: before, SelectionAfter: after})
				if !result.Applied || result.Rejected || result.Stale {
					t.Fatalf("apply %d = %+v", i, result)
				}
			}
			if b.String() != final {
				t.Fatalf("edited text = %q, want %q", b.String(), final)
			}
			undo := b.UndoTransaction()
			if !undo.Applied || undo.Owner != owner || undo.Selection != first || b.String() != initial || b.CanUndo() {
				t.Fatalf("grouped undo=%+v text=%q remaining=%v", undo, b.String(), b.CanUndo())
			}
			redo := b.RedoTransaction()
			if !redo.Applied || redo.Selection != last || b.String() != final || b.CanRedo() {
				t.Fatalf("grouped redo=%+v text=%q", redo, b.String())
			}
			b.EndUndoGroup()
			result := b.ApplyTransaction(Transaction{Version: b.Version(), Owner: owner, Kind: kind,
				Edits: []Edit{{Start: b.Len(), End: b.Len(), Text: "!"}}, SelectionBefore: last,
				SelectionAfter: transactionSelection{b.Len() + 1, b.Len() + 1}})
			if !result.Applied || !b.UndoTransaction().Applied || b.String() != final || !b.CanUndo() {
				t.Fatal("EndUndoGroup did not separate the next edit")
			}
		})
	}
}

func TestBufferTransactionOwnersAndNoopsKeepHistoryMetadata(t *testing.T) {
	b := NewBuffer("")
	owners := [2]*int{new(int), new(int)}
	for i, owner := range owners {
		result := b.ApplyTransaction(Transaction{Version: b.Version(), Owner: owner, Kind: EditTyping,
			Edits:           []Edit{{Start: i, End: i, Text: "x"}},
			SelectionBefore: transactionSelection{i, i}, SelectionAfter: transactionSelection{i + 1, i + 1}})
		if !result.Applied {
			t.Fatalf("apply %d = %+v", i, result)
		}
	}
	version := b.Version()
	noop := b.ApplyTransaction(Transaction{Version: version, Owner: owners[1], Kind: EditOther,
		Edits: []Edit{{Start: 1, End: 2, Text: "x"}}, SelectionAfter: transactionSelection{0, 0}})
	if !noop.Applied || noop.Rejected || noop.Stale || noop.Version != version || len(noop.Changes) != 0 {
		t.Fatalf("no-op = %+v", noop)
	}
	for i := 1; i >= 0; i-- {
		undo := b.UndoTransaction()
		if !undo.Applied || undo.Owner != owners[i] || undo.Selection != (transactionSelection{i, i}) || b.Len() != i {
			t.Fatalf("owner undo %d = %+v, text=%q", i, undo, b.String())
		}
	}
	for i, owner := range owners {
		redo := b.RedoTransaction()
		if !redo.Applied || redo.Owner != owner || redo.Selection != (transactionSelection{i + 1, i + 1}) {
			t.Fatalf("owner redo %d = %+v", i, redo)
		}
	}
	if noHistory := NewBuffer("").UndoTransaction(); noHistory.Applied || noHistory.Version != 0 || noHistory.Selection != nil {
		t.Fatalf("empty undo = %+v", noHistory)
	}
}

func TestBufferTransactionCompositionRestoresFinalSelection(t *testing.T) {
	b := NewBufferPreservingLineEndings("开\r\n")
	owner := new(int)
	initial := transactionSelection{3, 3}
	b.EndUndoGroup()
	result := b.ApplyTransaction(Transaction{Version: b.Version(), Owner: owner, Kind: EditComposition,
		Edits: []Edit{{Start: 3, End: 3, Text: "n"}}, SelectionBefore: initial,
		SelectionAfter: transactionSelection{3, 4}})
	if !result.Applied {
		t.Fatalf("start composition = %+v", result)
	}
	b.mu.Lock()
	b.lastEdit = time.Now().Add(-time.Hour)
	b.mu.Unlock()
	result = b.ApplyTransaction(Transaction{Version: b.Version(), Owner: owner, Kind: EditComposition,
		Edits: []Edit{{Start: 3, End: 4, Text: "你"}}, SelectionBefore: transactionSelection{3, 4},
		SelectionAfter: transactionSelection{3, 6}})
	if !result.Applied {
		t.Fatalf("update composition = %+v", result)
	}
	version := result.Version
	final := transactionSelection{6, 6}
	result = b.ApplyTransaction(Transaction{Version: version, Owner: owner, Kind: EditComposition,
		Edits: []Edit{{Start: 3, End: 6, Text: "你"}}, SelectionBefore: transactionSelection{3, 6}, SelectionAfter: final})
	if !result.Applied || result.Version != version {
		t.Fatalf("same-text commit = %+v", result)
	}
	b.EndUndoGroup()
	if undo := b.UndoTransaction(); !undo.Applied || undo.Selection != initial || b.OriginalText() != "开\r\n" || b.CanUndo() {
		t.Fatalf("composition undo=%+v source=%q", undo, b.OriginalText())
	}
	if redo := b.RedoTransaction(); !redo.Applied || redo.Selection != final || b.OriginalText() != "开你\r\n" {
		t.Fatalf("composition redo=%+v source=%q", redo, b.OriginalText())
	}
}

func TestBufferTransactionSelectionAfterUsesCandidateProjection(t *testing.T) {
	b := NewBufferPreservingLineEndings("a\r")
	owner := new(int)
	initial := b.Snapshot()
	before := transactionSelection{2, 2}
	var candidate Snapshot
	result := b.ApplyTransaction(Transaction{Version: initial.Version(), Owner: owner, Kind: EditComposition,
		Edits: []Edit{{Start: 2, End: 2, Text: "\n中"}}, SelectionBefore: before,
		SelectionAfter: transactionSelection{6, 6},
		SelectionAfterEdit: func(snapshot Snapshot, changes []Change) any {
			candidate = snapshot
			head := changes[len(changes)-1].NewEnd
			// The callback owns its copy of public changes.
			changes[0].OldStart = 999
			return transactionSelection{head, head}
		}})
	if !result.Applied || result.Version != initial.Version()+1 || candidate.Version() != result.Version ||
		candidate.OriginalText() != "a\r\n中" || candidate.String() != "a\n中" || result.Changes[0].OldStart != 2 {
		t.Fatalf("result=%+v candidate version=%d source=%q LF=%q", result, candidate.Version(), candidate.OriginalText(), candidate.String())
	}
	if undo := b.UndoTransaction(); !undo.Applied || undo.Selection != before || b.OriginalText() != "a\r" {
		t.Fatalf("projection undo=%+v source=%q", undo, b.OriginalText())
	}
	if redo := b.RedoTransaction(); !redo.Applied || redo.Selection != (transactionSelection{5, 5}) || b.OriginalText() != "a\r\n中" {
		t.Fatalf("projection redo=%+v source=%q", redo, b.OriginalText())
	}
}

func TestBufferTransactionSelectionCallbackPanicDoesNotPublish(t *testing.T) {
	b := NewBufferPreservingLineEndings("a\r\nb")
	owner := new(int)
	selection := transactionSelection{1, 1}
	b.ApplyTransaction(Transaction{Version: b.Version(), Owner: owner, Edits: []Edit{{Text: "x"}}, SelectionAfter: selection})
	b.UndoTransaction()
	before := b.Snapshot()
	panicked := false
	func() {
		defer func() { panicked = recover() == "selection failure" }()
		b.ApplyTransaction(Transaction{Version: before.Version(), Owner: owner, Edits: []Edit{{Text: "中\r\n"}},
			SelectionAfterEdit: func(Snapshot, []Change) any { panic("selection failure") }})
	}()
	changes, after, ok := b.ChangesSince(before.Version())
	if !panicked || !ok || len(changes) != 0 || after.Version() != before.Version() ||
		after.OriginalText() != before.OriginalText() || b.CanUndo() || !b.CanRedo() || b.Modified() {
		t.Fatalf("panic published state: panic=%v changes=%v version=%d source=%q undo=%v redo=%v", panicked, changes, after.Version(), after.OriginalText(), b.CanUndo(), b.CanRedo())
	}
	if redo := b.RedoTransaction(); !redo.Applied || redo.Owner != owner || redo.Selection != selection || b.OriginalText() != "xa\r\nb" {
		t.Fatalf("panic changed redo: result=%+v source=%q", redo, b.OriginalText())
	}
	called := false
	callback := func(Snapshot, []Change) any { called = true; return nil }
	stale := b.ApplyTransaction(Transaction{Version: before.Version(), Edits: []Edit{{Text: "x"}}, SelectionAfterEdit: callback})
	if !stale.Stale || called {
		t.Fatalf("stale callback result=%+v called=%v", stale, called)
	}
	b.SetMaxSourceBytes(len(b.OriginalText()))
	rejected := b.ApplyTransaction(Transaction{Version: b.Version(), Edits: []Edit{{Text: "x"}}, SelectionAfterEdit: callback})
	if !rejected.Rejected || called {
		t.Fatalf("rejected callback result=%+v called=%v", rejected, called)
	}
}

func TestBufferChangesSinceIsAtomicAndDoesNotExposeInternalLog(t *testing.T) {
	b := NewBufferPreservingLineEndings("a\r\nb\rc")
	initial := b.Snapshot()
	b.Edit(Edit{Start: 0, End: 1, Text: "甲\n乙"})
	b.Edit(Edit{Start: b.Len(), End: b.Len(), Text: "尾"})
	changes, snapshot, ok := b.ChangesSince(initial.Version())
	want := [][]Change{{{OldStart: 0, OldEnd: 1, NewStart: 0, NewEnd: len("甲\n乙")}},
		{{OldStart: len("甲\n乙\nb\nc"), OldEnd: len("甲\n乙\nb\nc"), NewStart: len("甲\n乙\nb\nc"), NewEnd: len("甲\n乙\nb\nc尾")}}}
	if !ok || !reflect.DeepEqual(changes, want) || snapshot.Version() != b.Version() || snapshot.OriginalText() != "甲\n乙\r\nb\rc尾" {
		t.Fatalf("changes=%v snapshot=%q version=%d ok=%v", changes, snapshot.OriginalText(), snapshot.Version(), ok)
	}
	changes[0][0].OldEnd = 999
	if again, _, _ := b.ChangesSince(initial.Version()); !reflect.DeepEqual(again, want) {
		t.Fatalf("caller mutated internal changes: %v", again)
	}
	if changes, same, ok := b.ChangesSince(snapshot.Version()); !ok || len(changes) != 0 || same.Version() != snapshot.Version() {
		t.Fatalf("same version: changes=%v version=%d ok=%v", changes, same.Version(), ok)
	}
	if changes, current, ok := b.ChangesSince(snapshot.Version() + 1); ok || len(changes) != 0 || current.Version() != snapshot.Version() {
		t.Fatalf("future version: changes=%v version=%d ok=%v", changes, current.Version(), ok)
	}
	for range logMax + 2 {
		b.Edit(Edit{Text: "x"})
	}
	if changes, current, ok := b.ChangesSince(initial.Version()); ok || len(changes) != 0 || current.Version() != b.Version() || current.OriginalText() != b.OriginalText() {
		t.Fatalf("expired version: changes=%v version=%d ok=%v", changes, current.Version(), ok)
	}
	if snapshot.OriginalText() != "甲\n乙\r\nb\rc尾" || initial.OriginalText() != "a\r\nb\rc" {
		t.Fatal("later edits mutated an earlier source snapshot")
	}
}
