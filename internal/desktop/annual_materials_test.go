package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

type annualMaterialsUIBackend struct{}

func (*annualMaterialsUIBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, nil
}
func (*annualMaterialsUIBackend) Dispatch(context.Context, application.Action) (application.ActionResult, error) {
	return application.ActionResult{}, fmt.Errorf("UI fixture does not execute file actions")
}

func TestAnnualMaterialsTreePermissionsAndLayouts(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				root := filepath.Join(t.TempDir(), "2026", "123-需求名称较长的年度资料目录")
				s := newApp(&annualMaterialsUIBackend{}, Options{Page: "tasks", Tab: 1})
				s.loading = false
				s.materialsUnit.state.documentsLoaded = true
				s.dark = dark
				s.taskUnit.state.tasks = []task{{id: "annual", path: filepath.Join(t.TempDir(), "task"), materialsPath: root, materialsStatus: "READY"}}
				c := domain.DefaultAppConfig()
				sub := "我的研发交付资料"
				c.RequirementMaterialsSubdirectory = &sub
				encoded, _ := json.Marshal(c)
				s.settingsUnit.state.configJSON = string(encoded)
				for _, name := range []string{sub, "temp", "测试同事的测试用例和验收记录目录"} {
					dir := filepath.Join(root, name)
					s.materialsUnit.state.documents = append(s.materialsUnit.state.documents, application.Document{Name: name, Path: dir, Kind: "directory", Children: []application.Document{{Name: "用例.md", Path: filepath.Join(dir, "用例.md"), Kind: "markdown"}}})
				}
				state := s.ensureMaterialsFileManagementState()
				s.setReadingDirectoryExpanded(sub, true)
				s.setReadingDirectoryExpanded("temp", true)
				s.setReadingDirectoryExpanded("测试同事的测试用例和验收记录目录", true)
				tester := ui.NewTester(func(c *ui.Context) {
					ui.MenuScope(c, "annual-materials")
					p := setTheme(c, dark)
					ui.Row(c).Fill().AlignItems(ui.Stretch).Background(p.bg).Children(func() {
						s.documentTree(c, p, 300)
						ui.Column(c).Grow(1).MinWidth(0).Padding(16).Children(func() {
							label(c, "年度需求资料", 18, p.text)
							label(c, "选择左侧资料，正式资料和 temp 可写；同事资料只读。", 13, p.muted).Wrap()
						})
					})
					ui.ThemedMenus(c, "annual-materials")
				}, width, 760)
				for _, name := range []string{sub, "temp", "测试同事的测试用例和验收记录目录"} {
					r, ok := tester.Find(name)
					if !ok || r.X < 0 || r.X+r.W > 301 {
						t.Fatal("tree overflow", name, r)
					}
				}
				if !tester.HasText("只读") {
					t.Fatal("missing read-only indication")
				}
				if dir, _ := s.materialsCurrentDirectory(state); dir != filepath.Join(root, sub) {
					t.Fatal("wrong default", dir)
				}
				peer := s.materialsUnit.state.documents[2].Path
				s.openMaterialsRenameForm(peer)
				if s.dialogOpen || s.lastError == "" {
					t.Fatal("peer rename opened a form")
				}
				if !s.materialsWritable(filepath.Join(root, "temp", "scratch.txt"), false) || s.materialsWritable(filepath.Join(peer, "case.md"), false) {
					t.Fatal("permissions")
				}
				tester.Key(0, ui.KeyTab)
				tester.Key(0, ui.KeyTab)
				if err := tester.RightClick("测试同事的测试用例和验收记录目录"); err != nil {
					t.Fatal(err)
				}
				tester.Key(0, ui.KeyEscape)
				tester.CloseMenu()
				captureDesktopTestFrame(t, tester, fmt.Sprintf("annual-materials-%d-dark-%t", width, dark))
			})
		}
	}
}
