package editor

import (
	"strings"
	"testing"
)

func TestMarkdownMaterialThreeMiBSourceLimit(t *testing.T) {
	const limit = 3 * 1024 * 1024
	if MaxDocumentBytes != limit {
		t.Fatalf("Markdown editor limit = %d, want %d", MaxDocumentBytes, limit)
	}
	for _, bomOutsideSource := range []bool{false, true} {
		t.Run(map[bool]string{false: "source-bom", true: "host-bom"}[bomOutsideSource], func(t *testing.T) {
			doc := NewDocument("before", "material-limit")
			source := "\ufeff" + strings.Repeat("x", limit-3)
			if bomOutsideSource {
				doc.SetMaxSourceBytes(limit - 3)
				source = strings.Repeat("x", limit-3)
			}
			if err := doc.ReplaceSource(source, false); err != nil {
				t.Fatalf("exact encoded 3 MiB source rejected: %v", err)
			}
			before := doc.Snapshot()
			if err := doc.ReplaceSource(source+"x", false); err == nil {
				t.Fatal("one byte beyond the BOM-aware limit was accepted")
			}
			after := doc.Snapshot()
			if after.Source != source || after.Version != before.Version || after.CanUndo != before.CanUndo || after.CanRedo != before.CanRedo {
				t.Fatal("rejected candidate changed committed source or history")
			}
			if !doc.Undo() || doc.Source() != "before" || !doc.Redo() || doc.Source() != source {
				t.Fatal("accepted large source lost document undo/redo")
			}
		})
	}
	if MaxMermaidSourceBytes != 512*1024 {
		t.Fatal("document editing limit changed the independent Mermaid render limit")
	}
}

func TestMarkdownMaterialLargeRichPatchKeepsOtherSource(t *testing.T) {
	const prefix = "---\r\nkeep: [RAW, META]\r\n---\r\n\r\n"
	const suffix = "\r\n\r\n```go keep-info\r\nuntouched()\nraw()\r\n```\r\n<!-- KEEP -->\r"
	richSource := "word " + strings.Repeat("x", 512*1024+32)
	doc := NewDocument(prefix+richSource+suffix, "large-rich")
	var part Part
	for _, candidate := range doc.Parts() {
		if candidate.Kind == PartRich && strings.HasPrefix(strings.TrimLeft(candidate.Source, "\r\n"), "word ") {
			part = candidate
			break
		}
	}
	if part.ID == 0 {
		for _, candidate := range doc.Parts() {
			t.Logf("part kind=%s bytes=%d prefix=%q", candidate.Kind, len(candidate.Source), candidate.Source[:min(32, len(candidate.Source))])
		}
		t.Fatal("fixture has no identified rich part")
	}
	model, err := NewRichBlock(part)
	if err != nil {
		t.Fatal(err)
	}
	if err := model.ReplaceUTF16(0, 4, "changed"); err != nil {
		t.Fatalf("rich content over the old 512 KiB limit is still blocked: %v", err)
	}
	if err := doc.ReplacePart(part.ID, model.Source(), false); err != nil {
		t.Fatal(err)
	}
	if got, want := doc.Source(), prefix+"changed "+richSource[len("word "):]+suffix; got != want {
		t.Fatal("rich patch changed an untouched source range")
	}
	binding := NewBinding("large", "", nil, nil, nil)
	if !binding.Update(doc.Source()) || binding.State().Content != doc.Source() {
		t.Fatal("binding rejected a valid large Markdown source")
	}
	if binding.Update(strings.Repeat("x", MaxDocumentBytes+1)) || !strings.Contains(binding.State().Fallback, "3 MiB") {
		t.Fatal("binding accepted an oversized source or reported the old limit")
	}
}
