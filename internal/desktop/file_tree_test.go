package desktop

import (
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

func TestFileTreeNativeRowsAndIndependentInputs(t *testing.T) {
	for _, scale := range []float32{1, 1.5, 2} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("scale=%g/dark=%v", scale, dark), func(t *testing.T) {
				rows := []FileTreeRow{{ID: "src", Name: "src", Path: "/fixture/src", Directory: true}, {ID: "src/a.sql", Name: "a.sql", Path: "/fixture/src/a.sql", Depth: 1}, {ID: "guide.md", Name: "guide.md", Path: "/fixture/guide.md"}}
				expanded, selected := map[string]bool{"src": true}, map[string]bool{"src/a.sql": true}
				state := &FileTreeState{}
				var inputs []FileTreeInput
				props := FileTreeProps{Key: "fixture", Root: FileTreeRow{ID: "@root", Name: "资料", Directory: true}, Rows: rows, Expanded: expanded, Selected: selected, Opened: "guide.md", State: state,
					OnInput: func(input FileTreeInput) { inputs = append(inputs, input) }, ContextMenu: func(_ FileTreeRow, m *ui.Menu) { m.Item("复制文件") }}
				tt := ui.NewTester(func(c *ui.Context) { FileTree(c, setTheme(c, dark), props) }, 248, 300)
				tt.SetScale(scale) // SDK rendering scales, not physical monitor DPI.
				folder, folderOK := tt.Find("src")
				file, fileOK := tt.Find("a.sql")
				if !folderOK || !fileOK || folder.H != 24 || file.H != 24 || file.Y-folder.Y != 24 {
					t.Fatalf("compact row geometry changed: folder=%v file=%v", folder, file)
				}
				if err := tt.ClickWith(ui.Ctrl, "a.sql"); err != nil {
					t.Fatal(err)
				}
				if len(inputs) != 1 || inputs[0].Kind != FileTreeActivate || inputs[0].Modifiers != ui.Ctrl || inputs[0].ClickCount != 1 {
					t.Fatalf("one click must yield one typed gesture: %+v", inputs)
				}
				if err := tt.Click("展开或收起 src"); err != nil {
					t.Fatal(err)
				}
				if len(inputs) != 2 || inputs[1].Kind != FileTreeToggle {
					t.Fatalf("disclosure also activated its row: %+v", inputs)
				}
				if err := tt.RightClick("guide.md"); err != nil {
					t.Fatal(err)
				}
				if len(inputs) != 3 || inputs[2].Kind != FileTreeContext || inputs[2].Row.ID != "guide.md" {
					t.Fatalf("right click activated a file: %+v", inputs)
				}
				if !expanded["src"] || !selected["src/a.sql"] || props.Opened != "guide.md" {
					t.Fatal("drawing or input changed host-owned state")
				}
			})
		}
	}
}

func TestFileTreeRootActionsKeepTheirSlotAndTooltipIsSuppressed(t *testing.T) {
	state := &FileTreeState{}
	props := FileTreeProps{Key: "root-slots", Root: FileTreeRow{ID: "@root", Name: "materials", Directory: true}, State: state,
		Rows: []FileTreeRow{{ID: "guide.md", Name: "guide.md", Path: "/fixture/完整路径/guide.md"}}, RootActionWidth: 44}
	var currentPalette palette
	var context *ui.Context
	props.RootActions = func() { fileTreeIconButton(context, currentPalette, "fixture-refresh", "refresh", "刷新目录") }
	tt := ui.NewTester(func(c *ui.Context) {
		context, currentPalette = c, setTheme(c, false)
		FileTree(c, currentPalette, props)
	}, 248, 300)
	before, _ := tt.Find("materials")
	if tt.HasText("刷新目录") {
		t.Fatal("root actions occupy the idle sidebar")
	}
	file, _ := tt.Find("guide.md")
	tt.Move(file.X+file.W/2, file.Y+file.H/2)
	after, _ := tt.Find("materials")
	if !tt.HasText("刷新目录") || before != after {
		t.Fatalf("revealing actions moved the root name: before=%v after=%v", before, after)
	}
	state.hoverSince = time.Now().Add(-400 * time.Millisecond)
	tt.Frame()
	if tt.HasText(props.Rows[0].Path) {
		t.Fatal("path tooltip appeared before 500ms")
	}
	state.hoverSince = time.Now().Add(-600 * time.Millisecond)
	tt.Frame()
	if !tt.HasText(props.Rows[0].Path) {
		t.Fatal("resting pointer never showed the full path")
	}
	props.SuppressTooltip = true
	tt.Frame()
	if tt.HasText(props.Rows[0].Path) {
		t.Fatal("a popup did not suppress the path tooltip")
	}
}

