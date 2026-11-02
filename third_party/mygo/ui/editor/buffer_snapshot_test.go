package editor

import (
	"bytes"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBufferSnapshotRetainsExactMixedNewlines(t *testing.T) {
	source := "\ufeff# 标题\r\n\n段落\r尾😀"
	b := NewBufferPreservingLineEndings(source)
	snapshot := b.Snapshot()
	wantLF := "\ufeff# 标题\n\n段落\n尾😀"
	if snapshot.String() != wantLF || snapshot.OriginalText() != source || snapshot.Version() != b.Version() {
		t.Fatalf("snapshot LF=%q source=%q version=%d", snapshot.String(), snapshot.OriginalText(), snapshot.Version())
	}
	for _, tc := range []struct {
		start, end int
		want       string
	}{
		{0, snapshot.Len(), source},
		{snapshot.LineStart(0), snapshot.LineStart(1), "\ufeff# 标题\r\n"},
		{snapshot.LineStart(1), snapshot.LineStart(2), "\n"},
		{snapshot.LineStart(2), snapshot.LineStart(3), "段落\r"},
		{snapshot.LineStart(3), snapshot.Len(), "尾😀"},
	} {
		if got := snapshot.OriginalSlice(tc.start, tc.end); got != tc.want {
			t.Fatalf("OriginalSlice(%d,%d) = %q, want %q", tc.start, tc.end, got, tc.want)
		}
	}
	var original bytes.Buffer
	if n, err := snapshot.WriteOriginalTo(&original); err != nil || n != int64(len(source)) || original.String() != source {
		t.Fatalf("original write: bytes=%d err=%v text=%q", n, err, original.String())
	}
	var legacy bytes.Buffer
	if _, err := snapshot.WriteTo(&legacy); err != nil || legacy.String() != strings.ReplaceAll(wantLF, "\n", "\r\n") {
		t.Fatalf("legacy WriteTo changed: err=%v text=%q", err, legacy.String())
	}
	b.Edit(Edit{Start: 0, End: 0, Text: "new\n"})
	if snapshot.String() != wantLF || snapshot.OriginalText() != source {
		t.Fatal("editing changed immutable snapshot")
	}
	ordinary := NewBuffer("a\r\nb").Snapshot()
	if ordinary.OriginalText() != "a\nb" || ordinary.OriginalSlice(1, 2) != "\n" {
		t.Fatalf("ordinary snapshot LF source=%q slice=%q", ordinary.OriginalText(), ordinary.OriginalSlice(1, 2))
	}
}

func TestBufferSnapshotTracksSourceOnlyNewlineEditsThroughHistory(t *testing.T) {
	b := NewBufferPreservingLineEndings("a\r\nb\rc")
	initial := b.Snapshot()
	result := b.ApplyTransaction(Transaction{Version: initial.Version(), Edits: []Edit{{Start: 1, End: 2, Text: "\n"}}})
	if !result.Applied || result.Version == initial.Version() || len(result.Changes) != 0 {
		t.Fatalf("source-only result = %+v", result)
	}
	changes, changed, ok := b.ChangesSince(initial.Version())
	if !ok || len(changes) != 1 || len(changes[0]) != 0 || changed.String() != initial.String() || changed.OriginalText() != "a\nb\rc" {
		t.Fatalf("source-only log=%v source=%q LF=%q ok=%v", changes, changed.OriginalText(), changed.String(), ok)
	}
	if !b.UndoTransaction().Applied || b.Snapshot().OriginalText() != "a\r\nb\rc" || b.Modified() {
		t.Fatalf("source-only undo = %q", b.Snapshot().OriginalText())
	}
	if !b.RedoTransaction().Applied || b.Snapshot().OriginalText() != "a\nb\rc" || !b.Modified() {
		t.Fatalf("source-only redo = %q", b.Snapshot().OriginalText())
	}
	if initial.OriginalText() != "a\r\nb\rc" || changed.OriginalText() != "a\nb\rc" {
		t.Fatal("source-only history mutated previous snapshots")
	}
}

type sourceSnapshotRecorder struct{ snapshots []Snapshot }

func (h *sourceSnapshotRecorder) Edit(_, next Snapshot, _ []Change) {
	h.snapshots = append(h.snapshots, next)
}
func (*sourceSnapshotRecorder) Highlight(int, time.Duration) bool { return true }
func (*sourceSnapshotRecorder) Tokens(_ int, _ string, dst []Token) ([]Token, bool) {
	return dst, true
}

func TestBufferHistoryHighlighterSnapshotsRetainSourceForEveryVersion(t *testing.T) {
	b := NewBufferPreservingLineEndings("a\r\nb\rc\n")
	recorder := &sourceSnapshotRecorder{}
	b.SetLanguage(&Language{Highlighter: func(snapshot Snapshot, _ func()) Highlighter {
		recorder.snapshots = append(recorder.snapshots, snapshot)
		return recorder
	}})
	b.highlighter()
	owner := new(int)
	for _, edit := range []Edit{{Start: 0, End: 1, Text: "A"}, {Start: 2, End: 3, Text: "B"}} {
		if result := b.ApplyTransaction(Transaction{Version: b.Version(), Owner: owner, Kind: EditTyping, Edits: []Edit{edit}}); !result.Applied {
			t.Fatalf("apply = %+v", result)
		}
	}
	if !b.UndoTransaction().Applied || !b.RedoTransaction().Applied {
		t.Fatal("history step missing")
	}
	b.highlighter()
	want := []string{"a\r\nb\rc\n", "A\r\nb\rc\n", "A\r\nB\rc\n", "A\r\nb\rc\n", "a\r\nb\rc\n", "A\r\nb\rc\n", "A\r\nB\rc\n"}
	if len(recorder.snapshots) != len(want) {
		t.Fatalf("received %d snapshots, want %d", len(recorder.snapshots), len(want))
	}
	for i, snapshot := range recorder.snapshots {
		if snapshot.Version() != uint64(i) || snapshot.OriginalText() != want[i] || snapshot.String() != normalizeSnapshotSource(want[i]) {
			t.Fatalf("snapshot %d: version=%d source=%q LF=%q", i, snapshot.Version(), snapshot.OriginalText(), snapshot.String())
		}
	}
}

func TestBufferSnapshotIsAtomicDuringConcurrentReset(t *testing.T) {
	b := NewBufferPreservingLineEndings("v0\r\nline\rend\n")
	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		for i := range 300 {
			b.Reset("v" + strconv.Itoa(i) + "\r\nline\rend\n")
		}
	}()
	defer writer.Wait()
	for range 300 {
		_, snapshot, _ := b.ChangesSince(0)
		if snapshot.String() != normalizeSnapshotSource(snapshot.OriginalText()) {
			t.Fatalf("snapshot version=%d LF=%q source=%q", snapshot.Version(), snapshot.String(), snapshot.OriginalText())
		}
	}
}

func normalizeSnapshotSource(source string) string {
	return strings.ReplaceAll(strings.ReplaceAll(source, "\r\n", "\n"), "\r", "\n")
}
