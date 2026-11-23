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
	if !d.SetPreparedHunks([]DiffHunk{{0, 0, 0, 2}}) {
		t.Fatal("added file rejected")
	}
	if add, remove := d.Stats(); add != 2 || remove != 0 {
		t.Fatalf("stats %d/%d", add, remove)
	}
	if got := d.Hunks(); !reflect.DeepEqual(got, []DiffHunk{{0, 0, 0, 2}}) {
		t.Fatalf("prepared hunk includes the synthetic line after the final newline: %v", got)
	}
	if d.New.String() != "new\nfile\n" {
		t.Fatal("new source lost")
	}
}

func TestPreparedHunksUseGitRangesForFinalNewlineChanges(t *testing.T) {
	for _, tc := range []struct {
		name, old, new string
	}{
		{"added final newline", "a\nb", "a\nb\n"},
		{"removed final newline", "a\nb\n", "a\nb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &DiffState{Old: NewBuffer(tc.old), New: NewBuffer(tc.new)}
			if !d.SetPreparedHunks([]DiffHunk{{1, 2, 1, 2}}) {
				t.Fatal("rejected the Git hunk for the line whose final newline changed")
			}
			if got := d.Hunks(); !reflect.DeepEqual(got, []DiffHunk{{1, 2, 1, 2}}) {
				t.Fatalf("prepared final-line hunk changed: %v", got)
			}
		})
	}
}

func TestPreparedHunksRejectSyntheticTrailingEmptyLine(t *testing.T) {
	for _, tc := range []struct {
		name, old, new string
		hunk           DiffHunk
	}{
		{"added final newline", "a\nb", "a\nb\n", DiffHunk{2, 2, 2, 3}},
		{"removed final newline", "a\nb\n", "a\nb", DiffHunk{2, 3, 2, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &DiffState{Old: NewBuffer(tc.old), New: NewBuffer(tc.new)}
			if d.SetPreparedHunks([]DiffHunk{tc.hunk}) {
				t.Fatal("accepted a hunk outside the read-only line bounds")
			}
			if got := d.Hunks(); !reflect.DeepEqual(got, []DiffHunk{{1, 2, 1, 2}}) {
				t.Fatalf("native EOL comparison did not retain the final line change: %v", got)
			}
		})
	}
}

func TestPreparedHunksRejectUnequalTerminatorsInContext(t *testing.T) {
	d := &DiffState{Old: NewBuffer("a\nb"), New: NewBuffer("a\nb\n")}
	if d.SetPreparedHunks(nil) {
		t.Fatal("accepted unchanged context whose final line terminators differ")
	}
	if got := d.Hunks(); !reflect.DeepEqual(got, []DiffHunk{{1, 2, 1, 2}}) {
		t.Fatalf("native EOL comparison did not find the final line change: %v", got)
	}
}

func TestPreparedHunksDropCompletedComparisonJob(t *testing.T) {
	d := &DiffState{Old: NewBuffer("before\nold\nafter\n"), New: NewBuffer("before\nnew\nafter\n")}
	d.model.set(d.Old, d.New, !d.Editable)
	oldText, oldVer := d.Old.snapshot()
	newText, newVer := d.New.snapshot()
	job := &diffJob{oldText: oldText, newText: newText, oldVer: oldVer, newVer: newVer}
	job.done.Store(true)
	d.model.job = job

	if !d.SetPreparedHunks([]DiffHunk{{1, 2, 1, 2}}) {
		t.Fatal("valid prepared hunks rejected")
	}
	if d.model.job != nil {
		t.Fatal("prepared hunks left the previous comparison job attached")
	}
	if got := d.Hunks(); !reflect.DeepEqual(got, []DiffHunk{{1, 2, 1, 2}}) {
		t.Fatalf("completed comparison replaced the prepared hunks: %v", got)
	}
}

func TestPreparedHunksRevertAcceptEmptyBufferOneStep(t *testing.T) {
	for _, tc := range []struct {
		source string
		lines  int
	}{{"first\nlast", 2}, {"first\nlast\n", 2}} {
		t.Run(tc.source, func(t *testing.T) {
			source := tc.source
			removed := &DiffState{Old: NewBuffer(source), New: NewBuffer("")}
			if !removed.SetPreparedHunks([]DiffHunk{{0, tc.lines, 0, 0}}) {
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
			if !added.SetPreparedHunks([]DiffHunk{{0, 0, 0, tc.lines}}) {
				t.Fatal("added file rejected")
			}
			added.Accept(0)
			if got := added.Old.String(); got != source || len(added.Hunks()) != 0 {
				t.Fatalf("accept stored %q, want exactly %q without another change", got, source)
			}
		})
	}
}
