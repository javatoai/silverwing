package desktop

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/editor"
	"silverwing-go/internal/workspace"
)

func TestSettingsDocumentsTabsKeepRolesSeparate(t *testing.T) {
	s := newApp(nil, Options{Page: "settings", CapturePath: "settings-documents-test"})
	s.loading = false
	s.snapshot.Home = t.TempDir()
	s.settingsUnit.state.configJSON = `{}`
	if !s.navigateSettingsPage("materials-sync", "") || s.settingsUnit.state.settingsPage != "paths" {
		t.Fatal("legacy sync link did not open task materials")
	}
	v := ui.NewTester(func(c *ui.Context) { s.settingsScreen(c, setTheme(c, false)) }, 1100, 820)
	for _, title := range []string{"任务资料", "项目资料", "全局说明"} {
		if tab, ok := v.AccessByRole(ui.RoleTab, title); !ok || title == "任务资料" && !tab.Selected {
			t.Fatalf("missing accessible documents tab %q: %+v", title, tab)
		}
	}
	for _, title := range []string{"本地仓库目录", "Git 仓库地址", "任务角色名", "默认展开目录层级"} {
		if _, ok := v.Find(title); !ok {
			t.Fatalf("task materials is missing %q", title)
		}
	}
	if err := v.Click("任务资料"); err != nil {
		t.Fatal(err)
	}
	v.Key(0, ui.KeyRight)
	if s.settingsUnit.state.settingsPage != "project-instructions" || v.HasText("任务角色名") || !v.HasText("本地仓库目录") {
		t.Fatal("right arrow did not switch from task materials to project materials")
	}
	for _, query := range []string{"项目资料", "project-materials"} {
		matches := settingsSearch(query)
		if len(matches) != 1 || matches[0].page != "project-instructions" || matches[0].title != "项目资料 Git 自动同步" {
			t.Fatalf("search for %q did not find project materials: %v", query, matches)
		}
	}
	if err := v.Click("全局说明"); err != nil {
		t.Fatal(err)
	}
	if s.settingsUnit.state.settingsPage != "agents" || v.HasText("本地仓库目录") {
		t.Fatal("global instructions received repository controls")
	}
	if _, ok := v.Find("更多文档操作"); !ok {
		t.Fatal("global instructions lost its editor actions")
	}
}

