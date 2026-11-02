package editor

// EditKind determines how consecutive transactions share an undo step.
type EditKind uint8

const (
	EditOther EditKind = iota
	EditTyping
	EditDeleting
	// EditComposition groups an input method's updates and commit regardless
	// of the time between them, until EndUndoGroup is called.
	EditComposition
)

// Transaction applies edits made for one version of the LF text. Offsets are
// UTF-8 bytes in that version, before any of the edits. Owner identifies the
// controller for undo grouping and must be comparable, usually a pointer.
// SelectionBefore and SelectionAfter are opaque, immutable values owned by
// that controller; the buffer retains them without interpreting or copying them.
type Transaction struct {
	Version                         uint64
	Edits                           []Edit
	Kind                            EditKind
	Owner                           any
	SelectionBefore, SelectionAfter any
	// SelectionAfterEdit optionally computes SelectionAfter from the candidate
	// snapshot and its changes, after newline projection. It runs with the
	// buffer locked, before publishing text or history. It must use only these
	// immutable arguments and must not call back into Buffer methods.
	// A panic leaves the text, version and undo/redo history unchanged.
	SelectionAfterEdit func(Snapshot, []Change) any
}

// TransactionResult describes an atomic attempt. Applied includes an accepted
// edit that changes no text; compare Version with the requested version to find
// whether the buffer changed. Rejected reports the source byte limit, and Stale
// reports a version mismatch. Either leaves text and undo/redo history intact.
type TransactionResult struct {
	Applied, Rejected, Stale bool
	Version                  uint64
	Changes                  []Change
}

// ApplyTransaction applies edits and selection metadata under the same lock,
// using the buffer's existing undo history and edit grouping.
func (b *Buffer) ApplyTransaction(t Transaction) TransactionResult {
	b.mu.Lock()
	defer b.mu.Unlock()
	if t.Version != b.version {
		return TransactionResult{Stale: true, Version: b.version}
	}
	kind := editKind(t.Kind)
	if kind > editCompose {
		kind = editOther
	}
	after := t.SelectionAfterEdit
	if after == nil {
		after = func(Snapshot, []Change) any { return t.SelectionAfter }
	}
	changes := b.editWithSelection(t.Edits, kind, t.Owner, t.SelectionBefore, after)
	if b.limitExceeded {
		return TransactionResult{Rejected: true, Version: b.version}
	}
	return TransactionResult{Applied: true, Version: b.version, Changes: publicChanges(changes)}
}

// HistoryResult describes a buffer undo/redo step and the selection to restore
// in the controller identified by Owner. A different controller follows the
// buffer's changes instead of interpreting this selection.
type HistoryResult struct {
	Applied   bool
	Version   uint64
	Owner     any
	Selection any
}

// UndoTransaction takes back the buffer's last undo step and returns its
// original selection metadata, regardless of the controller that made it.
func (b *Buffer) UndoTransaction() HistoryResult {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, ok := b.undoStep()
	return HistoryResult{Applied: ok, Version: b.version, Owner: t.owner, Selection: t.selBefore}
}

// RedoTransaction reapplies the last step undone and returns its final selection.
func (b *Buffer) RedoTransaction() HistoryResult {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, ok := b.redoStep()
	return HistoryResult{Applied: ok, Version: b.version, Owner: t.owner, Selection: t.selAfter}
}

// EndUndoGroup makes the next edit start its own undo step, as moving a caret
// or ending an input method composition does.
func (b *Buffer) EndUndoGroup() { b.endGroup() }

// ChangesSince returns each version's LF byte changes and the current snapshot
// atomically. Changes within a version use the text before that version; the
// outer slice is in version order. When the bounded log no longer reaches since,
// ok is false and the snapshot still contains the current complete text.
func (b *Buffer) ChangesSince(since uint64) (changes [][]Change, snapshot Snapshot, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	entries, _, ok := b.entriesSinceLocked(since)
	changes = make([][]Change, len(entries))
	for i, e := range entries {
		changes[i] = publicChanges(e.changes)
	}
	return changes, b.snapshotLocked(), ok
}
