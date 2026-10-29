package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	codeeditor "github.com/egoist/mygo/ui/editor"
)

const materialJSONFoldSource = "{\r\n  \"label\": \"中文😀\",\r\n  \"nested\": {\r\n    \"items\": [\r\n      \"first\",\r\n      \"hidden needle\"\r\n    ]\r\n  },\r\n  \"tail\": \"done🪴\"\r\n}\r\n"

func materialJSONFoldFixture(t *testing.T, source string, width int, dark bool) (*app, *ui.Tester, **nativeCodePreviewState) {
	t.Helper()
	s, _, files := materialTextEditorFixture(t, map[string]string{"review.json": source})
	bindMaterialTextTest(s, files["review.json"])
	s.materialsUnit.state.documentContent = source
	var owner *nativeCodePreviewState
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, dark)
		if s.materialsTextEditingVisible() {
			s.renderMaterialsTextEditor(c, p)
		} else {
			s.prepareDocumentFind()
			owner = s.renderDetectedMaterialsText(c, p)
		}
	}, width, 700)
	tester.SetLocale("zh-Hans")
	tester.Frame()
	return s, tester, &owner
}

func clickMaterialJSONFold(t *testing.T, tester *ui.Tester, owner *nativeCodePreviewState, line int) {
	t.Helper()
	bounds, found := tester.Find(filepath.Base(owner.view.Label))
	if !found {
		t.Fatal("missing native JSON preview")
	}
	top, height, visible := owner.view.LineGeometry(line)
	if !visible {
		t.Fatal("fold target is not visible", line)
	}
	// The native gutter for this short fixture has two line-number digits;
	// the fold hit area is to their right, before the text viewport.
	tester.ClickAt(bounds.X+40, bounds.Y+float32(top-owner.view.ScrollPosition().Y)+min(height, 20)/2)
}

func TestMaterialsJSONNativeFoldPointerClipboardAndSearch(t *testing.T) {
	s, tester, ownerRef := materialJSONFoldFixture(t, materialJSONFoldSource, 960, false)
	owner := *ownerRef
	view := owner.view
	if view.NoFolding || !view.ReadOnly || len(view.Folded()) != 0 {
		t.Fatal("JSON preview did not open fully expanded and read-only")
	}
	version := view.Buffer.Version()
	editState := s.currentMaterialTextEditor()
	if editState == nil {
		t.Fatal("missing independent source editor")
	}
	editVersion := editState.view.Buffer.Version()
	clickMaterialJSONFold(t, tester, owner, 2)
	if !slices.Contains(view.Folded(), 2) {
		t.Fatalf("native gutter did not fold the object: %v", view.Folded())
	}
	if _, _, visible := view.LineGeometry(5); visible {
		t.Fatal("fold did not hide its child")
	}
	tester.Frame()
	if !slices.Contains(view.Folded(), 2) {
		t.Fatal("ordinary render reset the fold")
	}
	clickMaterialJSONFold(t, tester, owner, 2)
	if len(view.Folded()) != 0 {
		t.Fatal("gutter did not unfold the object")
	}
	clickMaterialJSONFold(t, tester, owner, 3)
	if !slices.Contains(view.Folded(), 3) {
		t.Fatal("array fold failed")
	}
	// Copy from a selection spanning hidden content, without expanding it.
	view.Select(0, len(view.Buffer.String()))
	tester.Frame()
	tester.Command("copy")
	if tester.Clipboard() != materialJSONFoldSource {
		t.Fatal("folded copy lost content or CRLF/Unicode")
	}
	view.Run(codeeditor.FoldAll)
	tester.Frame()
	view.Find("hidden needle")
	tester.Frame()
	if view.SelectedText() != "hidden needle" {
		t.Fatal("native find did not select hidden content")
	}
	if _, _, visible := view.LineGeometry(5); !visible {
		t.Fatal("search did not reveal the folded match")
	}
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if view.Buffer.OriginalText() != materialJSONFoldSource || view.Buffer.Version() != version || s.materialsUnit.state.documentContent != materialJSONFoldSource || editState.view.Buffer.Version() != editVersion || s.materialsUnit.state.markdownAutosave.Status(editState.key).Dirty {
		t.Fatal("fold/search changed source or autosave state")
	}
	if raw, err := os.ReadFile(s.materialsUnit.state.documentPath); err != nil || string(raw) != materialJSONFoldSource {
		t.Fatal("folding wrote the file", err)
	}
}

