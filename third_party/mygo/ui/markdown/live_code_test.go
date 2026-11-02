package markdown

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
)

func assertLiveCodeCard(t *testing.T, s *EditorState, view *ui.Tester, content string) {
	t.Helper()
	if !view.HasText(content) || view.HasText("```") || view.HasText("~~~") {
		t.Fatalf("code did not stay in its card: %q", view.Texts())
	}
	for _, run := range s.runs {
		if strings.Contains(run.text, content) {
			if run.font.Family != s.view.th.CodeFont || run.layout == nil || len(run.boundaries) != len(run.units) {
				t.Fatalf("code lost its shared native geometry: %+v", run)
			}
			return
		}
	}
	t.Fatalf("code is not served by the root's native input runs: %q", content)
}

func chooseLiveCodeLanguage(t *testing.T, view *ui.Tester, label string) {
	t.Helper()
	if err := view.Click("代码语言"); err != nil {
		t.Fatal(err)
	}
	if err := view.Click(label); err != nil {
		t.Fatal(err)
	}
	view.Frame()
}

func TestLiveCodeCardPointerTypingHighlightAndExactUndo(t *testing.T) {
	const source = "\ufeffbefore\r\n\r\n```sql  title=\"query\"\r\nSELECT 1;\r\n```\r\n\r\nafter"
	s, view := liveTester(source, 0, EditorOptions{InPlaceCode: true})
	buffer := s.Buffer()
	assertLiveCodeCard(t, s, view, "SELECT 1;")
	if err := view.Click("SELECT 1;"); err != nil {
		t.Fatal(err)
	}
	a, z := s.Selection()
	if a != z || a < strings.Index(s.source, "SELECT") || a > strings.Index(s.source, ";")+1 {
		t.Fatalf("pointer did not hit code body: %d:%d", a, z)
	}
	view.Key(0, ui.KeyEnd)
	view.Type(" -- 中文😀")
	want := strings.Replace(source, "SELECT 1;", "SELECT 1; -- 中文😀", 1)
	if buffer.OriginalText() != want || s.Buffer() != buffer {
		t.Fatalf("card input changed another source or buffer: %q", buffer.OriginalText())
	}
	assertLiveCodeCard(t, s, view, "SELECT 1; -- 中文😀")
	highlighted := false
	for _, run := range s.runs {
		if strings.Contains(run.text, "SELECT") {
			for _, span := range run.spans {
				if strings.Contains(span.Text, "SELECT") && span.Color != s.view.th.CodeColors.Text {
					highlighted = true
				}
			}
		}
	}
	if !highlighted {
		t.Fatal("active SQL card lost syntax highlighting")
	}
	view.Command("undo")
	if buffer.OriginalText() != source {
		t.Fatal("card typing undo did not restore BOM and CRLF source bytes")
	}
}

func TestLiveCodeLanguageSelectPreservesInfoAliasesAndOneUndo(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n", "\r"} {
		source := strings.ReplaceAll("before\n\n~~~  js\t title=\"query\"  \nconst answer = 1;\n~~~~~  \n\nafter\n", "\n", ending)
		s, view := liveTester(source, strings.Index(normalizedText(source), "answer"), EditorOptions{InPlaceCode: true})
		buffer, version := s.Buffer(), s.Buffer().Version()
		chooseLiveCodeLanguage(t, view, "JavaScript")
		if buffer.Version() != version || buffer.OriginalText() != source {
			t.Fatal("choosing an existing alias changed bytes or history")
		}
		chooseLiveCodeLanguage(t, view, "Python")
		want := strings.Replace(source, "js\t", "python\t", 1)
		if buffer.OriginalText() != want || s.Buffer() != buffer {
			t.Fatalf("language select changed more than the raw token: %q", buffer.OriginalText())
		}
		if s.caret != strings.Index(s.source, "answer") {
			t.Fatal("language selection moved the existing code caret into the hidden opener")
		}
		s.Focus()
		view.Frame()
		view.Command("undo")
		if buffer.OriginalText() != source {
			t.Fatal("one language undo did not restore the original alias and metadata")
		}
		view.Command("redo")
		if buffer.OriginalText() != want {
			t.Fatal("language redo did not use the same buffer history")
		}
	}
}

