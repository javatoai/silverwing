package desktop

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	markdownui "silverwing-go/internal/ui/markdown"
)

func TestMarkdownLinkFilePreviewIsIndependentReadOnlyAndRejectsStaleOwner(t *testing.T) {
	owner := newReadonlyMarkdownState("origin", "# 原文", nil)
	defer closeMarkdownPreviewState(owner)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	viewer := &markdownLinkFilePreview{open: true, home: "home", owner: owner, ownerVersion: owner.doc.Snapshot().Version, ctx: ctx, cancel: cancel, target: markdownui.LinkTarget{Path: "D:/external/Test.java", Line: 2}, content: documents.Content{Path: "D:/external/Test.java", Kind: "text", Text: "first\n第二行\nthird", ReadOnly: true}}
	s := &app{snapshot: application.Snapshot{Home: "home"}, materialsUnit: materialsController{state: materialsState{markdownLinkFile: viewer, documentPath: "unchanged.md", documentContent: "draft"}}}
	view := ui.NewTester(func(c *ui.Context) { s.renderMarkdownLinkFile(c, setTheme(c, false)) }, 900, 650)
	if !view.HasText("Test.java · 只读") || viewer.code.view == nil || viewer.code.view.Buffer.OriginalText() != viewer.content.Text {
		t.Fatalf("preview: %v", view.Texts())
	}
	if viewer.code.view.CursorPos().Line != 1 {
		t.Fatalf("line request: %+v", viewer.code.view.CursorPos())
	}
	if !viewer.code.view.ReadOnly || s.materialsUnit.state.documentPath != "unchanged.md" || s.materialsUnit.state.documentContent != "draft" {
		t.Fatal("external preview replaced original editor")
	}
	owner.doc.SyncExternal("# 新原文")
	view.Frame()
	if s.materialsUnit.state.markdownLinkFile != nil || ctx.Err() == nil {
		t.Fatal("stale owner preview survived")
	}
}

func TestMarkdownLinkLocationOffsetHandlesUnicodeAndLineEndings(t *testing.T) {
	if got := markdownLinkLocationOffset("first\r\n甲😀乙\r\nlast", 2, 3); got != len("first\n甲😀") {
		t.Fatalf("UTF-8 offset %d", got)
	}
}

func TestMarkdownNestedFilePreviewClickKeepsReplacementAndOriginalEditor(t *testing.T) {
	s, _, files := materialTextEditorFixture(t, map[string]string{"origin.md": "# 原文\r\n\r\n保留草稿\r\n"})
	content := files["origin.md"]
	action, result := writableMarkdownReadFixture(s, content)
	s.materialsUnit.state.documentPath, s.materialsUnit.state.documentReadPending = content.Path, true
	s.applyMaterialsActionResult(action, result)
	owner := s.ensureMarkdownEditor()
	t.Cleanup(s.closeMarkdownEditor)
	if err := owner.componentEditor.SetMode(markdownui.Source); err != nil {
		t.Fatal(err)
	}
	owner.live.SetSelection(2, 2)
	buffer, before := owner.live.Buffer(), owner.componentEditor.Snapshot()
	ctx, cancel := context.WithCancel(context.Background())
	path := filepath.Join(t.TempDir(), "outside.md")
	text := "[Nested file](next.md)"
	old := &markdownLinkFilePreview{open: true, home: s.snapshot.Home, owner: owner, ownerVersion: owner.doc.Snapshot().Version, ctx: ctx, cancel: cancel,
		target: markdownui.LinkTarget{Path: path}, content: documents.Content{Path: path, Kind: "markdown", Text: text, ReadOnly: true},
		reader: markdownui.NewReaderState(markdownui.Document{ID: "nested-old", Path: path, Source: text})}
	s.materialsUnit.state.markdownLinkFile = old
	t.Cleanup(s.closeMarkdownLinkFile)
	view := ui.NewTester(func(c *ui.Context) { s.renderMarkdownLinkFile(c, setTheme(c, false)) }, 900, 650)
	if err := view.Click("Nested file"); err != nil {
		t.Fatal(err)
	}
	next := s.materialsUnit.state.markdownLinkFile
	if next == nil || next == old || !next.open || next.ctx.Err() != nil || next.target.Path != filepath.Join(filepath.Dir(path), "next.md") {
		t.Fatalf("old viewer cleanup cancelled its replacement: %+v", next)
	}
	if ctx.Err() == nil || old.reader.Context().Err() == nil {
		t.Fatal("replaced viewer retained its reader or request")
	}
	if s.materialsUnit.state.markdown != owner || owner.live.Buffer() != buffer || owner.componentEditor.Mode() != markdownui.Source || owner.componentEditor.Snapshot() != before {
		t.Fatal("nested preview replaced or rewrote the original editor")
	}
	if a, z := owner.live.Selection(); a != 2 || z != 2 {
		t.Fatalf("nested preview moved the original selection: %d:%d", a, z)
	}
}

