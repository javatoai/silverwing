package editor

import (
	"reflect"
	"testing"
)

func TestPreparedHunksPreserveFullBuffersAndInvalidateOnEdit(t *testing.T) {
	d := &DiffState{Old: NewBuffer("before\nold\nafter\n"), New: NewBuffer("before\nnew\nafter\n")}
	hunks := []DiffHunk{{1, 2, 1, 2}}
	if !d.SetPreparedHunks(hunks) {
		t.Fatal("valid prepared hunks rejected")
	}
	hunks[0].OldEnd = 99
	if got := d.Hunks(); !reflect.DeepEqual(got, []DiffHunk{{1, 2, 1, 2}}) {
		t.Fatalf("hunks = %v", got)
	}
	if d.Old.String() != "before\nold\nafter\n" || d.New.String() != "before\nnew\nafter\n" {
		t.Fatal("full text changed")
	}
	d.ExpandAll()
	d.CollapseAll()
	if d.New.Line(2) != "after" {
		t.Fatal("context unavailable")
	}
	d.New.Edit(Edit{Start: 0, End: 0, Text: "inserted\n"})
	d.Hunks()
	if d.model.seededHunks {
		t.Fatal("seed survived edit")
	}
	checkHunks(t, d.Old.String(), d.New.String(), d.model.hunks)
	d.New = NewBuffer("replacement\n")
	d.Hunks()
	checkHunks(t, d.Old.String(), d.New.String(), d.model.hunks)
}

func TestPreparedHunksRejectInvalidRangesAndContext(t *testing.T) {
	for _, hunks := range [][]DiffHunk{
		{{-1, 1, 0, 1}}, {{0, 9, 0, 1}}, {{1, 2, 1, 2}, {0, 1, 0, 1}}, {{0, 0, 0, 0}}, nil,
	} {
		d := &DiffState{Old: NewBuffer("a\nb"), New: NewBuffer("a\nc")}
		if d.SetPreparedHunks(hunks) {
			t.Fatalf("accepted %v", hunks)
		}
		d.Hunks()
		checkHunks(t, d.Old.String(), d.New.String(), d.model.hunks)
	}
}

func TestPreparedHunksAddedFileFullContext(t *testing.T) {
	d := &DiffState{Old: NewBuffer(""), New: NewBuffer("new\nfile\n")}
	if !d.SetPreparedHunks([]DiffHunk{{0, 0, 0, 3}}) {
		t.Fatal("added file rejected")
	}
	if add, remove := d.Stats(); add != 3 || remove != 0 {
		t.Fatalf("stats %d/%d", add, remove)
	}
	if d.New.String() != "new\nfile\n" {
		t.Fatal("new source lost")
	}
}

func TestPreparedHunksRevertAcceptEmptyBufferOneStep(t *testing.T) {
	for _, source := range []string{"first\nlast", "first\nlast\n"} {
		t.Run(source, func(t *testing.T) {
			removed := &DiffState{Old: NewBuffer(source), New: NewBuffer("")}
			if !removed.SetPreparedHunks([]DiffHunk{{0, removed.Old.LineCount(), 0, 0}}) {
				t.Fatal("deleted file rejected")
			}
			removed.Revert(0)
			if got := removed.New.String(); got != source || len(removed.Hunks()) != 0 {
				t.Fatalf("revert restored %q, want exactly %q without another change", got, source)
			}
			if !removed.New.Undo() || removed.New.String() != "" {
				t.Fatal("revert was not one undo step")
			}
			added := &DiffState{Old: NewBuffer(""), New: NewBuffer(source)}
			if !added.SetPreparedHunks([]DiffHunk{{0, 0, 0, added.New.LineCount()}}) {
				t.Fatal("added file rejected")
			}
			added.Accept(0)
			if got := added.Old.String(); got != source || len(added.Hunks()) != 0 {
				t.Fatalf("accept stored %q, want exactly %q without another change", got, source)
			}
		})
	}
}
