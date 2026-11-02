package desktop

import (
	"os"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func csvClickFixture(t *testing.T, source string, session string) (*app, *ui.Tester) {
	t.Helper()
	s, action, result := materialsTextPreviewFixture(t, "click.csv", []byte(source))
	s.applyMaterialsActionResult(action, result)
	s.materialsUnit.state.documentEditing = false
	if session != "none" {
		s.readingSession = &desktopReadingSession{cache: EmptyReadingSnapshot(), scrollStates: map[string]readingScrollBinding{}, closed: session == "closed"}
	}
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		s.prepareDocumentFind()
		ui.Scroll(c.Key("csv-test-viewport")).TrackScroll(s.readingScrollState("text")).Fill().Children(func() { s.renderCSVPreview(c, p, s.materialsUnit.state.documentContent) })
	}, 500, 180)
	return s, tester
}

func TestCSVClickWithoutRequiredReadingSession(t *testing.T) {
	const source = "name,value\none,1\ntwo,2\n"
	for _, session := range []string{"none", "memory", "closed"} {
		t.Run(session, func(t *testing.T) {
			s, tester := csvClickFixture(t, source, session)
			for i := 0; i < 6; i++ {
				if err := tester.Click("one"); err != nil {
					t.Fatal(err)
				}
				tester.Key(ui.Ctrl, ui.KeyC)
				if tester.Clipboard() != "one" {
					t.Fatal("click must select and focus without frame-bound handles", tester.Clipboard())
				}
				if err := tester.Click("two"); err != nil {
					t.Fatal(err)
				}
				tester.Key(ui.Ctrl, ui.KeyC)
				if tester.Clipboard() != "two" {
					t.Fatal("repeated click lost selection", tester.Clipboard())
				}
			}
			if s.materialsUnit.state.documentDirty {
				t.Fatal("readonly selection changed content")
			}
			data, err := os.ReadFile(s.materialsUnit.state.documentPath)
			if err != nil || string(data) != source {
				t.Fatal("preview changed source file", err)
			}
		})
	}
}

func TestCSVClickAfterScrollUsesGridCoordinates(t *testing.T) {
	var source strings.Builder
	source.WriteString("name,value\n")
	for i := 0; i < 24; i++ {
		source.WriteString(string(rune('a'+i)) + "-row,value\n")
	}
	s, tester := csvClickFixture(t, source.String(), "memory")
	scroll := s.readingScrollState("text")
	scroll.Y = csvPreviewRowHeight * 8
	tester.Frame()
	if err := tester.Click("j-row"); err != nil {
		t.Fatal(err)
	}
	tester.Key(ui.Ctrl, ui.KeyC)
	if tester.Clipboard() != "j-row" {
		t.Fatal("vertical scroll offset was added twice", tester.Clipboard())
	}
}

func TestCSVClickQuotedCellAndDocumentChangeResetSelection(t *testing.T) {
	s, tester := csvClickFixture(t, "name,value\n\"中文,😀\",2\n", "memory")
	if err := tester.Click("中文,😀"); err != nil {
		t.Fatal(err)
	}
	tester.Key(ui.Ctrl, ui.KeyC)
	if tester.Clipboard() != `"中文,😀"` {
		t.Fatal("CSV quoting or unicode copy changed", tester.Clipboard())
	}
	s.materialsUnit.state.documentContent = "name,value\nnew-document,3\n"
	tester.Frame()
	if err := tester.Click("new-document"); err != nil {
		t.Fatal(err)
	}
	tester.Key(ui.Ctrl, ui.KeyC)
	if tester.Clipboard() != "new-document" {
		t.Fatal("old document selection survived replacement", tester.Clipboard())
	}
}

func TestCSVDragSelectionAndHorizontalScrollCopy(t *testing.T) {
	_, tester := csvClickFixture(t, "name,value\none,1\ntwo,2\n", "memory")
	first, ok := tester.Find("one")
	if !ok {
		t.Fatal("first cell missing")
	}
	last, ok := tester.Find("two")
	if !ok {
		t.Fatal("last row missing")
	}
	fromX, fromY := first.X+4, first.Y+first.H/2
	toX, toY := last.X+csvPreviewCellWidth+4, last.Y+last.H/2
	for _, reverse := range []bool{false, true} {
		if reverse {
			tester.Press(toX, toY)
			tester.Move(fromX, fromY)
			tester.Release(fromX, fromY)
		} else {
			tester.Press(fromX, fromY)
			tester.Move(toX, toY)
			tester.Release(toX, toY)
		}
		tester.Key(ui.Ctrl, ui.KeyC)
		if tester.Clipboard() != "one,1\r\ntwo,2" {
			t.Fatal("drag selection must preserve CSV rectangle", tester.Clipboard(), reverse)
		}
	}
	s, wide := csvClickFixture(t, "a,b,c,d\na-one,b-two,c-three,d-four\n", "memory")
	s.readingScrollState("csv-horizontal").X = 140
	wide.Frame()
	if err := wide.Click("d-four"); err != nil {
		t.Fatal(err)
	}
	wide.Key(ui.Ctrl, ui.KeyC)
	if wide.Clipboard() != "d-four" {
		t.Fatal("horizontal scroll changed clicked column", wide.Clipboard())
	}
}

func TestCSVFullMaterialsPreviewClickDoesNotCreateDraft(t *testing.T) {
	const source = "name,value\none,1\ntwo,2\n"
	s, action, result := materialsTextPreviewFixture(t, "entry.csv", []byte(source))
	s.applyMaterialsActionResult(action, result)
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		s.beginOverflowMenuFrame()
		s.materials(c, p)
		s.endOverflowMenuFrame()
	}, 1000, 650)
	if err := tester.Click("one"); err != nil {
		t.Fatal(err)
	}
	tester.Key(ui.Ctrl, ui.KeyC)
	if tester.Clipboard() != "one" || s.materialsUnit.state.documentDirty || s.materialsUnit.state.documentEditing || len(s.materialsUnit.state.documentDrafts) != 0 {
		t.Fatal("CSV preview click mutated draft or lost copy", tester.Clipboard())
	}
}