func TestWritableMarkdownHeadingNavigationConsumesOnceAndKeepsDraftHistory(t *testing.T) {
	for _, otherFile := range []bool{false, true} {
		for _, mode := range []markdownui.Mode{markdownui.Live, markdownui.Source} {
			t.Run(fmt.Sprintf("other-file-%t/mode-%d", otherFile, mode), func(t *testing.T) {
				text := "# Start\r\n\r\n" + strings.Repeat("filler paragraph\r\n\r\n", 45) + "# API v2 — **notes**!\r\n\r\n目标内容\r\n"
				s, _, files := materialTextEditorFixture(t, map[string]string{"origin.md": "# Origin\r\n", "other.md": text})
				content := files["origin.md"]
				if !otherFile {
					content = files["other.md"]
				}
				s.materialsUnit.state.documents = []application.Document{{Path: files["origin.md"].Path, Kind: "markdown"}, {Path: files["other.md"].Path, Kind: "markdown"}}
				action, result := writableMarkdownReadFixture(s, content)
				s.materialsUnit.state.documentPath, s.materialsUnit.state.documentReadPending = content.Path, true
				s.applyMaterialsActionResult(action, result)
				if otherFile {
					origin := s.ensureMarkdownEditor()
					if err := origin.componentEditor.NativeState().ReplaceRange(0, 0, "origin draft\r\n", origin.live.Buffer().Version()); err != nil {
						t.Fatal(err)
					}
					if err := s.captureMaterialsMarkdownInput(origin); err != nil {
						t.Fatal(err)
					}
					originDraft := s.materialsUnit.state.documentDrafts[content.Path]
					// The fixture has no native window: deliver the controlled read
					// after exercising the local-link and materials selection route.
					s.openMarkdownLink(origin, "other.md#api-v2--notes")
					if s.materialsUnit.state.documentPath != files["other.md"].Path || s.materialsUnit.state.documentDrafts[content.Path] != originDraft {
						t.Fatal("other-file navigation lost the original draft")
					}
					content = files["other.md"]
					action, result = writableMarkdownReadFixture(s, content)
					s.materialsUnit.state.documentReadPending = true
					s.applyMaterialsActionResult(action, result)
				}
				state := s.ensureMarkdownEditor()
				t.Cleanup(s.closeMarkdownEditor)
				buffer := state.live.Buffer()
				if err := state.componentEditor.NativeState().ReplaceRange(0, 0, "destination draft\r\n\r\n", buffer.Version()); err != nil {
					t.Fatal(err)
				}
				if err := s.captureMaterialsMarkdownInput(state); err != nil {
					t.Fatal(err)
				}
				if err := state.componentEditor.SetMode(mode); err != nil {
					t.Fatal(err)
				}
				before, draft := state.componentEditor.Snapshot(), s.materialsUnit.state.documentDrafts[state.path]
				if !otherFile {
					s.openMarkdownLink(state, "#api-v2--notes")
				}
				view := ui.NewTester(func(c *ui.Context) {
					ui.Column(c).Fill().Children(func() { s.renderMaterialsMarkdownEditor(c, setTheme(c, false)) })
				}, 700, 360)
				want := strings.Index(buffer.Snapshot().String(), "# API")
				if a, z := state.live.Selection(); a != want || z != want {
					t.Fatalf("heading selected %d:%d, want %d", a, z, want)
				}
				if s.materialsUnit.state.documentLinkFragment != "" || s.materialsUnit.state.markdown != state || state.live.Buffer() != buffer || state.componentEditor.Mode() != mode || state.componentEditor.Snapshot() != before || s.materialsUnit.state.documentDrafts[state.path] != draft {
					t.Fatal("heading navigation changed identity, mode, source, draft or version, or remained pending")
				}
				if caret, ok := view.TextCaret(); !ok || caret.Y < 0 || caret.Y >= 360 {
					t.Fatalf("heading caret was not revealed: %+v, %v", caret, ok)
				}
				state.live.SetSelection(0, 0)
				view.Frame()
				if a, z := state.live.Selection(); a != 0 || z != 0 {
					t.Fatal("consumed heading request moved the caret again")
				}
				if !buffer.Undo() || buffer.OriginalText() != content.Text {
					t.Fatal("heading navigation discarded the destination undo history")
				}
			})
		}
	}
}
