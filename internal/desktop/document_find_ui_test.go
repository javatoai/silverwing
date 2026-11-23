package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestDocumentFindShortcutSelectsExistingQuery(t *testing.T) {
	s, action, result := materialsTextPreviewFixture(t, "find.csv", []byte("name,value\n旧查询,1\nneedle,2\nneedle,3\n新查询,4\n"))
	s.applyMaterialsActionResult(action, result)
	materials := &s.materialsUnit.state
	materials.documentSearch = "旧查询"
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		s.beginOverflowMenuFrame()
		s.materials(c, p)
		s.endOverflowMenuFrame()
	}, 980, 650)

	tester.Key(ui.Ctrl, ui.KeyF)
	if !tester.Focused("查找正文") || materials.documentSearch != "旧查询" {
		t.Fatalf("opening the find bar did not focus its existing query: %q", materials.documentSearch)
	}
	tester.Type("needle")
	tester.Key(0, ui.KeyF3)
	f := materials.documentFind
	if f.query != "needle" || f.active != 1 || len(f.hits) != 2 {
		t.Fatalf("typing after opening did not replace the query and find both matches: query=%q active=%d hits=%v", f.query, f.active, f.hits)
	}
	active, _ := f.current()

	// Put the caret at the end to distinguish selecting again from keeping focus.
	tester.Key(0, ui.KeyEnd)
	tester.Key(ui.Ctrl, ui.KeyF)
	if got, ok := f.current(); !ok || got != active || materials.documentSearch != "needle" {
		t.Fatalf("reopening changed the query or active match: query=%q match=%v", materials.documentSearch, got)
	}
	tester.Type("新查询")
	if f.query != "新查询" || len(f.hits) != 1 {
		t.Fatalf("typing after reopening did not replace the query: query=%q hits=%v", f.query, f.hits)
	}
	tester.Key(0, ui.KeyEscape)
	if materials.documentFindOpen || materials.documentSearch != "" || len(f.hits) != 0 {
		t.Fatal("closing the find bar did not clear its query and results")
	}
}

func TestDocumentFindShortcutDoesNotTargetHiddenBarForNativePreview(t *testing.T) {
	s, action, result := materialsTextPreviewFixture(t, "find.txt", []byte("native preview"))
	s.applyMaterialsActionResult(action, result)
	if !s.materialsTextPreviewVisible() || s.currentMaterialsTextPreview() != nil {
		t.Fatal("fixture must have a native preview that has not been bound yet")
	}
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		c.OnShortcut(ui.Ctrl, ui.KeyF, func() { s.openDocumentFind(c) })
		s.prepareDocumentFind()
		s.renderDocumentFindBar(c, p)
	}, 500, 180)

	tester.Key(ui.Ctrl, ui.KeyF)
	if s.materialsUnit.state.documentFindOpen {
		t.Fatal("shortcut on the unbound native preview opened a hidden shared find bar")
	}
}
