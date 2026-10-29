package editor_test

import (
	"reflect"
	"testing"

	"silverwing-go/internal/editor"
	"silverwing-go/internal/markdowntext"
)

func TestMarkdowntextParserCompatibilityKeepsEditorIdentitiesAndSource(t *testing.T) {
	source := "# 中文标题\r\n\r\n![图片](image.png)\r\n\r\n```go\r\n// code\r\n```\r\n"
	pureParts, pureNodes := markdowntext.ParseDocument(source)
	snapshot := editor.Parse(source)
	if snapshot.Source != source || snapshot.CanUndo || snapshot.CanRedo || snapshot.Version != 0 || len(snapshot.Parts) != len(pureParts) {
		t.Fatalf("editor Parse contract changed: %#v", snapshot)
	}
	seen := map[uint64]bool{}
	for index, part := range snapshot.Parts {
		if part.ID == 0 || seen[part.ID] {
			t.Fatalf("editor part ID was missing or reused: %d", part.ID)
		}
		seen[part.ID] = true
		part.ID = 0
		if pureParts[index].ID != 0 || !reflect.DeepEqual(part, pureParts[index]) {
			t.Fatalf("editor wrapper changed parsed source or AST at part %d", index)
		}
	}
	for _, part := range editor.Parse(source).Parts {
		if seen[part.ID] || part.ID == 0 {
			t.Fatal("independent parses shared an editor part identity")
		}
	}
	// These assignments also keep the existing source-located value types
	// source-compatible with consumers of the pure parser.
	var nodes []editor.Node = pureNodes
	if len(nodes) == 0 {
		t.Fatal("fixture did not produce source-located Markdown nodes")
	}
	if !reflect.DeepEqual(editor.CommentImages(source), markdowntext.CommentImages(source)) {
		t.Fatal("editor image API no longer shares the pure parser")
	}
}

func TestMarkdowntextSharedListSyntaxKeepsDocumentEditsSourcePreserving(t *testing.T) {
	for _, test := range []struct {
		name, source, want string
		command            editor.FormatCommand
	}{
		{name: "add unordered", source: "中文一\r\n中文二  \r\n", want: "- 中文一\r\n- 中文二  \r\n", command: editor.FormatUnorderedList},
		{name: "remove unordered", source: "- 中文一\r\n* 中文二  \r\n", want: "中文一\r\n中文二  \r\n", command: editor.FormatUnorderedList},
		{name: "convert indented ordered", source: "  9) 中文一\r\n  10) 中文二  \r\n", want: "  - 中文一\r\n  - 中文二  \r\n", command: editor.FormatUnorderedList},
		{name: "convert to ordered", source: "- 中文一\r\n- 中文二  \r\n", want: "1. 中文一\r\n2. 中文二  \r\n", command: editor.FormatOrderedList},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := editor.NewDocument(test.source, "shared-list-syntax")
			part := document.Parts()[0]
			if part.Kind != editor.PartRich {
				t.Fatalf("fixture is not an editable rich source group: %#v", part)
			}
			if err := document.Format(editor.FormatRequest{PartID: part.ID, Start: 0, End: len(part.Source), Command: test.command}); err != nil {
				t.Fatal(err)
			}
			if got := document.Source(); got != test.want {
				t.Fatalf("list formatting changed source outside its markers: got %q, want %q", got, test.want)
			}
		})
	}
	const original = "- [ ] 中文任务  \r\n- [X] 第二项\r\n"
	document := editor.NewDocument(original, "shared-checkbox-syntax")
	part := document.Parts()[0]
	if err := document.SetTaskChecked(part.ID, 0, true); err != nil {
		t.Fatal(err)
	}
	want := "- [x] 中文任务  \r\n- [X] 第二项\r\n"
	if document.Source() != want {
		t.Fatalf("checkbox edit changed text, whitespace, or line endings: %q", document.Source())
	}
	if err := document.SetTaskChecked(part.ID, 2, true); err == nil || document.Source() != want {
		t.Fatal("invalid checkbox index changed the document or succeeded")
	}
}