func TestMaterialsJSONNativeFoldMenuKeyboardAndIdentity(t *testing.T) {
	s, tester, ownerRef := materialJSONFoldFixture(t, materialJSONFoldSource, 960, false)
	view := (*ownerRef).view
	for _, label := range []string{"全部折叠", "全部展开"} {
		if err := tester.RightClick("review.json"); err != nil {
			t.Fatal(err)
		}
		if err := tester.ChooseMenuItem("折叠", label); err != nil {
			t.Fatalf("native folding menu: %v; %v", err, tester.Menu())
		}
		if (label == "全部折叠") != (len(view.Folded()) > 0) {
			t.Fatal("menu command did not update folds", label)
		}
	}
	if err := tester.Click("review.json"); err != nil {
		t.Fatal(err)
	}
	tester.Key(ui.Ctrl, ui.KeyK)
	tester.Key(ui.Ctrl, ui.Key0)
	if len(view.Folded()) == 0 {
		t.Fatal("native fold-all chord failed")
	}
	tester.Key(ui.Ctrl, ui.KeyK)
	tester.Key(ui.Ctrl, ui.KeyJ)
	if len(view.Folded()) != 0 {
		t.Fatal("native unfold-all chord failed")
	}
	view.Fold(2)
	tester.Frame()
	oldPath := s.materialsUnit.state.documentPath
	s.materialsUnit.state.documentPath = filepath.Join(filepath.Dir(oldPath), "other.json")
	tester.Frame()
	if (*ownerRef).view == view || len((*ownerRef).view.Folded()) != 0 {
		t.Fatal("new file inherited old folds")
	}
	s.materialsUnit.state.documentPath = oldPath
	tester.Frame()
	if len((*ownerRef).view.Folded()) != 0 {
		t.Fatal("reopened file did not default to expanded")
	}
	(*ownerRef).view.Fold(2)
	tester.Frame()
	s.materialsUnit.state.documentContent = strings.Replace(materialJSONFoldSource, "hidden needle", "remote update", 1)
	tester.Frame()
	if len((*ownerRef).view.Folded()) != 0 || (*ownerRef).view.Buffer.OriginalText() != s.materialsUnit.state.documentContent {
		t.Fatal("source update reused old fold positions")
	}
	s.toggleMaterialsTextEditing()
	tester.Frame()
	if !s.materialsTextEditingVisible() || s.currentMaterialTextEditor().view.NoFolding {
		t.Fatal("source editor folding was changed")
	}
}

func TestMaterialsJSONFoldEmptySingleLineInvalidAndOtherSources(t *testing.T) {
	for _, source := range []string{"", "{}", "[]", `{"a":[1,2],"b":{}}`, "{\n  \"broken\": [\n"} {
		t.Run(fmt.Sprintf("bytes-%d", len(source)), func(t *testing.T) {
			_, tester, ownerRef := materialJSONFoldFixture(t, source, 960, false)
			view := (*ownerRef).view
			if strings.Contains(source, "broken") {
				if !view.NoFolding || !tester.HasText("JSON 解析失败，显示原文。") {
					t.Fatal("invalid JSON lost raw-source fallback")
				}
			} else if source != "" {
				view.Fold(0)
				tester.Frame()
				if len(view.Folded()) != 0 {
					t.Fatal("single-line JSON has a false fold")
				}
			}
			if view.Buffer.OriginalText() != source {
				t.Fatal("preview rewrote original JSON")
			}
		})
	}
	s, tester, ownerRef := materialJSONFoldFixture(t, materialJSONFoldSource, 960, false)
	for _, name := range []string{"review.log", "review.js", "review.txt"} {
		s.materialsUnit.state.documentPath = filepath.Join(filepath.Dir(s.materialsUnit.state.documentPath), name)
		s.materialsUnit.state.documentKind = documentKindForPath(name)
		tester.Frame()
		if !(*ownerRef).view.NoFolding {
			t.Fatal("non-JSON preview gained folding", name)
		}
	}
}

func TestMaterialsJSONFoldResponsiveThemes(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				_, tester, ownerRef := materialJSONFoldFixture(t, materialJSONFoldSource, width, dark)
				view := (*ownerRef).view
				view.Fold(2)
				tester.Frame()
				if _, _, visible := view.LineGeometry(8); !visible {
					t.Fatal("original tail line hidden by sibling fold")
				}
				bounds, ok := tester.Find("review.json")
				if !ok || bounds.X < 0 || bounds.Y < 0 || bounds.X+bounds.W > float32(width)+1 || bounds.Y+bounds.H > 701 {
					t.Fatal("folded editor overflows viewport", bounds)
				}
				captureDesktopTestFrame(t, tester, fmt.Sprintf("materials-json-fold-%d-dark-%t", width, dark))
				view.Run(codeeditor.FoldAll)
				tester.Frame()
				captureDesktopTestFrame(t, tester, fmt.Sprintf("materials-json-fold-all-%d-dark-%t", width, dark))
			})
		}
	}
}
