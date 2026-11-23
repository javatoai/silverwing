package desktop

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func projectMaterialsBrowserFixture(t *testing.T) (*app, *projectInstructionsView) {
	t.Helper()
	s := newApp(nil, Options{Page: "tasks", CapturePath: "project-instructions-browser"})
	s.snapshot.Home = t.TempDir()
	s.desktopPreferences.materialsWidth = 360
	config := domain.DefaultAppConfig()
	config.ProjectInstructionsGitURL = "https://example.invalid/project.git"
	applyMaterialsGitTestConfig(t, s, config)
	ctx, cancel := context.WithCancel(context.Background())
	view := &projectInstructionsView{scope: application.ProjectInstructionsGitScope(s.snapshot.Home, config), ctx: ctx, cancel: cancel}
	view.port = newProjectMaterialsBackend(s.backend, view.scope, ctx, nil)
	view.browser = s.newProjectMaterialsBrowser(view)
	view.browser.materialsUnit.state.documentsLoaded = true
	s.projectInstructions = view
	t.Cleanup(func() {
		view.stop()
		if err := view.drain(); err != nil {
			t.Error(err)
		}
	})
	return s, view
}

func TestProjectInstructionsBrowserUsesMaterialsToolbarAndPreviewWithoutTask(t *testing.T) {
	s, view := projectMaterialsBrowserFixture(t)
	if !s.selectTaskContentTab(taskProjectInstructionsTab) {
		t.Fatal("could not enter project instructions without a task")
	}
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		s.observeProjectMaterials()
		s.taskWorkspaceDetail(c, p)
		s.renderProjectMaterialsLayers(c, p)
	}, 920, 530)
	root, rootVisible := tester.Find(domain.ProjectInstructionsDirectory)
	preview, previewVisible := tester.Find("文档预览")
	if !rootVisible || !previewVisible || root.X+root.W > preview.X || preview.H < 250 {
		t.Fatalf("expected directory tree and preview: root=%+v preview=%+v", root, preview)
	}
	for _, action := range []string{"新建文档", "新建文件夹", "刷新资料", "收起全部文件夹", "更多资料操作"} {
		if _, ok := tester.Find(action); !ok {
			t.Fatalf("shared toolbar did not render %q", action)
		}
	}
	if err := tester.Click("新建文档"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !view.browser.dialogOpen || view.browser.form.kind != "document.create" || s.dialogOpen {
		t.Fatal("the project toolbar did not open the shared project-owned creation form")
	}
}

func TestProjectInstructionsBrowserSelectionIsIndependentOfTaskMaterials(t *testing.T) {
	s, view := projectMaterialsBrowserFixture(t)
	browser := view.browser
	root := browser.taskUnit.currentTask().materialsPath
	guidePath := filepath.Join(root, "guide")
	notePath := filepath.Join(guidePath, "setup.md")
	browser.materialsUnit.replaceDocuments([]application.Document{{Name: "guide", Path: guidePath, Kind: "directory", Children: []application.Document{
		{Name: "setup.md", Path: notePath, Kind: "file"},
	}}})
	taskFile := filepath.Join(t.TempDir(), "task.md")
	s.materialsUnit.state.documentPath = taskFile
	if !s.selectTaskContentTab(taskProjectInstructionsTab) {
		t.Fatal("could not enter project instructions")
	}
	tester := ui.NewTester(func(c *ui.Context) {
		s.observeProjectMaterials()
		s.taskWorkspaceDetail(c, setTheme(c, false))
	}, 920, 530)
	if err := tester.Click("收起全部文件夹"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if tester.HasText("setup.md") {
		t.Fatal("shared collapse action did not close the child folder")
	}
	if err := tester.Click("展开或收起 guide"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if err := tester.Click("setup.md"); err != nil {
		t.Fatal(err)
	}
	if browser.materialsUnit.state.documentPath != notePath || s.materialsUnit.state.documentPath != taskFile {
		t.Fatalf("selection crossed browser owners: project=%q task=%q", browser.materialsUnit.state.documentPath, s.materialsUnit.state.documentPath)
	}
	if !s.selectTaskContentTab(0) || !s.selectTaskContentTab(taskProjectInstructionsTab) {
		t.Fatal("could not switch away and return")
	}
	if browser.materialsUnit.state.documentPath != notePath {
		t.Fatal("project selection was lost when switching tabs")
	}
}