func TestFileTreeSelectionFadesWhenNativeWindowLosesFocus(t *testing.T) {
	state := &FileTreeState{}
	tt := ui.NewTester(func(c *ui.Context) {
		FileTree(c, setTheme(c, false), FileTreeProps{Key: "focus", Root: FileTreeRow{Name: "root"}, State: state,
			Rows: []FileTreeRow{{ID: "active.sql", Name: "active.sql"}}, Selected: map[string]bool{"active.sql": true}})
	}, 248, 200)
	row, ok := tt.Find("active.sql")
	if !ok {
		t.Fatal("selection row missing")
	}
	x, y := int(row.X+row.W-8), int(row.Y+row.H/2)
	inactive := tt.Image().RGBAAt(x, y)
	if err := tt.Click("active.sql"); err != nil {
		t.Fatal(err)
	}
	active := tt.Image().RGBAAt(x, y)
	if active == inactive {
		t.Fatal("tree keyboard focus did not strengthen the selection")
	}
	tt.SetFocused(false)
	if blurred := tt.Image().RGBAAt(x, y); blurred != inactive {
		t.Fatalf("native window blur kept the active selection: active=%v blurred=%v inactive=%v", active, blurred, inactive)
	}
}

func TestMaterialsFileTreeMultiSelectionContextAndPreviewPin(t *testing.T) {
	root := filepath.Join(t.TempDir(), "materials")
	s := &app{
		taskUnit: taskController{state: taskState{tasks: []task{{id: "tree-task", path: filepath.Dir(root), materialsPath: root, materialsStatus: "READY"}}}},
		materialsUnit: materialsController{state: materialsState{documentsLoaded: true,
			documentDrafts:   map[string]string{},
			documentDirtyMap: map[string]bool{}}},
		page: "tasks",
		tab:  1,
	}
	for _, name := range []string{"a.sql", "b.json", "c.md"} {
		s.materialsUnit.state.documents = append(s.materialsUnit.state.documents, application.Document{Name: name, Path: filepath.Join(root, name), Kind: "file"})
	}
	state := s.ensureMaterialsFileManagementState()
	s.selectMaterialsPath(s.materialsUnit.state.documents[0].Path)
	tt := ui.NewTester(func(c *ui.Context) { s.documentTree(c, setTheme(c, false), 248) }, 248, 500)
	if err := tt.ClickWith(ui.Ctrl, "b.json"); err != nil {
		t.Fatal(err)
	}
	if s.materialsUnit.state.documentPath != s.materialsUnit.state.documents[0].Path || len(state.selected) != 2 {
		t.Fatal("Ctrl selection opened a document or lost the original selection")
	}
	if err := tt.ClickWith(ui.Shift, "c.md"); err != nil {
		t.Fatal(err)
	}
	if s.materialsUnit.state.documentPath != s.materialsUnit.state.documents[0].Path || len(state.selected) != 2 || state.selected["file:b.json"].Path == "" || state.selected["file:c.md"].Path == "" {
		t.Fatal("Shift selection did not follow the visible anchor range")
	}
	selection, anchor := materialsSelectedPaths(state), state.anchor
	if err := tt.RightClick("a.sql"); err != nil {
		t.Fatal(err)
	}
	if s.materialsUnit.state.documentPath != s.materialsUnit.state.documents[0].Path || state.contextPath != s.materialsUnit.state.documents[0].Path || state.anchor != anchor || !reflect.DeepEqual(selection, materialsSelectedPaths(state)) {
		t.Fatal("right-clicking an unselected file changed the reading or multi-selection state")
	}
	entry, _ := s.findMaterialsEntry(s.materialsUnit.state.documents[0].Path, s.materialsUnit.state.documents)
	if contextSelection := s.materialsContextSelection(state, entry); len(contextSelection) != 1 || contextSelection[0].Path != entry.Path {
		t.Fatal("unselected context target inherited another selection's command scope")
	}
	entry, _ = s.findMaterialsEntry(s.materialsUnit.state.documents[1].Path, s.materialsUnit.state.documents)
	if len(s.materialsContextSelection(state, entry)) != 2 {
		t.Fatal("right-clicking a selected item lost its group")
	}
	tt.CloseMenu()
	s.openMaterialsTreeFile(state, s.materialsUnit.state.documents[1].Path, false)
	if state.previewPath != s.materialsUnit.state.documents[1].Path {
		t.Fatal("single activation did not mark a replaceable preview")
	}
	s.openMaterialsTreeFile(state, s.materialsUnit.state.documents[2].Path, false)
	if slices.Contains(s.materialsUnit.state.documentTabs, s.materialsUnit.state.documents[1].Path) || state.previewPath != s.materialsUnit.state.documents[2].Path {
		t.Fatal("a new preview did not replace the previous clean preview")
	}
	s.openMaterialsTreeFile(state, s.materialsUnit.state.documents[2].Path, true)
	s.openMaterialsTreeFile(state, s.materialsUnit.state.documents[1].Path, false)
	if !slices.Contains(s.materialsUnit.state.documentTabs, s.materialsUnit.state.documents[2].Path) {
		t.Fatal("double activation's pinned tab was replaced")
	}
}

