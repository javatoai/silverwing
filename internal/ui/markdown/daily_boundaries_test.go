package markdownui

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
)

func TestDailyRichPasteAndResourcesMaterializeWritingGaps(t *testing.T) {
	for _, kind := range []string{"html", "resource", "link", "link-dialog"} {
		t.Run(kind, func(t *testing.T) {
			const source = "```sql\nSELECT 1;\n```\n```python\nprint(2)\n```\n"
			s := NewEditorState(Document{ID: "gap", Path: "D:/synthetic/gap.md", Source: source})
			o := EditorOptions{ReadClipboard: func(context.Context) (ClipboardContent, error) {
				return ClipboardContent{Text: "middle", HTML: "<b>middle</b>"}, nil
			}}
			v := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Children(func() { MarkdownEditor(c, s, o).Fill() }) }, 900, 600)
			if err := v.Click("在代码块之间插入内容"); err != nil {
				t.Fatal(err)
			}
			if kind == "html" {
				v.Key(ui.Cmd, ui.KeyV)
			} else if kind == "link" {
				if err := s.Execute(EditorCommand{Kind: InsertLink, Text: "middle", Target: "https://example.com"}); err != nil {
					t.Fatal(err)
				}
			} else if kind == "link-dialog" {
				v.Key(ui.Cmd, ui.KeyK)
				if s.insert == nil || !s.insert.open {
					t.Fatal("link command unavailable in the writing row")
				}
				s.insert.text = "middle"
				s.insert.target = "https://example.com"
				v.Frame()
				if err := v.Click("插入"); err != nil {
					t.Fatal(err)
				}
			} else {
				s.options.OnImportResources = func(_ context.Context, _ ImportRequest, done func(ImportResult, error)) {
					done(ImportResult{Resources: []ImportedResource{{Target: "gap.assets/image.png", Image: true}}}, nil)
				}
				s.importResources(ImportRequest{Pick: true})
			}
			d := parser.Parse(s.native.Buffer().String())
			count := 0
			for i := 1; i < d.Len(); i++ {
				if b := d.Block(i); b.Kind == parser.CodeBlock {
					count++
					if b.Open || !strings.Contains(d.Lines(i), map[string]string{"sql": "SELECT 1;", "python": "print(2)"}[b.Language()]) {
						t.Fatalf("rich insertion changed adjacent code: %q", s.Snapshot().Source)
					}
				}
			}
			if count != 2 {
				t.Fatalf("lost a code block after rich insertion: %q", s.Snapshot().Source)
			}
			v.Command("undo")
			if s.Snapshot().Source != source {
				t.Fatal("gap/import was not one undo")
			}
		})
	}
}

func TestDailyMultipleImportedImagesRemainInsideOneTableCell(t *testing.T) {
	const source = "| A | B |\n|---|---|\n| x | y |\n"
	s, v := dailyPublicEditor(source)
	at := strings.Index(source, "x")
	s.native.SetSelection(at, at+1)
	v.Frame()
	s.options.OnImportResources = func(_ context.Context, _ ImportRequest, done func(ImportResult, error)) {
		done(ImportResult{Resources: []ImportedResource{{Target: "daily.assets/a.png", Image: true}, {Target: "daily.assets/b.png", Image: true}}}, nil)
	}
	s.importResources(ImportRequest{Pick: true})
	d := parser.Parse(s.native.Buffer().String())
	rows, cells := 0, 0
	for i := 1; i < d.Len(); i++ {
		if d.Block(i).Kind == parser.TableRow {
			rows++
		}
		if d.Block(i).Kind == parser.TableCell {
			cells++
		}
	}
	if rows != 2 || cells != 4 || !strings.Contains(s.Snapshot().Source, "<br>") {
		t.Fatalf("resources broke physical table: %q (rows=%d cells=%d)", s.Snapshot().Source, rows, cells)
	}
	v.Command("undo")
	if s.Snapshot().Source != source {
		t.Fatal("multi-resource cell insertion was not one undo")
	}
}