func TestLiveCodeLanguageUnknownAndMissingInfo(t *testing.T) {
	for _, source := range []string{"```vendor-lang  custom=1\nx\n```", "```\nx\n```", "~~~   \nx\n~~~"} {
		s, view := liveTester(source, strings.Index(source, "\nx")+1, EditorOptions{InPlaceCode: true})
		if strings.Contains(source, "vendor-lang") && !view.HasText("vendor-lang") {
			t.Fatal("unknown code language was discarded")
		}
		chooseLiveCodeLanguage(t, view, "SQL")
		got := s.Buffer().OriginalText()
		if strings.Contains(source, "custom=1") && !strings.Contains(got, "sql  custom=1") || !strings.Contains(got, "sql") {
			t.Fatalf("language selection lost metadata: %q", got)
		}
		view.Command("undo")
		if s.Buffer().OriginalText() != source {
			t.Fatal("unknown/empty info undo changed original bytes")
		}
	}
}

func TestLiveCodeEmptyAndOpenCardsAcceptNativeInput(t *testing.T) {
	for _, fixture := range []struct{ source, want string }{
		{"```sql\r\n```\r\n", "```sql\r\nSELECT 1;\r\n```\r\n"},
		{"```sql", "```sql\nSELECT 1;"},
		{"```sql\n", "```sql\nSELECT 1;"},
		{"> ```sql\r\n> ```\r\n", "> ```sql\r\n> SELECT 1;\r\n> ```\r\n"},
		{"- ```sql\n  ```\n", "- ```sql\n  SELECT 1;\n  ```\n"},
	} {
		s, view := liveTester(fixture.source, 0, EditorOptions{InPlaceCode: true})
		if err := view.Click("Code content"); err != nil {
			t.Fatal(err)
		}
		view.Type("SELECT 1;")
		if s.Buffer().OriginalText() != fixture.want {
			t.Fatalf("empty/open input: %q want %q", s.Buffer().OriginalText(), fixture.want)
		}
		assertLiveCodeCard(t, s, view, "SELECT 1;")
		view.Command("undo")
		if s.Buffer().OriginalText() != fixture.source {
			t.Fatal("empty/open card undo changed original source")
		}
	}
}

func TestLiveCodeNestedNewlinesAndBodyEdgeDeletionProtectSyntax(t *testing.T) {
	for _, fixture := range []struct{ source, want string }{
		{"```sql\r\nSELECT 1;\r\n```\r\n", "```sql\r\nSELECT 1;\r\nSELECT 2;\r\n```\r\n"},
		{"- ```sql\r\n  SELECT 1;\r\n  ```\r\n", "- ```sql\r\n  SELECT 1;\r\n  SELECT 2;\r\n  ```\r\n"},
		{"> ```sql\n> SELECT 1;\n> ```\n", "> ```sql\n> SELECT 1;\n> SELECT 2;\n> ```\n"},
		{"> - ```sql\n>   SELECT 1;\n>   ```\n", "> - ```sql\n>   SELECT 1;\n>   SELECT 2;\n>   ```\n"},
	} {
		normalized := normalizedText(fixture.source)
		start, end := strings.Index(normalized, "SELECT"), strings.Index(normalized, ";")+1
		s, view := liveTester(fixture.source, start, EditorOptions{InPlaceCode: true})
		version := s.Buffer().Version()
		view.Key(0, ui.KeyBackspace)
		if s.Buffer().Version() != version || s.Buffer().OriginalText() != fixture.source {
			t.Fatal("Backspace at the first code column deleted the hidden opener")
		}
		s.SetSelection(end, end)
		view.Frame()
		view.Key(0, ui.KeyDelete)
		if s.Buffer().Version() != version || s.Buffer().OriginalText() != fixture.source {
			t.Fatal("Delete at the final code column deleted the hidden closer")
		}
		view.SetClipboard("\nSELECT 2;")
		view.Command("paste")
		// Preserve the pasted LF; existing CRLF remains literal in other rows.
		want := fixture.want
		if strings.Contains(fixture.source, "\r\n") {
			want = strings.Replace(want, "SELECT 1;\r\n", "SELECT 1;\n", 1)
		}
		if s.Buffer().OriginalText() != want {
			t.Fatalf("nested paste lost a continuation prefix: %q want %q", s.Buffer().OriginalText(), want)
		}
		assertLiveCodeCard(t, s, view, "SELECT 2;")
		view.Command("undo")
		if s.Buffer().OriginalText() != fixture.source {
			t.Fatal("nested paste undo lost exact source")
		}
	}
}

