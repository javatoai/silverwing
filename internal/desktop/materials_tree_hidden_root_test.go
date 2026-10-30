package desktop

import (
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func TestFileTreeHiddenRootIgnoresOldCollapseAndPreservesInputs(t *testing.T) {
	state := &FileTreeState{rootCollapsed: true}
	expanded := map[string]bool{"研发": true}
	var inputs []FileTreeInput
	props := FileTreeProps{Key: "hidden", HideRoot: true, Root: FileTreeRow{ID: "@root", Name: "123-需求名称", Path: "/root", Directory: true}, State: state,
		Rows:     []FileTreeRow{{ID: "研发", Name: "研发", Path: "/root/研发", Directory: true}, {ID: "研发/说明.md", Name: "说明.md", Path: "/root/研发/说明.md", Depth: 1}, {ID: "Index.md", Name: "Index.md", Path: "/root/Index.md"}},
		Expanded: expanded, OnInput: func(input FileTreeInput) { inputs = append(inputs, input) }}
	tester := ui.NewTester(func(c *ui.Context) { FileTree(c, setTheme(c, false), props) }, 320, 300)
	_, rootVisible := tester.Find(props.Root.Name)
	if rootVisible || !tester.HasText("说明.md") || !tester.HasText("Index.md") {
		t.Fatal("hidden root or old collapse hid the contents")
	}
	row, _ := tester.Find("研发")
	if row.Y >= fileTreeRowHeight {
		t.Fatal("hidden root still reserves its row")
	}
	if err := tester.ClickWith(ui.Ctrl, "说明.md"); err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 1 || inputs[0].Row.Path != "/root/研发/说明.md" || inputs[0].Modifiers != ui.Ctrl {
		t.Fatal("hidden root changed selection identity", inputs)
	}
	tester.Key(0, ui.KeyDown)
	if len(inputs) != 2 || inputs[1].Kind != FileTreeSelect || inputs[1].Row.ID != "Index.md" {
		t.Fatal("keyboard navigation changed when hiding root", inputs)
	}
	if !state.rootCollapsed || !expanded["研发"] {
		t.Fatal("drawing modified host expansion state")
	}
	props.Rows = nil
	props.Empty = "目录为空"
	tester.Frame()
	if !tester.HasText(props.Empty) {
		t.Fatal("empty state hidden by old root collapse")
	}
	props.HideRoot = false
	tester.Frame()
	if !tester.HasText(props.Root.Name) || tester.HasText(props.Empty) {
		t.Fatal("default root behavior changed")
	}
}

func TestMaterialsHiddenRootHeaderLayoutsAndPermissions(t *testing.T) {
	for _, windowWidth := range []int{960, 1440} {
		for _, width := range []float32{180, 320} {
			for _, dark := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d-pane%g-dark%v", windowWidth, width, dark), func(t *testing.T) {
					root := filepath.Join(t.TempDir(), "2026", "123-需求名称")
					s := newApp(&annualMaterialsUIBackend{}, Options{Page: "tasks", Tab: 1})
					s.loading = false
					s.materialsUnit.state.documentsLoaded = true
					s.taskUnit.state.tasks = []task{{id: "annual", path: filepath.Join(t.TempDir(), "task"), materialsPath: root, materialsStatus: "READY"}}
					config := domain.DefaultAppConfig()
					sub := "研发"
					config.RequirementMaterialsSubdirectory = &sub
					encoded, _ := json.Marshal(config)
					s.settingsUnit.state.configJSON = string(encoded)
					for _, name := range []string{sub, "temp", "接口文档-同事资料目录名称很长"} {
						s.materialsUnit.state.documents = append(s.materialsUnit.state.documents, application.Document{Name: name, Path: filepath.Join(root, name), Kind: "directory"})
					}
					s.materialsUnit.state.documents = append(s.materialsUnit.state.documents, application.Document{Name: "Index.md", Path: filepath.Join(root, "Index.md"), Kind: "markdown"})
					state := s.ensureMaterialsFileManagementState()
					state.tree.rootCollapsed = true
					tester := ui.NewTester(func(c *ui.Context) {
						ui.MenuScope(c, "tree-test")
						p := setTheme(c, dark)
						ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
							s.documentTree(c, p, width)
							ui.Column(c).Grow(1).MinWidth(0).Background(p.surface).Padding(24).Children(func() { label(c, "文件预览", 16, p.text) })
						})
						s.actionDialog(c, p)
						ui.ThemedMenus(c, "tree-test")
					}, windowWidth, 700)
					_, rootVisible := tester.Find(filepath.Base(root))
					if rootVisible || !tester.HasText("Index.md") || !tester.HasText("只读") {
						t.Fatal("root flattening changed visible contents or permissions")
					}
					inline := width >= materialsTreeInlineActionsMinWidth
					for _, name := range []string{"新建文档", "新建文件夹", "刷新资料", "收起全部文件夹"} {
						r, ok := tester.Find(name)
						if ok != inline || ok && (r.Y >= 34 || r.X+r.W > width) {
							t.Fatal("header action overflow or wrong adaptive state", name, r)
						}
					}
					if output := os.Getenv("SILVERWING_TREE_CAPTURE_DIR"); output != "" {
						if err := os.MkdirAll(output, 0700); err != nil {
							t.Fatal(err)
						}
						file, err := os.Create(filepath.Join(output, fmt.Sprintf("tree-%d-pane%g-dark%v.png", windowWidth, width, dark)))
						if err != nil {
							t.Fatal(err)
						}
						if err := png.Encode(file, tester.Image()); err != nil {
							t.Fatal(err)
						}
						if err := file.Close(); err != nil {
							t.Fatal(err)
						}
					}
					if err := tester.Click("更多资料操作"); err != nil {
						t.Fatal(err)
					}
					if !tester.HasText("收起全部文件夹") || !tester.HasText("新建文档") {
						t.Fatal("narrow menu lacks migrated actions")
					}
					tester.CloseMenu()
					s.setReadingDirectoryExpanded("研发", true)
					s.collapseMaterialsTreeDirectories()
					if s.readingExpandedDirectories()["研发"] || !state.tree.rootCollapsed {
						t.Fatal("collapse all changed root state or missed child")
					}
					if !s.materialsWritable(filepath.Join(root, "temp", "test.txt"), false) || s.materialsWritable(filepath.Join(root, "接口文档-同事资料目录名称很长", "test.txt"), false) {
						t.Fatal("materials permissions changed")
					}
					if inline {
						if err := tester.Click("temp"); err != nil {
							t.Fatal(err)
						}
						if err := tester.Click("新建文件夹"); err != nil {
							t.Fatal(err)
						}
						if !s.dialogOpen || s.form.path != filepath.Join(root, "temp") {
							t.Fatal("header creation ignored selected directory", s.form.path)
						}
						tester.Key(0, ui.KeyEscape)
						if s.dialogOpen {
							t.Fatal("Esc did not close creation form")
						}
						if err := tester.Click("接口文档-同事资料目录名称很长"); err != nil {
							t.Fatal(err)
						}
						_ = tester.Click("新建文件夹")
						if s.dialogOpen {
							t.Fatal("header allowed creating in a read-only directory")
						}
					}
				})
			}
		}
	}
}