func TestDailyInsertLinkPanelStaysVisibleAndAcceptsTyping(t *testing.T) {
	s, v := dailyPublicEditor("hello")
	s.native.SetSelection(0, 5)
	v.Frame()
	v.Key(ui.Cmd, ui.KeyK)
	v.Frame()
	for _, label := range []string{"插入链接", "显示文字", "链接地址", "插入", "取消"} {
		r, ok := v.Find(label)
		if !ok || r.Y < 0 || r.Y+r.H > 600 || r.X < 0 || r.X+r.W > 900 {
			t.Fatalf("clipped insert panel %s: %+v found=%v", label, r, ok)
		}
	}
	v.Type("https://example.com/writing")
	if s.Snapshot().Source != "hello" || s.insert.target != "https://example.com/writing" {
		t.Fatal("dialog did not focus the link address")
	}
	if err := v.Click("插入"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.Snapshot().Source, "[hello](<https://example.com/writing>)") {
		t.Fatal("dialog insertion failed")
	}
	v.Command("undo")
	if s.Snapshot().Source != "hello" {
		t.Fatal("dialog insertion was not one undo")
	}
}

func TestDailyInvalidViewRestoreDoesNotPartiallyChangeState(t *testing.T) {
	s, _ := dailyPublicEditor("hello")
	before := s.GetViewState()
	bad := before
	bad.Mode = Preview
	bad.ScrollY = float32(math.NaN())
	if err := s.RestoreViewState(bad); err == nil {
		t.Fatal("invalid viewport accepted")
	}
	if after := s.GetViewState(); after != before {
		t.Fatalf("rejected restore changed state: before=%+v after=%+v", before, after)
	}
}

func TestDailyPreviewRestoreDoesNotOverwriteNativeModeScroll(t *testing.T) {
	s, v := dailyPublicEditor(strings.Repeat("paragraph\n\n", 120))
	before := s.native.GetViewState()
	state := s.GetViewState()
	state.Mode = Preview
	state.ScrollY = 500
	if err := s.RestoreViewState(state); err != nil {
		t.Fatal(err)
	}
	v.Frame()
	if err := s.SetMode(Live); err != nil {
		t.Fatal(err)
	}
	v.Frame()
	if after := s.native.GetViewState(); after.ScrollY != before.ScrollY {
		t.Fatalf("preview scroll leaked into live: before=%+v after=%+v", before, after)
	}
}

func TestDailyRichInsertionUndoKeepsReverseSelection(t *testing.T) {
	s, v := dailyPublicEditor("hello")
	s.native.SetSelection(5, 0)
	v.Frame()
	if err := s.Execute(EditorCommand{Kind: InsertLink, Text: "hello", Target: "https://example.com"}); err != nil {
		t.Fatal(err)
	}
	v.Command("undo")
	a, z := s.native.Selection()
	if a != 5 || z != 0 || s.Snapshot().Source != "hello" {
		t.Fatalf("rich undo lost original direction: %d:%d %q", a, z, s.Snapshot().Source)
	}
}

func TestDailyRichInsertionTargetsOmittedColumnAndRestoresIntentOnUndo(t *testing.T) {
	for _, kind := range []string{"link", "resource"} {
		for _, suffix := range []string{"", "\n", "\n\n<!-- untouched -->"} {
			t.Run(kind+suffix, func(t *testing.T) {
				source := "| A | B | C |\n|---|---|---|\n| x |" + suffix
				s, v := dailyPublicEditor(source)
				at := strings.Index(source, "C")
				s.native.SetSelection(at, at)
				v.Frame()
				v.Key(0, ui.KeyDown)
				if s.Snapshot().Source != source || s.Snapshot().Version != 0 {
					t.Fatal("navigation materialized missing cells")
				}
				if kind == "link" {
					if err := s.Execute(EditorCommand{Kind: InsertLink, Text: "chosen", Target: "https://example.com"}); err != nil {
						t.Fatal(err)
					}
				} else {
					s.options.OnImportResources = func(_ context.Context, _ ImportRequest, done func(ImportResult, error)) {
						done(ImportResult{Resources: []ImportedResource{{Target: "daily.assets/chosen.png", Text: "chosen", Image: true}}}, nil)
					}
					s.importResources(ImportRequest{Pick: true})
				}
				assertThird := func(want string) {
					t.Helper()
					d := parser.Parse(s.native.Buffer().String())
					var cells []string
					for i := 1; i < d.Len(); i++ {
						if d.Block(i).Kind == parser.TableCell {
							var text strings.Builder
							for _, in := range d.Inlines(i) {
								if in.Kind == parser.Text || in.Kind == parser.Image {
									text.WriteString(in.Text)
								}
							}
							cells = append(cells, text.String())
						}
					}
					if len(cells) != 6 || cells[4] != "" || cells[5] != want {
						t.Fatalf("write missed third cell: %q cells=%q", s.Snapshot().Source, cells)
					}
				}
				assertThird("chosen")
				v.Command("undo")
				if s.Snapshot().Source != source {
					t.Fatal("omitted insertion undo changed old source")
				}
				v.Type("again")
				assertThird("again")
			})
		}
	}
}