func TestLiveCodePastedClosingFenceExtendsBothEndsInOneUndo(t *testing.T) {
	for _, source := range []string{"```sql\r\nx\r\n```\r\n", "> ```sql\n> x\n> ```\n", "```sql\n```"} {
		s, view := liveTester(source, 0, EditorOptions{InPlaceCode: true})
		if err := view.Click("Code content"); err != nil {
			t.Fatal(err)
		}
		view.SetClipboard("\n```\nSELECT 2;")
		view.Command("paste")
		d := parser.Parse(s.source)
		blocks := 0
		for i := 1; i < d.Len(); i++ {
			if d.Block(i).Kind == parser.CodeBlock {
				blocks++
				if d.Block(i).Open || !strings.Contains(d.Lines(i), "```\nSELECT 2;") {
					t.Fatalf("pasted fence escaped the code card: %q", s.source)
				}
			}
		}
		if blocks != 1 || !strings.Contains(s.source, "````sql") {
			t.Fatalf("paste did not retain one safely fenced block: %q", s.source)
		}
		view.Command("undo")
		if s.Buffer().OriginalText() != source {
			t.Fatal("safe-fence paste required more than one undo or lost original EOLs")
		}
	}
}

func TestLiveCodeNativeIMEPreeditRemainsHighlightedInsideCard(t *testing.T) {
	for _, source := range []string{"```sql\r\nSELECT 甲😀乙;\r\n```\r\n", "> - ```sql\n>   SELECT 甲😀乙;\n>   ```\n"} {
		normalized := normalizedText(source)
		a, z := strings.Index(normalized, "甲"), strings.Index(normalized, "乙")
		changes := 0
		s, view := liveTester(source, a, EditorOptions{InPlaceCode: true, OnChange: func() { changes++ }})
		s.SetSelection(a, z)
		view.Frame()
		version := s.Buffer().Version()
		view.Compose("候选😀\n第二行", editorUnits("候选😀\n第二行"))
		if s.Buffer().OriginalText() != source || s.Buffer().Version() != version || changes != 0 {
			t.Fatal("virtual code preedit changed committed bytes")
		}
		assertLiveCodeCard(t, s, view, "候选😀\n第二行")
		shown := 0
		for _, run := range s.runs {
			shown += strings.Count(run.text, "候选😀")
		}
		if shown != 1 {
			t.Fatalf("IME preedit was duplicated: %d", shown)
		}
		if caret, active := view.TextCaret(); !active || caret.H <= 0 {
			t.Fatalf("code preedit lost native candidate bounds: %+v", caret)
		}
		if marked, active := s.client.MarkedRange(); !active || marked.End-marked.Start != editorUnits("候选😀\n第二行") {
			t.Fatalf("code preedit lost UTF-16 marked range: %+v", marked)
		}
		view.Type("提交😀")
		if changes != 1 || !strings.Contains(s.source, "SELECT 提交😀乙;") {
			t.Fatalf("IME commit did not use one root source transaction: %q", s.source)
		}
		view.Command("undo")
		if s.Buffer().OriginalText() != source {
			t.Fatal("one code IME undo did not restore original bytes")
		}
	}
}

func TestLiveCodeIndentedLanguageConversionPreservesContentsAndContainers(t *testing.T) {
	for _, fixture := range []struct{ source, want string }{
		{"    SELECT 1;\r\n", "```python\r\nSELECT 1;\r\n```\r\n"},
		{"\tSELECT 1;", "```python\nSELECT 1;\n```"},
		{">     SELECT 1;\n", "> ```python\n> SELECT 1;\n> ```\n"},
		{"- item\n\n      SELECT 1;\n", "- item\n\n  ```python\n  SELECT 1;\n  ```\n"},
	} {
		s, view := liveTester(fixture.source, strings.Index(normalizedText(fixture.source), "SELECT"), EditorOptions{InPlaceCode: true})
		assertLiveCodeCard(t, s, view, "SELECT 1;")
		chooseLiveCodeLanguage(t, view, "Python")
		if s.Buffer().OriginalText() != fixture.want {
			t.Fatalf("indented language conversion: %q want %q", s.Buffer().OriginalText(), fixture.want)
		}
		assertLiveCodeCard(t, s, view, "SELECT 1;")
		view.Command("undo")
		if s.Buffer().OriginalText() != fixture.source {
			t.Fatal("indented conversion undo lost original code indentation")
		}
	}
}

