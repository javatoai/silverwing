package markdown

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown/parser"
)

func TestDailyCodeStructuralKeysKeepLiteralFencesInsideBody(t *testing.T) {
	for _, kind := range []string{"enter", "outdent", "draft-before-code"} {
		t.Run(kind, func(t *testing.T) {
			source := "```sql\n```x\n```\n"
			if kind == "outdent" {
				source = "```sql\n    ```\ntext\n```\n"
			}
			if kind == "draft-before-code" {
				source = "\n\n" + source
			}
			s, v := liveTester(source, 0, dailyOptions())
			if kind == "draft-before-code" {
				v.Type("```")
			}
			before := s.Buffer().OriginalText()
			at := strings.Index(s.source, "```x") + 3
			key, mods := ui.KeyEnter, ui.Modifiers(0)
			if kind == "outdent" {
				at = strings.Index(s.source, "    ") + 4
				key, mods = ui.KeyTab, ui.Shift
			}
			if kind == "draft-before-code" {
				key = ui.KeyDelete
			}
			s.SetSelection(at, at)
			v.Frame()
			v.Key(mods, key)
			d, count := s.editingDocument(), 0
			for i := 1; i < d.Len(); i++ {
				if b := d.Block(i); b.Kind == parser.CodeBlock {
					count++
					if b.Open || b.Language() != "sql" || !strings.Contains(d.Lines(i), "```") {
						t.Fatalf("body escaped its original code: %q", s.source)
					}
				}
			}
			if count != 1 {
				t.Fatalf("created %d code blocks: %q", count, s.source)
			}
			v.Command("undo")
			if s.Buffer().OriginalText() != before {
				t.Fatal("code protection was not one undo")
			}
		})
	}
}

func TestDailyFenceConfirmationWorksInContainersAndAfterBOM(t *testing.T) {
	for _, prefix := range []string{"", "> ", "- ", "\ufeff"} {
		t.Run(prefix, func(t *testing.T) {
			s, v := liveTester(prefix, len(prefix), dailyOptions())
			for n := 0; n < 3; n++ {
				v.Type("`")
			}
			v.Type("sql")
			if s.source != prefix+"```sql" {
				t.Fatalf("pairing intercepted a fence: %q", s.source)
			}
			v.Key(0, ui.KeyEnter)
			a, z := s.Selection()
			code, ok := s.editingCode(a, z)
			if !ok || !code.closed || s.editingDocument().Block(code.block).Language() != "sql" {
				t.Fatalf("Enter failed to confirm container code: %q", s.source)
			}
			v.Command("undo")
			if s.source != prefix+"```sql" {
				t.Fatal("undo lost the literal draft")
			}
		})
	}
}

func TestDailyInlineCommandsPreserveOriginalBytesDirectionAndOtherStyles(t *testing.T) {
	t.Run("partial-crlf", func(t *testing.T) {
		s, v := liveTester("**a\r\nb**", 4, dailyOptions())
		s.SetSelection(4, 5)
		if err := s.Execute(EditorCommand{Kind: CommandBold}); err != nil {
			t.Fatal(err)
		}
		if got := s.Buffer().OriginalText(); got != "**a**\r\nb" {
			t.Fatalf("unselected newline changed: %q", got)
		}
		v.Command("undo")
		if s.Buffer().OriginalText() != "**a\r\nb**" {
			t.Fatal("original bytes not restored")
		}
	})
	t.Run("reverse", func(t *testing.T) {
		s, _ := liveTester("one\r\n\r\ntwo", 0, dailyOptions())
		s.SetSelection(len(s.source), 0)
		if err := s.Execute(EditorCommand{Kind: CommandBold}); err != nil {
			t.Fatal(err)
		}
		a, z := s.Selection()
		if a <= z {
			t.Fatalf("reversed selection lost: %d:%d", a, z)
		}
	})
	for _, kind := range []CommandKind{CommandBold, CommandItalic} {
		t.Run(string(kind), func(t *testing.T) {
			s, _ := liveTester("***hello***", 3, dailyOptions())
			s.SetSelection(3, 8)
			if err := s.Execute(EditorCommand{Kind: kind}); err != nil {
				t.Fatal(err)
			}
			want := "*hello*"
			if kind == CommandItalic {
				want = "**hello**"
			}
			if s.source != want {
				t.Fatalf("removed another format: %q", s.source)
			}
		})
	}
}