func TestDocumentsGitLocationAlignsDirectoryAndActions(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		target documentsGitTarget
		width  int
		bound  bool
	}{
		{"task materials", taskMaterialsGit, 1314, false},
		{"project instructions", projectInstructionsGit, 1100, true},
		{"narrow project instructions", projectInstructionsGit, 820, true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			s := newApp(nil, Options{})
			path := `C:\测试资料\本地仓库`
			if fixture.width < settingsAppearanceStackBreakpoint {
				path += `\` + strings.Repeat("项目资料", 16)
			}
			scope := workspace.MaterialsGitScope{Root: path}
			v := settingsSystemPageTester(s, fixture.width, false, func(c *ui.Context, p palette) {
				s.settingsDocumentsGitLocation(c, p, fixture.target, scope, fixture.bound, false)
			})
			assertSettingsVisibleBounds(t, v, path, fixture.width, 720)
			directory, _ := v.Find(path)
			center := directory.Y + directory.H/2
			actions := []string{"选择仓库"}
			if fixture.bound {
				actions = []string{"更换目录", "解除关联"}
			}
			right := directory.X + directory.W
			for _, name := range actions {
				assertSettingsVisibleBounds(t, v, name, fixture.width, 720)
				button, _ := v.Find(name)
				if offset := button.Y + button.H/2 - center; offset < -1 || offset > 1 || button.X < right {
					t.Fatalf("directory and %s must share one row: directory=%+v button=%+v", name, directory, button)
				}
				right = button.X + button.W
			}
			if fixture.width >= settingsAppearanceStackBreakpoint {
				label, _ := v.Find("本地仓库目录")
				if offset := label.Y + label.H/2 - center; offset < -1 || offset > 1 {
					t.Fatalf("directory label must share the row: label=%+v directory=%+v", label, directory)
				}
			}
		})
	}
}

func TestDocumentsGitBindingLeavesDraftInOriginalRepositoryCache(t *testing.T) {
	root := t.TempDir()
	key := filepath.Join(root, "document.md")
	unrelated := filepath.Join(t.TempDir(), "unrelated.md")
	drafts := map[string]string{}
	engine := editor.NewAutosave(nil, nil)
	engine.SetDebounce(time.Hour)
	engine.SetDraftWriter(func(_ context.Context, path, content string) error {
		if path == unrelated {
			return errors.New("unrelated repository cache failed")
		}
		drafts[path] = content
		return nil
	}, nil)
	engine.Seed(key, "disk")
	engine.Edit(key, "draft")
	engine.Edit(unrelated, "other draft")
	plan := &documentsGitDraftPlan{engine: engine, keys: []string{key}, scope: workspace.MaterialsGitScope{Root: root, BindingID: "old"}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	defer engine.Close(ctx)
	lease, err := plan.prepare(ctx)
	if err != nil || drafts[key] != "draft" {
		t.Fatalf("old draft was not cached before dispatch: %v", err)
	}
	result := application.ActionResult{Data: domain.MaterialsGitSyncSettings{LocalRoot: t.TempDir(), BindingID: "new"}}
	if err := plan.finish(lease, "config.documents-git.bind", result, nil); err != nil {
		t.Fatal(err)
	}
	if _, exists := engine.Content(key); exists || drafts[key] != "draft" {
		t.Fatal("switching repository did not retire the old editor and retain its cached draft")
	}
	if !engine.Status(unrelated).Dirty {
		t.Fatal("switching repository changed a draft in another repository")
	}
}

func TestProjectInstructionsRepositorySelectionUsesCapturedScope(t *testing.T) {
	backend := &settingsAuditCaptureBackend{calls: make(chan application.Action, 1)}
	s := newApp(backend, Options{})
	s.snapshot.Home = t.TempDir()
	config := domain.DefaultAppConfig()
	applyMaterialsGitTestConfig(t, s, config)
	scope := projectInstructionsGit.scope(s.snapshot.Home, config)

	s.snapshot.Home = t.TempDir()
	s.changeDocumentsGitBinding(projectInstructionsGit, scope, t.TempDir())
	if s.lastError == "" || s.busy {
		t.Fatal("an old directory picker could change the new profile")
	}
	scope = projectInstructionsGit.scope(s.snapshot.Home, config)
	directory := t.TempDir()
	s.changeDocumentsGitBinding(projectInstructionsGit, scope, directory)
	select {
	case action := <-backend.calls:
		if action.Kind != "config.documents-git.bind" || action.Path != directory ||
			action.Payload["role"] != domain.ProjectInstructionsRepositoryRole || action.Payload["scope"] != scope {
			t.Fatalf("repository selection did not retain its role and scope: %+v", action)
		}
	case <-time.After(time.Second):
		t.Fatal("missing bind action")
	}
}

func TestProjectInstructionsGitSettingsLayoutAndRepository(t *testing.T) {
	for _, width := range []int{960, 1440} {
		backend := &settingsSystemBackend{actions: make(chan application.Action, 1)}
		s := newApp(backend, Options{})
		s.snapshot.Home = t.TempDir()
		config := domain.DefaultAppConfig()
		config.ProjectInstructionsGitURL = "https://example.invalid/instructions.git"
		applyMaterialsGitTestConfig(t, s, config)
		scope := application.ProjectInstructionsGitScope(s.snapshot.Home, config)
		s.projectInstructions = &projectInstructionsView{scope: scope, ctx: context.Background()}
		s.materialsUnit.state.projectGitBranches = &materialsGitBranchesState{scope: scope, loaded: true, branches: []string{"main", "docs"}}
		tester := settingsSystemPageTester(s, width, false, s.settingsProjectInstructions)
		for _, label := range []string{"Git 自动同步", "Git 仓库地址", "同步分支", "同步周期（分钟）", "打开仓库", "立即同步"} {
			assertSettingsVisibleBounds(t, tester, label, width, 720)
		}
		if !tester.HasText(scope.URL) || len(s.inputs) != 1 || s.inputs[projectInstructionsGit.inputKey("intervalMinutes")] == nil {
			t.Fatal("only the sync interval should be an editable text value")
		}
		open, _ := tester.Find("打开仓库")
		sync, _ := tester.Find("立即同步")
		if open.X < float32(width)/2 || open.X >= sync.X || open.Y != sync.Y || sync.W > 150 {
			t.Fatalf("sync actions must be compact and right-aligned: open=%+v sync=%+v", open, sync)
		}
		if err := tester.Click("打开仓库"); err != nil {
			t.Fatal(err)
		}
		select {
		case action := <-backend.actions:
			if action.Kind != "system.open" || action.Path != scope.Root {
				t.Fatalf("opened the wrong repository: %+v", action)
			}
		case <-time.After(time.Second):
			t.Fatal("project repository action was not dispatched")
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := s.closeSystemOpenJobs(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestProjectInstructionsGitBranchResultsHaveIndependentScope(t *testing.T) {
	s, backend, updates := materialsBranchesFixture(t)
	config, _ := s.materialsGitConfig()
	config.ProjectInstructionsGitURL = config.MaterialsGitSettings().RepositoryURL
	applyMaterialsGitTestConfig(t, s, config)
	s.loadMaterialsGitBranches(false)
	materials := nextMaterialsBranchCall(t, backend)
	s.loadDocumentsGitBranches(projectInstructionsGit, false)
	project := nextMaterialsBranchCall(t, backend)
	if project.scope.RepositoryRole != domain.ProjectInstructionsRepositoryRole || project.scope.Root == materials.scope.Root {
		t.Fatalf("project branches were requested for the task repository: %+v", project.scope)
	}
	config.ProjectInstructionsGitBranch = "project/docs"
	applyMaterialsGitTestConfig(t, s, config)
	s.loadDocumentsGitBranches(projectInstructionsGit, false)
	current := nextMaterialsBranchCall(t, backend)
	current.reply <- materialsBranchReply{branches: []string{"project/docs", "main"}}
	materialsGitNextUpdate(t, updates)
	project.reply <- materialsBranchReply{branches: []string{"old"}}
	materialsGitNextUpdate(t, updates)
	materials.reply <- materialsBranchReply{branches: []string{"master"}}
	materialsGitNextUpdate(t, updates)
	if !s.materialsGitBranchReady("master") || !s.documentsGitBranchReady(projectInstructionsGit, config.ProjectInstructionsGitSettings()) {
		t.Fatal("a result from another repository or superseded branch changed the selection")
	}
}

func TestProjectInstructionsGitLocalBranchChoicesAreExplicit(t *testing.T) {
	for _, name := range []string{"managed", "local", "shared managed"} {
		t.Run(name, func(t *testing.T) {
			s := newApp(nil, Options{})
			s.snapshot.Home = t.TempDir()
			config := domain.DefaultAppConfig()
			config.ProjectInstructionsGitURL = "https://example.invalid/project.git"
			config.ProjectInstructionsGitBranch = "master"
			switch name {
			case "local":
				config.ProjectInstructionsGitLocalRoot = t.TempDir()
				config.ProjectInstructionsGitRemoteName = "origin"
				config.ProjectInstructionsGitBindingID = "22222222222222222222222222222222"
			case "shared managed":
				materials := domain.DefaultMaterialsGitSyncSettings()
				materials.LocalRoot = application.ProjectInstructionsGitScope(s.snapshot.Home, config).Root
				materials.RepositoryURL = config.ProjectInstructionsGitURL
				materials.RemoteName = "origin"
				materials.BindingID = "11111111111111111111111111111111"
				config.MaterialsGitSync = &materials
			}
			applyMaterialsGitTestConfig(t, s, config)
			scope := application.ProjectInstructionsGitScope(s.snapshot.Home, config)
			s.materialsUnit.state.projectGitBranches = &materialsGitBranchesState{scope: scope, loaded: true, branches: []string{"master", "docs"}}
			tester := settingsSystemPageTester(s, 960, false, func(c *ui.Context, p palette) {
				s.settingsDocumentsGitBranch(c, p, projectInstructionsGit, config.ProjectInstructionsGitSettings())
			})
			if err := tester.Click("选择同步分支"); err != nil {
				t.Fatal(err)
			}
			if !tester.HasText("docs") || tester.HasText(documentsGitDefaultBranch) != (name == "managed") {
				t.Fatalf("incorrect branch choices: %v", tester.Texts())
			}
		})
	}
}

func TestProjectInstructionsGitDisableInvalidatesTimerWithoutReplacingEditor(t *testing.T) {
	s := newApp(nil, Options{})
	s.snapshot.Home = t.TempDir()
	config := domain.DefaultAppConfig()
	config.ProjectInstructionsGitURL = "https://example.invalid/instructions.git"
	applyMaterialsGitTestConfig(t, s, config)
	view := &projectInstructionsView{
		scope: application.ProjectInstructionsGitScope(s.snapshot.Home, config),
		ctx:   context.Background(), syncSettings: config.ProjectInstructionsGitSettings(),
		timer: time.NewTimer(time.Hour),
	}
	view.port = newProjectMaterialsBackend(nil, view.scope, view.ctx, nil)
	view.browser = s.newProjectMaterialsBrowser(view)
	view.browser.materialsUnit.state.documentPath = "notes.md"
	s.projectInstructions = view
	defer view.stopSyncTimer()

	enabled := false
	config.ProjectInstructionsGitEnabled = &enabled
	applyMaterialsGitTestConfig(t, s, config)
	s.updateProjectInstructionsSchedule(view, config.ProjectInstructionsGitSettings())
	if view.timer != nil || view.timerGeneration == 0 || view.syncSettings.Enabled {
		t.Fatal("disabling project sync left the scheduled job active")
	}
	if !s.projectInstructionsCurrent(view) || view.browser.materialsUnit.state.documentPath != "notes.md" {
		t.Fatal("the sync preference invalidated the open document")
	}
}

func TestProjectInstructionsGitPendingUsesTheSavedIntervalValue(t *testing.T) {
	s := newApp(nil, Options{})
	config := domain.DefaultAppConfig()
	input := s.input(projectInstructionsGit.inputKey("intervalMinutes"), "05")
	if s.documentsGitSettingsPending(projectInstructionsGit, config) {
		t.Fatal("a different presentation of the saved interval blocked synchronization")
	}
	*input = "10"
	if !s.documentsGitSettingsPending(projectInstructionsGit, config) {
		t.Fatal("an unsaved interval did not block synchronization")
	}
}