func TestLiveCodeReadOnlySourceModeAndCrossBlockSelection(t *testing.T) {
	const source = "before\r\n\r\n```sql\r\nSELECT 1;\r\n```\r\n\r\nafter"
	s, view := liveTester(source, strings.Index(normalizedText(source), "SELECT"), EditorOptions{InPlaceCode: true, ReadOnly: true})
	assertLiveCodeCard(t, s, view, "SELECT 1;")
	view.Click("代码语言")
	view.Type("blocked")
	view.SetClipboard("blocked")
	view.Command("paste")
	if s.Buffer().OriginalText() != source {
		t.Fatal("read-only code accepted body or language input")
	}
	if err := s.setCodeLanguage(1, "python", s.Buffer().Version()); err == nil {
		t.Fatal("read-only language mutation was accepted")
	}
	s, view = liveTester(source, strings.Index(normalizedText(source), "SELECT"), EditorOptions{InPlaceCode: true})
	s.SourceMode = true
	view.Frame()
	if !view.HasText("```sql") || view.HasText("代码语言") {
		t.Fatal("source mode did not show the complete original source")
	}
	view.Key(0, ui.KeyEnd)
	view.Type(" -- source")
	s.SourceMode = false
	view.Frame()
	assertLiveCodeCard(t, s, view, "SELECT 1; -- source")
	view.Command("undo")
	if s.Buffer().OriginalText() != source {
		t.Fatal("mode switching split the shared code undo history")
	}
	a, z := strings.Index(s.source, "fore"), strings.Index(s.source, "SELECT")+len("SELECT")
	snapshot := s.Buffer().Snapshot()
	want := snapshot.OriginalSlice(0, a) + "cross" + snapshot.OriginalSlice(z, snapshot.Len())
	s.SetSelection(a, z)
	view.Frame()
	view.Command("copy")
	if view.Clipboard() != snapshot.OriginalSlice(a, z) {
		t.Fatal("explicit cross-block selection did not copy its full source range")
	}
	view.Type("cross")
	if s.Buffer().OriginalText() != want {
		t.Fatalf("cross-block editing unexpectedly clamped to the code body: %q", s.Buffer().OriginalText())
	}
	view.Command("undo")
	if s.Buffer().OriginalText() != source {
		t.Fatal("cross-block undo lost original source bytes")
	}
}

func TestLiveCodeCustomHooksLegacyDefaultAndChunkBoundaries(t *testing.T) {
	const source = "```sql\nSELECT 1;\n```"
	_, legacy := liveTester(source, strings.Index(source, "SELECT"), EditorOptions{})
	if !legacy.HasText("```sql\nSELECT 1;\n```") || legacy.HasText("代码语言") {
		t.Fatal("zero-value SDK options changed their legacy source activation")
	}
	wrapped := 0
	options := &Options{CodeBlock: func(c *ui.Context, block *CodeBlockView) { wrapped++; DefaultCodeBlock(c, block) },
		Code: map[string]func(*ui.Context, *CodeBlockView){"mermaid": func(c *ui.Context, _ *CodeBlockView) { ui.Text(c, "Custom diagram") }}}
	s, view := liveTester(source+"\n\n```mermaid\ngraph LR\nA-->B\n```", 0, EditorOptions{InPlaceCode: true, Options: options})
	assertLiveCodeCard(t, s, view, "SELECT 1;")
	if wrapped == 0 || !view.HasText("Custom diagram") {
		t.Fatal("in-place code replaced custom Code/CodeBlock rendering hooks")
	}
	s.SetSelection(strings.Index(s.source, "A-->B"), strings.Index(s.source, "A-->B"))
	view.Frame()
	if !view.HasText("```mermaid") {
		t.Fatal("language renderer lost its existing active-source contract")
	}
	chunked := "```python\n" + strings.Repeat("print(1)\n", 32) + "\n```"
	s, view = liveTester(chunked, strings.Index(chunked, "print"), EditorOptions{InPlaceCode: true})
	for _, run := range s.runs {
		if len(run.boundaries) != len(run.units) {
			t.Fatalf("empty last highlight chunk lost its rune mapping: %+v", run)
		}
	}
	view.Key(ui.Ctrl, ui.KeyEnd)
	view.Frame()
}

func TestLiveCodeStaleLanguageAndRejectedFencePasteDoNotWrite(t *testing.T) {
	const source = "before\n\n```sql\nx\n```"
	s, view := liveTester(source, strings.Index(source, "\nx")+1, EditorOptions{InPlaceCode: true})
	version := s.Buffer().Version()
	if err := view.Click("代码语言"); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceRange(0, len("before"), "changed", version); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if err := s.setCodeLanguage(2, "python", version); err == nil {
		t.Fatal("stale language target was accepted")
	}
	if strings.Contains(s.source, "python") || !strings.HasPrefix(s.source, "changed") {
		t.Fatal("stale language action wrote into another version")
	}
	s, view = liveTester(source, strings.Index(source, "\nx")+2, EditorOptions{InPlaceCode: true})
	version = s.Buffer().Version()
	s.Buffer().SetMaxSourceBytes(len(source))
	view.SetClipboard("\n```\n")
	view.Command("paste")
	if s.Buffer().OriginalText() != source || s.Buffer().Version() != version {
		t.Fatal("rejected safe-fence paste partially changed the source")
	}
}