func TestFileTreeKeyboardSelectionAndOpenedAncestors(t *testing.T) {
	rows := []FileTreeRow{{ID: "src", Name: "src", Directory: true}, {ID: "src/a.go", Name: "a.go", Depth: 1}, {ID: "src/b.go", Name: "b.go", Depth: 1}, {ID: "notes", Name: "notes", Directory: true}, {ID: "notes/readme.md", Name: "readme.md", Depth: 1}}
	expanded := map[string]bool{"src": true}
	visible := fileTreeVisibleRows(rows, expanded)
	if len(visible) != 4 || !fileTreeOpenedAncestors(rows, "src/b.go")["src"] || fileTreeOpenedAncestors(rows, "src/b.go")["notes"] {
		t.Fatal("visibility or the opened document's guide crossed a parent boundary")
	}
	selected, anchor := fileTreeSelection(visible, nil, "", "src/a.go", 0)
	selected, anchor = fileTreeSelection(visible, selected, anchor, "notes", ui.Shift)
	if len(selected) != 3 || selected["src"] || !selected["src/b.go"] || anchor != "src/a.go" {
		t.Fatal("range selection included collapsed descendants or lost its anchor")
	}
	var inputs []FileTreeInput
	state := &FileTreeState{}
	tt := ui.NewTester(func(c *ui.Context) {
		FileTree(c, setTheme(c, false), FileTreeProps{Key: "keys", Root: FileTreeRow{Name: "root"}, Rows: rows, Expanded: expanded, State: state, OnInput: func(input FileTreeInput) { inputs = append(inputs, input) }})
	}, 248, 300)
	if err := tt.Click("a.go"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyDown)
	if len(inputs) != 2 || inputs[1].Kind != FileTreeSelect || inputs[1].Row.ID != "src/b.go" {
		t.Fatalf("Down must select the next visible row without activating it: %+v", inputs)
	}
}

func TestSkillsFileTreeUsesSharedRowsAndOnlyCopyCapability(t *testing.T) {
	s := &app{}
	state := &skillsBrowserState{selectedDirectory: "fixture", selectedPath: "SKILL.md", expanded: map[string]bool{}, files: []map[string]any{{"relativePath": "SKILL.md", "markdown": true}, {"relativePath": "scripts/run.sql"}, {"relativePath": "assets/unknown.bin"}}}
	s.settingsUnit.state.skillsBrowser = state
	state.app = s
	tt := ui.NewTester(func(c *ui.Context) { s.renderSkillsBrowserDirectory(c, setTheme(c, false), state) }, 248, 500)
	sql, ok := tt.Find("run.sql")
	if !ok || sql.H != 24 || !state.expanded["scripts"] {
		t.Fatal("Skills did not reuse the compact shared tree and prior default expansion")
	}
	if err := tt.ClickWith(ui.Ctrl, "run.sql"); err != nil {
		t.Fatal(err)
	}
	if state.selectedPath != "SKILL.md" || len(state.treeSelected) != 2 {
		t.Fatal("Skills modifier selection also changed its document")
	}
	if err := tt.RightClick("run.sql"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tt.Menu(), []string{"复制文件"}) {
		t.Fatalf("Skills exposed an unsupported write capability: %v", tt.Menu())
	}
	for name, want := range map[string]string{"a.md": "file-markdown", "a.txt": "file", "a.json": "file-json", "a.sql": "file-sql", "a.go": "file-code", "a.png": "file-image", "a.pdf": "file-pdf", "a.bin": "file-unknown"} {
		icon, _ := fileTreeIcon(FileTreeRow{Name: name}, false, colors(false))
		if icon != want || icons[icon] == nil {
			t.Fatalf("missing type icon for %s: %s", name, icon)
		}
	}
}