func TestDailyRawReplaceRangeDoesNotApplyFutureTypingStyle(t *testing.T) {
	s, v := liveTester("", 0, dailyOptions())
	v.Key(ui.Cmd, ui.KeyB)
	if err := s.ReplaceRange(0, 0, "raw", s.Buffer().Version()); err != nil {
		t.Fatal(err)
	}
	if s.source != "raw" {
		t.Fatalf("explicit raw API formatted text: %q", s.source)
	}
}

func TestDailyInlineCodeCommandPadsLiteralEdgeBackticks(t *testing.T) {
	for _, body := range []string{"a`", "`a"} {
		s, _ := liveTester(body, 0, dailyOptions())
		s.SetSelection(0, len(body))
		if err := s.Execute(EditorCommand{Kind: CommandInlineCode}); err != nil {
			t.Fatal(err)
		}
		d := s.editingDocument()
		found := false
		for i := 1; i < d.Len(); i++ {
			for _, in := range d.Inlines(i) {
				if in.Kind == parser.Code && in.Text == body {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("code command changed literal text: %q => %q", body, s.source)
		}
		a, z := s.Selection()
		if s.source[a:z] != body {
			t.Fatalf("format selected padding instead of content: %d:%d %q", a, z, s.source)
		}
	}
}

func TestDailyParagraphConversionKeepsLiteralPrefixesAndConvertsSetext(t *testing.T) {
	for _, source := range []string{"[x] literal", "1234567890. literal", "title\n====="} {
		t.Run(source, func(t *testing.T) {
			s, _ := liveTester(source, 0, dailyOptions())
			err := s.Execute(EditorCommand{Kind: CommandParagraph})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(source, "\n") {
				for i := 1; i < s.editingDocument().Len(); i++ {
					if s.editingDocument().Block(i).Kind == parser.Heading {
						t.Fatalf("setext still a heading: %q", s.source)
					}
				}
			} else if s.source != source {
				t.Fatalf("literal content stripped: %q", s.source)
			}
		})
	}
}

func TestDailyRenumberAcrossDigitWidthPreservesChildOwnership(t *testing.T) {
	for _, prefix := range []string{"", "> "} {
		t.Run(prefix, func(t *testing.T) {
			source := prefix + "8. a\n" + prefix + "9. b\n" + prefix + "   - child\n"
			s, v := liveTester(source, len(prefix)+4, dailyOptions())
			v.Key(0, ui.KeyEnter)
			d := s.editingDocument()
			owner := -1
			child := -1
			for i := 1; i < d.Len(); i++ {
				b := d.Block(i)
				if b.Kind == parser.Item && b.Ordered && b.Number == 10 {
					owner = i
				}
				if b.Kind == parser.Paragraph && strings.Contains(d.Lines(i), "child") {
					child = i
				}
			}
			if owner < 0 || child < 0 {
				t.Fatalf("missing list payload: %q", s.source)
			}
			for child > 0 && child != owner {
				child = d.Block(child).Parent
			}
			if child != owner {
				t.Fatalf("renumber detached child from item10: %q", s.source)
			}
			v.Command("undo")
			if s.Buffer().OriginalText() != source {
				t.Fatal("renumber/padding was not atomic")
			}
		})
	}
}

func TestDailyFutureStylesSurviveSeparateWhitespaceInput(t *testing.T) {
	for _, kind := range []CommandKind{CommandBold, CommandItalic, CommandStrike, CommandInlineCode} {
		t.Run(string(kind), func(t *testing.T) {
			s, v := liveTester("", 0, dailyOptions())
			if err := s.Execute(EditorCommand{Kind: kind}); err != nil {
				t.Fatal(err)
			}
			v.Type("hello")
			v.Type(" ")
			v.Type("world")
			d := parser.Parse(s.source)
			var plain strings.Builder
			styled := false
			for i := 1; i < d.Len(); i++ {
				inlines := d.Inlines(i)
				for _, in := range inlines {
					if in.Kind == parser.Text || in.Kind == parser.Code {
						plain.WriteString(in.Text)
					}
					if in.Kind == []parser.InlineKind{parser.Strong, parser.Emphasis, parser.Strikethrough, parser.Code}[inlineCommandIndex(kind)] {
						styled = true
					}
				}
			}
			if plain.String() != "hello world" || !styled || !s.QueryCommand(EditorCommand{Kind: kind}).Active {
				t.Fatalf("space broke future style: %q => %q", s.source, plain.String())
			}
		})
	}
}

func TestDailyCrossParagraphFormatEditsEachASTContainerOnce(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n"} {
		source := strings.ReplaceAll("**a\nb**\n\n**c**", "\n", ending)
		s, v := liveTester(source, 2, dailyOptions())
		s.SetSelection(2, 12)
		if err := s.Execute(EditorCommand{Kind: CommandBold}); err != nil {
			t.Fatal(err)
		}
		if want := strings.ReplaceAll("a\nb\n\nc", "\n", ending); s.Buffer().OriginalText() != want {
			t.Fatalf("overlapping format corrupted source: %q", s.Buffer().OriginalText())
		}
		v.Command("undo")
		if s.Buffer().OriginalText() != source {
			t.Fatal("format was not one exact undo")
		}
	}
}

func TestDailyStructuralExitAndDeletionKeepLeadingBOM(t *testing.T) {
	for _, kind := range []string{"empty-code", "empty-table", "delete-code", "delete-heading"} {
		t.Run(kind, func(t *testing.T) {
			source := "\ufeff```sql\r\n\r\n```\r\n"
			if kind == "empty-table" {
				source = "\ufeff|  |  |\r\n|---|---|\r\n|  |  |\r\n"
			}
			if kind == "delete-heading" {
				source = "\ufeff# title\r\n"
			}
			s, v := liveTester(source, 0, dailyOptions())
			at := strings.Index(s.source, "\n") + 1
			if kind == "empty-table" {
				for i := 1; i < s.editingDocument().Len(); i++ {
					if s.editingDocument().Block(i).Kind == parser.Table {
						at = s.readTable(s.editingDocument(), i).rows[0][0].start
						break
					}
				}
			}
			if kind == "delete-heading" {
				at = strings.Index(s.source, "title")
			}
			s.SetSelection(at, at)
			v.Frame()
			if strings.HasPrefix(kind, "delete") {
				if err := s.Execute(EditorCommand{Kind: CommandDeleteBlock}); err != nil {
					t.Fatal(err)
				}
			} else {
				v.Key(0, ui.KeyBackspace)
			}
			if !strings.HasPrefix(s.Buffer().OriginalText(), "\ufeff") || s.Buffer().OriginalText() == source {
				t.Fatalf("BOM lost or structural action ignored: %q", s.Buffer().OriginalText())
			}
			v.Command("undo")
			if s.Buffer().OriginalText() != source {
				t.Fatal("BOM original bytes not restored")
			}
		})
	}
}

func TestDailySourceCommandRejectsOverlappingEditsAtomically(t *testing.T) {
	s, _ := liveTester("abcd", 0, dailyOptions())
	version := s.Buffer().Version()
	err := s.ApplyEdits([]editor.Edit{{Start: 0, End: 2, Text: "x"}, {Start: 1, End: 3, Text: "y"}}, 0, 0, version)
	if err == nil || s.source != "abcd" || s.Buffer().Version() != version {
		t.Fatal("overlapping command was not rejected atomically")
	}
}

func TestDailyFormattingMixedAndAdjacentExistingStyleKeepsValidMarkup(t *testing.T) {
	s, v := liveTester("one **two** three", 0, dailyOptions())
	s.SetSelection(0, len(s.source))
	if err := s.Execute(EditorCommand{Kind: CommandBold}); err != nil {
		t.Fatal(err)
	}
	if s.source != "**one two three**" {
		t.Fatalf("mixed style produced nested same delimiters: %q", s.source)
	}
	v.Command("undo")
	if s.source != "one **two** three" {
		t.Fatal("mixed formatting changed unrelated source")
	}
	s, v = liveTester("**abc**", 3, dailyOptions())
	s.SetSelection(3, 4)
	if err := s.Execute(EditorCommand{Kind: CommandBold}); err != nil {
		t.Fatal(err)
	}
	if s.source != "**a**b**c**" {
		t.Fatalf("partial toggle failed: %q", s.source)
	}
	if err := s.Execute(EditorCommand{Kind: CommandBold}); err != nil {
		t.Fatal(err)
	}
	if s.source != "**abc**" {
		t.Fatalf("adjacent styles did not merge without literal markers: %q", s.source)
	}
	v.Command("undo")
	if s.source != "**a**b**c**" {
		t.Fatal("merged format was not one undo")
	}
}

func TestDailyCapturedInsertionRejectsOtherBufferVersionAndRestoresOnLimit(t *testing.T) {
	s, _ := liveTester("hello", 2, dailyOptions())
	point := s.CaptureInsertionPoint()
	other, _ := liveTester("hello", 2, dailyOptions())
	if err := other.InsertMarkdownAt(point, "x"); err == nil || other.source != "hello" {
		t.Fatal("insertion point crossed buffer identity")
	}
	s.SetSelection(4, 4)
	s.Buffer().SetMaxSourceBytes(5)
	if err := s.InsertMarkdownAt(point, "oversize"); err == nil || s.source != "hello" {
		t.Fatal("oversize insertion changed source")
	}
	a, z := s.Selection()
	if a != 4 || z != 4 {
		t.Fatal("rejected insertion replaced newer selection")
	}
	s.Buffer().SetMaxSourceBytes(-1)
	if err := s.ReplaceRange(0, 0, "new", s.Buffer().Version()); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertMarkdownAt(point, "late"); err == nil {
		t.Fatal("stale insertion version accepted")
	}
}

func TestDailyRemovingStyledTextLeavesNoEmptyMarkers(t *testing.T) {
	for _, source := range []string{"**x**", "***x***", "~~x~~", "`x`"} {
		s, v := liveTester(source, 0, dailyOptions())
		at := strings.Index(source, "x")
		s.SetSelection(at, at+1)
		v.Key(0, ui.KeyBackspace)
		if s.source != "" {
			t.Fatalf("empty style left markers: %q", s.source)
		}
		v.Command("undo")
		if s.source != source {
			t.Fatal("empty-format cleanup was not one undo")
		}
	}
	s, v := liveTester("", 0, dailyOptions())
	if err := s.Execute(EditorCommand{Kind: CommandBold}); err != nil {
		t.Fatal(err)
	}
	v.Type("hello")
	v.Type(" ")
	v.Key(0, ui.KeyBackspace)
	v.Type("world")
	if s.source != "**helloworld**" {
		t.Fatalf("resumed future style created adjacent markers: %q", s.source)
	}
}

func TestDailyDraftPolicyPreservesOmittedCellIntentAcrossRichUndo(t *testing.T) {
	s, v := liveTester("\n\n| A | B | C |\n|---|---|---|\n| x |", 0, dailyOptions())
	v.Type("```")
	at := strings.Index(s.source, "C")
	s.SetSelection(at, at)
	v.Frame()
	v.Key(0, ui.KeyDown)
	before := s.source
	if err := s.Execute(EditorCommand{Kind: CommandInsertLink, Text: "chosen", Target: "https://example.com"}); err != nil {
		t.Fatal(err)
	}
	v.Command("undo")
	if s.source != before {
		t.Fatal("rich undo changed literal draft/table source")
	}
	if hit := s.focusedOmittedTableCell(); hit == nil || hit.cell.column != 2 {
		t.Fatal("draft-aware undo lost omitted third-column intent")
	}
	v.Type("again")
	d := s.editingDocument()
	found := false
	for i := 1; i < d.Len(); i++ {
		if d.Block(i).Kind == parser.Table {
			table := s.readTable(d, i)
			cell := table.rows[1][2]
			found = s.source[cell.start:cell.end] == "again"
		}
	}
	if !found {
		t.Fatalf("input after rich undo targeted wrong cell: %q", s.source)
	}
}

func TestDailyFutureStylesKeepBatchWhitespaceAndOriginalDelimiterAliases(t *testing.T) {
	for _, kind := range []CommandKind{CommandBold, CommandItalic, CommandStrike} {
		for _, via := range []string{"typing", "paste"} {
			t.Run(string(kind)+via, func(t *testing.T) {
				s, v := liveTester("", 0, dailyOptions())
				if err := s.Execute(EditorCommand{Kind: kind}); err != nil {
					t.Fatal(err)
				}
				if via == "typing" {
					v.Type(" hello ")
				} else {
					v.SetClipboard(" hello ")
					v.Key(ui.Cmd, ui.KeyV)
				}
				marker := inlineMarkers[inlineCommandIndex(kind)]
				if want := " " + marker + "hello" + marker + " "; s.source != want {
					t.Fatalf("batch boundary corrupted style: %q want %q", s.source, want)
				}
				v.Type("next")
				d := parser.Parse(s.source)
				var text strings.Builder
				for b := 1; b < d.Len(); b++ {
					for _, in := range d.Inlines(b) {
						if in.Kind == parser.Text {
							text.WriteString(in.Text)
						}
					}
				}
				if text.String() != "hello next" {
					t.Fatalf("batch continuation exposed markers: %q", s.source)
				}
			})
		}
	}
	s, v := liveTester("", 0, dailyOptions())
	_ = s.Execute(EditorCommand{Kind: CommandBold})
	v.Type("hello")
	v.SetClipboard(" world ")
	v.Key(ui.Cmd, ui.KeyV)
	if s.source != "**hello world** " {
		t.Fatalf("existing style closing not moved past body: %q", s.source)
	}
	s, v = liveTester("__hello__", 3, dailyOptions())
	_ = s.Execute(EditorCommand{Kind: CommandBold})
	v.Type(" X ")
	if s.source != "__h__ X __ello__" {
		t.Fatalf("original delimiter alias lost: %q", s.source)
	}
}

func TestDailyEmptyHeadingConvertsWithoutKeepingOldMarker(t *testing.T) {
	for _, command := range []EditorCommand{{Kind: CommandParagraph}, {Kind: CommandHeading, Level: 2}} {
		s, v := liveTester("# ", 2, dailyOptions())
		if err := s.Execute(command); err != nil {
			t.Fatal(err)
		}
		want := ""
		if command.Kind == CommandHeading {
			want = "## "
		}
		if s.source != want {
			t.Fatalf("empty heading kept old marker: %q", s.source)
		}
		v.Command("undo")
		if s.source != "# " {
			t.Fatal("empty heading conversion not one undo")
		}
	}
}

func TestDailyStyleStatusMatchesCrossParagraphToggle(t *testing.T) {
	s, _ := liveTester("**a**\n\n**b**", 2, dailyOptions())
	s.SetSelection(2, 10)
	if !s.QueryCommand(EditorCommand{Kind: CommandBold}).Active {
		t.Fatal("fully styled selection reported inactive")
	}
	if err := s.Execute(EditorCommand{Kind: CommandBold}); err != nil {
		t.Fatal(err)
	}
	if s.QueryCommand(EditorCommand{Kind: CommandBold}).Active || s.source != "a\n\nb" {
		t.Fatal("query/execution states disagree")
	}
}

func TestDailyAddingStyleAcrossPartialOtherStyleKeepsAllSemantics(t *testing.T) {
	for _, source := range []string{"English\n\n**粗体**、*斜体*", "xa*斜体*by"} {
		t.Run(source, func(t *testing.T) {
			s, v := liveTester(source, 0, dailyOptions())
			a := 0
			if strings.HasPrefix(source, "x") {
				a = 1
			}
			z := strings.Index(source, "斜") + len("斜")
			s.SetSelection(a, z)
			if err := s.Execute(EditorCommand{Kind: CommandBold}); err != nil {
				t.Fatal(err)
			}
			d := parser.Parse(s.source)
			var plain, bold, italic strings.Builder
			for b := 1; b < d.Len(); b++ {
				ins := d.Inlines(b)
				for _, in := range ins {
					if in.Kind != parser.Text {
						continue
					}
					plain.WriteString(in.Text)
					isBold, isItalic := false, false
					for _, owner := range ins {
						if owner.Start <= in.Start && owner.End >= in.End {
							isBold = isBold || owner.Kind == parser.Strong
							isItalic = isItalic || owner.Kind == parser.Emphasis
						}
					}
					if isBold {
						bold.WriteString(in.Text)
					}
					if isItalic {
						italic.WriteString(in.Text)
					}
				}
			}
			wantPlain, wantBold := "English粗体、斜体", "English粗体、斜"
			if a == 1 {
				wantPlain, wantBold = "xa斜体by", "a斜"
			}
			if plain.String() != wantPlain || bold.String() != wantBold || italic.String() != "斜体" {
				t.Fatalf("crossing styles changed meaning: %q plain=%q bold=%q italic=%q", s.source, plain.String(), bold.String(), italic.String())
			}
			v.Command("undo")
			if s.source != source {
				t.Fatal("balanced style edit was not one exact undo")
			}
		})
	}
}

func TestDailyFutureStyleIMEBatchWhitespaceCommitsOnce(t *testing.T) {
	for _, prefix := range []string{"", "a"} {
		s, v := liveTester("", 0, dailyOptions())
		_ = s.Execute(EditorCommand{Kind: CommandBold})
		if prefix != "" {
			v.Type(prefix)
		}
		before := s.Buffer().OriginalText()
		version := s.Buffer().Version()
		v.Compose("候选😀 ", editorUnits("候选😀 "))
		if !s.Composing() || s.Buffer().Version() != version || s.Buffer().OriginalText() != before {
			t.Fatal("preedit changed original source")
		}
		v.Type("候选😀 ")
		if want := "**" + prefix + "候选😀** "; s.source != want || s.Composing() {
			t.Fatalf("IME trailing whitespace broke style: %q", s.source)
		}
		v.Command("undo")
		if s.Buffer().OriginalText() != before {
			t.Fatal("IME batch and delimiter move were not one undo")
		}
	}
}

func TestDailyRemovingStyleAcrossPartialOtherStyleKeepsAllSemantics(t *testing.T) {
	const source = "**plain *斜体* tail**"
	s, v := liveTester(source, 2, dailyOptions())
	z := strings.Index(source, "斜") + len("斜")
	s.SetSelection(2, z)
	if err := s.Execute(EditorCommand{Kind: CommandBold}); err != nil {
		t.Fatal(err)
	}
	d := parser.Parse(s.source)
	var plain, bold, italic strings.Builder
	for b := 1; b < d.Len(); b++ {
		ins := d.Inlines(b)
		for _, in := range ins {
			if in.Kind != parser.Text {
				continue
			}
			plain.WriteString(in.Text)
			isBold, isItalic := false, false
			for _, owner := range ins {
				if owner.Start <= in.Start && owner.End >= in.End {
					isBold = isBold || owner.Kind == parser.Strong
					isItalic = isItalic || owner.Kind == parser.Emphasis
				}
			}
			if isBold {
				bold.WriteString(in.Text)
			}
			if isItalic {
				italic.WriteString(in.Text)
			}
		}
	}
	if plain.String() != "plain 斜体 tail" || bold.String() != "体 tail" || italic.String() != "斜体" {
		t.Fatalf("partial removal crossed another style: %q plain=%q bold=%q italic=%q", s.source, plain.String(), bold.String(), italic.String())
	}
	v.Command("undo")
	if s.source != source {
		t.Fatal("partial removal not one undo")
	}
}

func TestDailyFormatWithExternalReferenceDefinitionRejectsCrossedLink(t *testing.T) {
	const source = "[label][id] tail\r\n\r\n[id]: https://example.com \"title\"\r\n"
	for _, command := range []CommandKind{CommandBold, CommandItalic, CommandStrike} {
		s, _ := liveTester(source, 0, dailyOptions())
		s.SetSelection(3, 14)
		version := s.Buffer().Version()
		if err := s.Execute(EditorCommand{Kind: command}); err == nil {
			t.Fatalf("crossed reference link was accepted: %q", s.Buffer().OriginalText())
		}
		if s.Buffer().OriginalText() != source || s.Buffer().Version() != version {
			t.Fatal("unsafe reference formatting changed source/history")
		}
	}
	s, v := liveTester(source, 1, dailyOptions())
	s.SetSelection(1, 6)
	if err := s.Execute(EditorCommand{Kind: CommandBold}); err != nil {
		t.Fatal(err)
	}
	d := parser.Parse(s.source)
	link, bold := false, false
	for b := 1; b < d.Len(); b++ {
		for _, in := range d.Inlines(b) {
			link = link || in.Kind == parser.Link && in.Dest == "https://example.com" && in.Title == "title"
			bold = bold || in.Kind == parser.Strong
		}
	}
	if !link || !bold {
		t.Fatal("safe reference label formatting lost link/style")
	}
	v.Command("undo")
	if s.Buffer().OriginalText() != source {
		t.Fatal("reference formatting was not one exact undo")
	}
}

func TestDailyFutureFormatsDoNotWrapPastedBlocks(t *testing.T) {
	for _, text := range []string{"one\n\ntwo", "```sql\nx\n```"} {
		for _, prefix := range []string{"", "hello"} {
			for _, plain := range []bool{false, true} {
				s, v := liveTester("", 0, dailyOptions())
				_ = s.Execute(EditorCommand{Kind: CommandBold})
				if prefix != "" {
					v.Type(prefix)
				}
				before := s.Buffer().OriginalText()
				v.SetClipboard(text)
				mods := ui.Cmd
				if plain {
					mods |= ui.Shift
				}
				v.Key(mods, ui.KeyV)
				if prefix == "" && s.source != text {
					t.Fatalf("bulk Markdown was wrapped: %q", s.source)
				}
				if strings.HasPrefix(text, "```") {
					d, count := parser.Parse(s.source), 0
					for b := 1; b < d.Len(); b++ {
						if d.Block(b).Kind == parser.CodeBlock && d.Block(b).Language() == "sql" && strings.TrimSpace(d.Lines(b)) == "x" {
							count++
						}
					}
					if count != 1 {
						t.Fatalf("complete code paste lost its block: %q", s.source)
					}
				} else if strings.Contains(s.source, "**one") || strings.Contains(s.source, "two**") {
					t.Fatalf("style crossed paragraph boundary: %q", s.source)
				}
				v.Command("undo")
				if s.Buffer().OriginalText() != before || !s.QueryCommand(EditorCommand{Kind: CommandBold}).Active {
					t.Fatal("bulk paste did not undo source and future style together")
				}
			}
		}
	}
}

func TestDailyInlineCodeCannotExposeCrossedHiddenMarkup(t *testing.T) {
	const source = "*italic* tail"
	s, _ := liveTester(source, 3, dailyOptions())
	s.SetSelection(3, 11)
	version := s.Buffer().Version()
	if err := s.Execute(EditorCommand{Kind: CommandInlineCode}); err == nil {
		t.Fatalf("crossed code style accepted: %q", s.source)
	}
	if s.source != source || s.Buffer().Version() != version {
		t.Fatal("unsafe code formatting changed source")
	}
	s, v := liveTester(source, 1, dailyOptions())
	s.SetSelection(1, 7)
	if err := s.Execute(EditorCommand{Kind: CommandInlineCode}); err != nil {
		t.Fatal(err)
	}
	if s.source != "*`italic`* tail" {
		t.Fatalf("safe inline code lost enclosing style: %q", s.source)
	}
	v.Command("undo")
	if s.source != source {
		t.Fatal("safe code format not one undo")
	}
}

func TestDailySelectedLinkCannotExposeCrossedHiddenMarkup(t *testing.T) {
	const source = "*italic* tail"
	s, _ := liveTester(source, 3, dailyOptions())
	s.SetSelection(3, 11)
	version := s.Buffer().Version()
	if err := s.Execute(EditorCommand{Kind: CommandInsertLink, Target: "https://example.com"}); err == nil {
		t.Fatalf("crossed label source accepted: %q", s.source)
	}
	if s.source != source || s.Buffer().Version() != version {
		t.Fatal("unsafe selected link changed source")
	}
}

func TestDailyInlineCodeLiteralBacktickAndEdgeSpacesStayCode(t *testing.T) {
	for _, kind := range []string{"existing", "future", "first-paste", "edge-spaces"} {
		t.Run(kind, func(t *testing.T) {
			source := "`hello`"
			at := 6
			want := "hello`"
			if kind == "future" || kind == "first-paste" {
				source = ""
				at = 0
			}
			if kind == "edge-spaces" {
				source = "`x `"
				at = 1
				want = " x "
			}
			s, v := liveTester(source, at, dailyOptions())
			if kind == "future" || kind == "first-paste" {
				_ = s.Execute(EditorCommand{Kind: CommandInlineCode})
				if kind == "future" {
					v.Type("hello")
				} else {
					want = "`"
				}
			}
			before := s.Buffer().OriginalText()
			s.Buffer().EndUndoGroup()
			if kind == "edge-spaces" {
				v.Type(" ")
			} else if kind == "first-paste" {
				v.SetClipboard("`")
				v.Key(ui.Cmd, ui.KeyV)
			} else {
				v.Type("`")
			}
			d := parser.Parse(s.source)
			found := false
			for b := 1; b < d.Len(); b++ {
				for _, in := range d.Inlines(b) {
					found = found || in.Kind == parser.Code && in.Text == want
				}
			}
			if !found {
				t.Fatalf("literal code edit lost content/style: %q want %q", s.source, want)
			}
			v.Command("undo")
			if s.Buffer().OriginalText() != before {
				t.Fatal("inline code body/fence not one undo")
			}
		})
	}
}

func TestDailyImageCannotExposeCrossedHiddenMarkup(t *testing.T) {
	const source = "*italic* tail"
	s, _ := liveTester(source, 3, dailyOptions())
	s.SetSelection(3, 11)
	version := s.Buffer().Version()
	if err := s.Execute(EditorCommand{Kind: CommandInsertImage, Target: "https://example.com/a.png"}); err == nil {
		t.Fatalf("crossed image accepted: %q", s.source)
	}
	if s.source != source || s.Buffer().Version() != version {
		t.Fatal("rejected image changed source")
	}
}

func TestDailyInlineCodeRejectsMultipleParagraphsWithoutLosingSpan(t *testing.T) {
	for _, future := range []bool{false, true} {
		for _, payload := range []string{"one\n\ntwo", "```sql\nx\n```"} {
			source := "`hello`"
			at := 6
			if future {
				source = ""
				at = 0
			}
			s, v := liveTester(source, at, dailyOptions())
			if future {
				_ = s.Execute(EditorCommand{Kind: CommandInlineCode})
				v.Type("hello")
			}
			before := s.Buffer().OriginalText()
			version := s.Buffer().Version()
			s.Buffer().EndUndoGroup()
			v.SetClipboard(payload)
			v.Key(ui.Cmd, ui.KeyV)
			if s.Buffer().OriginalText() != before || s.Buffer().Version() != version {
				t.Fatalf("multiblock broke inline code: %q", s.source)
			}
		}
	}
}

func TestDailyInlineCodeRejectsAnEmptyLineCreatedBySurroundingBody(t *testing.T) {
	for _, kind := range []string{"insert-after-existing-LF", "trailing-LF", "space-line", "delete-line-text"} {
		t.Run(kind, func(t *testing.T) {
			source := "`a\nb`"
			at := 3
			if kind == "trailing-LF" {
				source = "`hello`"
				at = 6
			}
			if kind == "space-line" || kind == "delete-line-text" {
				source = "`a\nx\nb`"
				at = 3
			}
			s, v := liveTester(source, at, dailyOptions())
			if kind == "space-line" {
				s.SetSelection(3, 4)
			} else if kind == "delete-line-text" {
				s.SetSelection(4, 4)
			}
			v.Frame()
			version := s.Buffer().Version()
			switch kind {
			case "trailing-LF":
				v.SetClipboard("one\n\n")
				v.Key(ui.Cmd, ui.KeyV)
			case "space-line":
				v.Type(" ")
			case "delete-line-text":
				v.Key(0, ui.KeyBackspace)
			default:
				v.Type("\n")
			}
			if s.source != source || s.Buffer().Version() != version {
				t.Fatalf("combined code body was split: %q", s.source)
			}
		})
	}
}

func TestDailyInlineCodeContextualBodyGuard(t *testing.T) {
	for _, source := range []string{"> `a\n> b`", "- `a\n  b`", "1. `a\n   b`"} {
		t.Run(source, func(t *testing.T) {
			at := strings.Index(source, "b")
			s, v := liveTester(source, at, dailyOptions())
			version := s.Buffer().Version()
			v.Type("\n")
			if s.source != source || s.Buffer().Version() != version {
				t.Fatalf("container continuation broke inline code: %q", s.source)
			}
			v.Type("x")
			d := parser.Parse(s.source)
			found := false
			for b := 1; b < d.Len(); b++ {
				for _, in := range d.Inlines(b) {
					found = found || in.Kind == parser.Code && in.Text == "a xb"
				}
			}
			if !found {
				t.Fatalf("ordinary container code input was rejected: %q", s.source)
			}
		})
	}
}

func TestDailyCodeBacktickInputIsLiteralAndUsesValidatedPlan(t *testing.T) {
	for _, row := range []struct{ source, want string }{
		{"`a `", "a `"},
		{"`a\nb `", "a b `"},
		{"> `a\n> b `", "a b `"},
		{"- `a\n  b `", "a b `"},
		{"1. `a\n   b `", "a b `"},
	} {
		t.Run(row.source, func(t *testing.T) {
			at := strings.LastIndex(row.source, "`")
			s, v := liveTester(row.source, at, dailyOptions())
			s.Buffer().EndUndoGroup()
			v.Type("`")
			d := parser.Parse(s.source)
			found := false
			for b := 1; b < d.Len(); b++ {
				if d.Block(b).Kind == parser.CodeBlock {
					t.Fatalf("literal key became a fenced block: %q", s.source)
				}
				for _, in := range d.Inlines(b) {
					found = found || in.Kind == parser.Code && in.Text == row.want
				}
			}
			if !found {
				t.Fatalf("literal input was duplicated or lost: %q", s.source)
			}
			a, z := s.Selection()
			_, _, left, right, ok := s.inlineContainer(3, a, z)
			if !ok || a != z || a < left || z > right {
				t.Fatal("caret escaped code body")
			}
			v.Command("undo")
			if s.Buffer().OriginalText() != row.source {
				t.Fatal("body and delimiters did not undo together")
			}
			v.Command("redo")
			if s.Buffer().OriginalText() == row.source {
				t.Fatal("code input did not redo")
			}
		})
	}
	source := "```sql\nselect \n```"
	at := strings.Index(source, "select ") + len("select ")
	s, v := liveTester(source, at, dailyOptions())
	s.Buffer().EndUndoGroup()
	v.Type("`")
	d := parser.Parse(s.source)
	found := false
	for b := 1; b < d.Len(); b++ {
		if d.Block(b).Kind == parser.CodeBlock && strings.Contains(d.Lines(b), "select `\n") {
			found = true
		}
	}
	if !found {
		t.Fatalf("code block key was paired instead of literal: %q", s.source)
	}
	v.Command("undo")
	if s.Buffer().OriginalText() != source {
		t.Fatal("code block input did not undo once")
	}
}
