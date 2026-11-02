package markdown

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui/markdown/parser"
)

func TestCode002QuoteTabFencePasteRemainsOneCodeBlock(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n", "\r"} {
		t.Run(strings.ReplaceAll(strings.ReplaceAll(ending, "\r", "CR"), "\n", "LF"), func(t *testing.T) {
			source := strings.ReplaceAll("> ```sql\n> x\n> ```\n", "\n", ending)
			want := strings.ReplaceAll("> ````sql\n> \t```\n> SELECT 2;x\n> ````\n", "\n", ending)
			at := strings.Index(normalizedText(source), "x")
			s, view := liveTester(source, at, EditorOptions{InPlaceCode: true})
			buffer := s.Buffer()
			view.SetClipboard("\t```" + ending + "SELECT 2;")
			view.Command("paste")
			if got := buffer.OriginalText(); got != want {
				t.Fatalf("quote-column tab closed the live code card: %q want %q", got, want)
			}
			d := parser.Parse(s.source)
			count := 0
			for i := 1; i < d.Len(); i++ {
				if d.Block(i).Kind == parser.CodeBlock {
					count++
					if d.Block(i).Open || d.Lines(i) != "\t```\nSELECT 2;x\n" {
						t.Fatalf("paste escaped or changed code contents: %q", d.Lines(i))
					}
				}
			}
			if count != 1 || s.Buffer() != buffer || !view.HasText("SELECT 2;x") {
				t.Fatalf("safe paste lost the one root code card: blocks=%d texts=%q", count, view.Texts())
			}
			view.Command("undo")
			if buffer.OriginalText() != source {
				t.Fatal("one undo did not restore the exact original quote/fence/EOL bytes")
			}
			view.Command("redo")
			if buffer.OriginalText() != want {
				t.Fatal("safe quote/tab paste did not redo as one source transaction")
			}
		})
	}
}

func TestCode002CandidateFenceGuardUsesActualContainerColumns(t *testing.T) {
	for _, fixture := range []struct{ source, paste, want string }{
		// A root tab occupies four columns, while a quote consumes one or
		// two columns before the same tab. These must not share a heuristic.
		{"```sql\nx\n```\n", "\t```\nSELECT 2;", "```sql\n\t```\nSELECT 2;x\n```\n"},
		{">```sql\n>x\n>```\n", "\t```\nSELECT 2;", ">````sql\n>\t```\n>SELECT 2;x\n>````\n"},
		{"> > ```sql\n> > x\n> > ```\n", "\t```\nSELECT 2;", "> > ```sql\n> > \t```\n> > SELECT 2;x\n> > ```\n"},
		// The long, four-column literal is never a closer, even when another
		// inserted line really requires extending the outer fence.
		{"```sql\nx\n```\n", "\n```\n    ````````\n`````\nSELECT 2;", "``````sql\n\n```\n    ````````\n`````\nSELECT 2;x\n``````\n"},
	} {
		t.Run(fixture.source+fixture.paste, func(t *testing.T) {
			at := strings.Index(fixture.source, "x")
			s, view := liveTester(fixture.source, at, EditorOptions{InPlaceCode: true})
			view.SetClipboard(fixture.paste)
			view.Command("paste")
			if got := s.Buffer().OriginalText(); got != fixture.want {
				t.Fatalf("guard did not follow the parser's actual closing-fence rules: %q want %q", got, fixture.want)
			}
			view.Command("undo")
			if s.Buffer().OriginalText() != fixture.source {
				t.Fatal("candidate guard changed original bytes outside its one undo step")
			}
		})
	}
}

func TestCode003LiteralFenceLikeBodyNeverRewrittenAsCloser(t *testing.T) {
	for _, fixture := range []struct{ source, want string }{
		{"```sql\n>```\n", "````sql\n\n```\nx>```\n"},
		{"```sql\n    ```\n", "````sql\n\n```\nx    ```\n"},
		{"> > ```sql\n> > x\n> ```\n", "> > ````sql\n> > \n> > ```\n> > xx\n> ```\n"},
	} {
		t.Run(fixture.source, func(t *testing.T) {
			at := strings.IndexByte(fixture.source, '\n') + 1
			if strings.HasPrefix(fixture.source, "> >") {
				at = strings.Index(fixture.source, "x")
			}
			s, view := liveTester(fixture.source, at, EditorOptions{InPlaceCode: true})
			buffer := s.Buffer()
			view.SetClipboard("\n```\nx")
			view.Command("paste")
			if got := buffer.OriginalText(); got != fixture.want {
				t.Fatalf("a literal body line or partial container close was rewritten as the fence: %q want %q", got, fixture.want)
			}
			d := parser.Parse(s.source)
			for i := 1; i < d.Len(); i++ {
				if d.Block(i).Kind == parser.CodeBlock && d.Block(i).Language() == "sql" {
					if !strings.Contains(d.Lines(i), "```\nx") {
						t.Fatalf("inserted literal fence escaped the original code body: %q", d.Lines(i))
					}
					break
				}
			}
			if s.Buffer() != buffer || !view.HasText("```") {
				t.Fatal("literal fence body no longer belongs to the shared code card")
			}
			view.Command("undo")
			if buffer.OriginalText() != fixture.source {
				t.Fatal("literal body preservation did not undo to the exact original source")
			}
		})
	}
}

func TestCode004LanguageEntityMetadataPreserved(t *testing.T) {
	for _, info := range []string{"sql&#x20;title=x", "s&#113;l&#9;title=x", "sql&Tab;title=x"} {
		t.Run(info, func(t *testing.T) {
			source := "\ufeffbefore\r\n\r\n```" + info + "\r\nx\r\n```\r\n\rafter"
			at := strings.Index(normalizedText(source), "\nx\n") + 1
			s, view := liveTester(source, at, EditorOptions{InPlaceCode: true})
			buffer, version := s.Buffer(), s.Buffer().Version()
			chooseLiveCodeLanguage(t, view, "SQL")
			if buffer.OriginalText() != source || buffer.Version() != version {
				t.Fatal("selecting the decoded language changed its raw entity spelling")
			}
			chooseLiveCodeLanguage(t, view, "Python")
			separator := strings.Index(info, "&#x20;")
			if separator < 0 {
				separator = strings.Index(info, "&#9;")
			}
			if separator < 0 {
				separator = strings.Index(info, "&Tab;")
			}
			want := strings.Replace(source, info, "python"+info[separator:], 1)
			if got := buffer.OriginalText(); got != want {
				t.Fatalf("language edit deleted raw metadata after a decoded separator: %q want %q", got, want)
			}
			if s.Buffer() != buffer || s.caret != strings.Index(s.source, "\nx\n")+1 {
				t.Fatal("language change lost the root Buffer or code caret")
			}
			assertLiveCodeCard(t, s, view, "x")
			view.Command("undo")
			if buffer.OriginalText() != source {
				t.Fatal("one language undo did not restore entity metadata and mixed EOL bytes")
			}
			view.Command("redo")
			if buffer.OriginalText() != want {
				t.Fatal("language metadata edit did not redo through the same history")
			}
		})
	}
}
