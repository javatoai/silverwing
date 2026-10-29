package editor

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func assertDocumentSource(t *testing.T, doc *Document, want string) {
	t.Helper()
	if got := doc.Source(); got != want {
		t.Fatalf("source = %q, want %q", got, want)
	}
	parts := doc.Parts()
	if doc.PartCount() != len(parts) || joinParts(parts) != want || doc.Snapshot().Source != want {
		t.Fatal("source, part count and defensive snapshot disagree")
	}
}

func TestDocumentSourceCacheTracksPartEditsHistoryAndExternalSync(t *testing.T) {
	const original = "word😀\r\n"
	doc := NewDocument(original, "source-cache")
	doc.SetClock(func() time.Time { return time.Unix(1, 0) })
	assertDocumentSource(t, doc, original)
	part := doc.Parts()[0]
	if err := doc.ReplacePart(part.ID, "changed🪴\r\n", true); err != nil {
		t.Fatal(err)
	}
	assertDocumentSource(t, doc, "changed🪴\r\n")
	if err := doc.ReplaceRange(part.ID, 0, len("changed"), "again", true); err != nil {
		t.Fatal(err)
	}
	assertDocumentSource(t, doc, "again🪴\r\n")
	if !doc.Undo() {
		t.Fatal("coalesced edits have no undo checkpoint")
	}
	assertDocumentSource(t, doc, original)
	if !doc.Redo() {
		t.Fatal("coalesced edits have no redo checkpoint")
	}
	assertDocumentSource(t, doc, "again🪴\r\n")
	doc.SetComposing(true)
	if doc.Undo() {
		t.Fatal("composing undo changed committed content")
	}
	assertDocumentSource(t, doc, "again🪴\r\n")
	doc.SetComposing(false)
	doc.SyncExternal("---\r\nraw: [KEEP]\r\n---\r\n\r\nexternal😀\r")
	assertDocumentSource(t, doc, "---\r\nraw: [KEEP]\r\n---\r\n\r\nexternal😀\r")
	if doc.Snapshot().CanUndo || doc.Snapshot().CanRedo {
		t.Fatal("external acknowledgment retained edit history")
	}
	if err := doc.ReplaceSource("source mode\n", false); err != nil {
		t.Fatal(err)
	}
	assertDocumentSource(t, doc, "source mode\n")
	if !doc.Undo() {
		t.Fatal("raw-source replacement lost undo")
	}
	assertDocumentSource(t, doc, "---\r\nraw: [KEEP]\r\n---\r\n\r\nexternal😀\r")
}

func TestDocumentSourceCacheTracksStructuralAndSpecializedMutations(t *testing.T) {
	t.Run("selection-insert-join-format", func(t *testing.T) {
		doc := NewDocument("one\r\n", "structure")
		first := doc.Parts()[0].ID
		inserted := doc.InsertParagraph(first)
		if inserted == 0 {
			t.Fatal("paragraph insertion failed")
		}
		assertDocumentSource(t, doc, "one\r\n\r\n")
		if err := doc.JoinAdjacent(first, inserted); err != nil {
			t.Fatal(err)
		}
		assertDocumentSource(t, doc, "one\r\n")
		if err := doc.Format(FormatRequest{PartID: first, Start: 0, End: 3, Command: FormatBold}); err != nil {
			t.Fatal(err)
		}
		assertDocumentSource(t, doc, "**one**\r\n")
		doc.SelectAll()
		if err := doc.ReplaceSelection("replacement😀\r"); err != nil {
			t.Fatal(err)
		}
		assertDocumentSource(t, doc, "replacement😀\r")
	})
	t.Run("task", func(t *testing.T) {
		doc := NewDocument("- [ ] keep😀\r\n", "task")
		if err := doc.SetTaskChecked(doc.Parts()[0].ID, 0, true); err != nil {
			t.Fatal(err)
		}
		assertDocumentSource(t, doc, "- [x] keep😀\r\n")
	})
	t.Run("code", func(t *testing.T) {
		doc := NewDocument("```go keep\r\nold()\r\n```\r\n", "code")
		part := doc.Parts()[0]
		if err := doc.SetCode(part.ID, "new😀()\r\n"); err != nil {
			t.Fatal(err)
		}
		assertDocumentSource(t, doc, "```go keep\r\nnew😀()\r\n```\r\n")
	})
	t.Run("table", func(t *testing.T) {
		const source = "| A | B |\r\n|---|---|\r\n| x | y |\r\n"
		doc := NewDocument(source, "table")
		if err := doc.SetTableCell(doc.Parts()[0].ID, 1, 0, "changed"); err != nil {
			t.Fatal(err)
		}
		assertDocumentSource(t, doc, strings.Replace(source, " x ", " changed ", 1))
	})
}

func TestDocumentSourceCachePreservesRejectedEditsAndDefensiveParts(t *testing.T) {
	doc := NewDocument("- [ ] original😀\r\n", "ownership")
	before := doc.Source()
	parts := doc.Parts()
	parts[0].Source = "mutated outside"
	parts[0].Nodes[0].Text = "mutated outside"
	assertDocumentSource(t, doc, before)
	doc.SetMaxSourceBytes(len(before))
	if err := doc.ReplaceSource(before+"x", false); err == nil {
		t.Fatal("oversized candidate was accepted")
	}
	assertDocumentSource(t, doc, before)
	if doc.Snapshot().CanUndo || doc.Snapshot().CanRedo {
		t.Fatal("rejected candidate changed history")
	}
	if err := doc.ReplaceRange(doc.Parts()[0].ID, -1, 0, "x", false); err == nil {
		t.Fatal("invalid byte range was accepted")
	}
	assertDocumentSource(t, doc, before)
	if count := NewDocument("", "empty").PartCount(); count != 1 {
		t.Fatalf("empty-source editable part count = %d, want existing one-part behavior", count)
	}
	if allocations := testing.AllocsPerRun(10, func() {
		documentSourceBenchSink = doc.Source()
		documentPartCountBenchSink = doc.PartCount()
	}); allocations != 0 {
		t.Fatalf("stable Source/PartCount allocates %g times", allocations)
	}
}

var documentSourceBenchSink string
var documentPartCountBenchSink int

func BenchmarkDocumentReadHotPath(b *testing.B) {
	paragraph := "A **bold** line. " + strings.Repeat("x", 240) + "\r\n\r\n"
	for _, size := range []int{64 * 1024, 1024 * 1024, 2 * 1024 * 1024} {
		source := strings.Repeat(paragraph, size/len(paragraph))
		b.Run(fmt.Sprintf("bytes_%d", size), func(b *testing.B) {
			doc := NewDocument(source, "benchmark")
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				documentSourceBenchSink = doc.Source()
				documentPartCountBenchSink = doc.PartCount()
			}
		})
	}
}