func TestDailyBlockHTMLPasteKeepsStructureInsideProse(t *testing.T) {
	for _, fragment := range []string{"<h2>Title</h2>", "<ul><li>item</li></ul>", "<table><tr><th>A</th></tr><tr><td>x</td></tr></table>"} {
		t.Run(fragment, func(t *testing.T) {
			s := NewEditorState(Document{ID: "block-html", Source: "beforeafter"})
			o := EditorOptions{ReadClipboard: func(context.Context) (ClipboardContent, error) {
				return ClipboardContent{Text: "fallback", HTML: fragment}, nil
			}}
			v := ui.NewTester(func(c *ui.Context) { MarkdownEditor(c, s, o).Fill() }, 800, 500)
			s.native.SetSelection(6, 6)
			s.native.Focus()
			v.Frame()
			v.Key(ui.Cmd, ui.KeyV)
			d := parser.Parse(s.native.Buffer().String())
			wanted := parser.Heading
			if strings.Contains(fragment, "<ul>") {
				wanted = parser.List
			}
			if strings.Contains(fragment, "<table>") {
				wanted = parser.Table
			}
			found := false
			for i := 1; i < d.Len(); i++ {
				if d.Block(i).Kind == wanted {
					found = true
				}
			}
			if !found || !strings.HasPrefix(s.Snapshot().Source, "before\n") || !strings.HasSuffix(s.Snapshot().Source, "\nafter") {
				t.Fatalf("block fragment merged into prose: %q", s.Snapshot().Source)
			}
			v.Command("undo")
			if s.Snapshot().Source != "beforeafter" {
				t.Fatal("fragment separators were not one undo")
			}
		})
	}
}

func TestDailySourceModeDoesNotApplyPendingLiveStyleNavigation(t *testing.T) {
	for _, key := range []string{"space", "enter"} {
		s, v := dailyPublicEditor("")
		v.Key(ui.Cmd, ui.KeyB)
		v.Type("a")
		if err := s.SetMode(Source); err != nil {
			t.Fatal(err)
		}
		v.Frame()
		want := "**a **"
		if key == "space" {
			v.Type(" ")
		} else {
			v.Key(0, ui.KeyEnter)
			want = "**a\n**"
		}
		if s.Snapshot().Source != want {
			t.Fatalf("source mode moved the raw caret: %s %q", key, s.Snapshot().Source)
		}
		v.Command("undo")
		if s.Snapshot().Source != "**a**" {
			t.Fatal("source typing undo not literal")
		}
	}
}

func TestDailyImagePickerPreservesEnteredDescription(t *testing.T) {
	s := NewEditorState(Document{ID: "image-alt", Path: "D:/synthetic/doc.md"})
	o := EditorOptions{OnImportResources: func(_ context.Context, _ ImportRequest, done func(ImportResult, error)) {
		done(ImportResult{Resources: []ImportedResource{{Target: "doc.assets/file.png", Text: "file", Image: true}}}, nil)
	}}
	v := ui.NewTester(func(c *ui.Context) { MarkdownEditor(c, s, o).Fill() }, 800, 500)
	if err := s.Execute(EditorCommand{Kind: InsertImage}); err != nil {
		t.Fatal(err)
	}
	s.insert.text = "填写的说明"
	v.Frame()
	if err := v.Click("选择本地图片"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.Snapshot().Source, "![填写的说明]") {
		t.Fatalf("picker dropped entered alt: %q", s.Snapshot().Source)
	}
}
