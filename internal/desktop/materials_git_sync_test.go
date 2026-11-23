package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

type materialsGitFakeBackend struct {
	count    atomic.Int32
	start    chan workspace.MaterialsGitScope
	release  chan struct{}
	progress func(string)
}

func (*materialsGitFakeBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, errors.New("unexpected snapshot")
}
func (*materialsGitFakeBackend) Dispatch(context.Context, application.Action) (application.ActionResult, error) {
	return application.ActionResult{}, errors.New("unexpected action")
}
func (*materialsGitFakeBackend) MaterialsGitStatus(context.Context, workspace.MaterialsGitScope) (workspace.MaterialsGitSyncStatus, error) {
	return workspace.MaterialsGitSyncStatus{}, nil
}
func (b *materialsGitFakeBackend) SyncMaterialsGit(ctx context.Context, scope workspace.MaterialsGitScope, _ bool, progress func(string)) (workspace.MaterialsGitSyncStatus, error) {
	b.count.Add(1)
	b.progress = progress
	b.start <- scope
	select {
	case <-b.release:
		return workspace.MaterialsGitSyncStatus{LastSuccess: time.Now().UTC().Format(time.RFC3339)}, nil
	case <-ctx.Done():
		return workspace.MaterialsGitSyncStatus{}, ctx.Err()
	}
}
func materialsGitDesktopFixture(t *testing.T) (*app, *materialsGitFakeBackend, chan func()) {
	t.Helper()
	b := &materialsGitFakeBackend{start: make(chan workspace.MaterialsGitScope, 4), release: make(chan struct{}, 4)}
	updates := make(chan func(), 32)
	cfg := domain.DefaultAppConfig()
	root := filepath.Join(t.TempDir(), "requirements")
	sub := "研发"
	cfg.RequirementMaterialsRoot = &root
	cfg.RequirementMaterialsSubdirectory = &sub
	settings := domain.DefaultMaterialsGitSyncSettings()
	settings.RepositoryURL = "https://github.com/example/docs.git"
	cfg.MaterialsGitSync = &settings
	data, _ := json.Marshal(cfg)
	s := newApp(b, Options{})
	s.loading = false
	s.snapshot.Home = t.TempDir()
	s.settingsUnit.state.configJSON = string(data)
	s.taskUnit.state.taskContextUpdate = func(update func()) { updates <- update }
	s.materialsUnit.state.materialsGit = &materialsGitDesktopState{scope: application.MaterialsGitScope(s.snapshot.Home, cfg)}
	t.Cleanup(func() { s.materialsUnit.stopMaterialsGitSync() })
	return s, b, updates
}
func materialsGitNextUpdate(t *testing.T, updates chan func()) {
	t.Helper()
	select {
	case f := <-updates:
		f()
	case <-time.After(3 * time.Second):
		t.Fatal("missing UI result")
	}
}

func TestMaterialsGitDuplicateManualAndTimerShareOneJob(t *testing.T) {
	s, b, updates := materialsGitDesktopFixture(t)
	s.beginMaterialsGitSync(true)
	select {
	case <-b.start:
	case <-time.After(3 * time.Second):
		t.Fatal("not started")
	}
	s.beginMaterialsGitSync(true)
	s.beginMaterialsGitSync(false)
	if b.count.Load() != 1 {
		t.Fatal("duplicate launch")
	}
	b.release <- struct{}{}
	materialsGitNextUpdate(t, updates)
	if s.materialsUnit.state.materialsGit.running || s.materialsUnit.state.materialsGit.status.LastSuccess == "" {
		t.Fatal("did not settle")
	}
	if b.count.Load() != 1 {
		t.Fatal("settlement started another cycle")
	}
}
func TestMaterialsGitDirtyMaterialsDeferWithoutSaving(t *testing.T) {
	s, b, _ := materialsGitDesktopFixture(t)
	s.materialsUnit.state.documentPath = filepath.Join(s.materialsUnit.state.materialsGit.scope.Root, "123-需求", "研发", "note.txt")
	s.materialsUnit.state.documentContent = "unsaved"
	s.materialsUnit.state.documentDirty = true
	s.beginMaterialsGitSync(true)
	if b.count.Load() != 0 || !s.materialsUnit.state.documentDirty || s.materialsUnit.state.documentContent != "unsaved" {
		t.Fatal("draft was synced or lost")
	}
	if s.lastError == "" {
		t.Fatal("missing save feedback")
	}
}

type sharedDocumentsGitFakeBackend struct{ *materialsGitFakeBackend }

func (*sharedDocumentsGitFakeBackend) ProjectInstructionsGitStatus(context.Context, workspace.MaterialsGitScope) (workspace.MaterialsGitSyncStatus, error) {
	return workspace.MaterialsGitSyncStatus{}, nil
}

func (b *sharedDocumentsGitFakeBackend) SyncProjectInstructionsGit(ctx context.Context, scope workspace.MaterialsGitScope, manual bool, progress func(string)) (workspace.MaterialsGitSyncStatus, error) {
	return b.SyncMaterialsGit(ctx, scope, manual, progress)
}

func TestSharedDocumentsGitSyncDefersForPeerDrafts(t *testing.T) {
	s, b, _ := materialsGitDesktopFixture(t)
	s.backend = &sharedDocumentsGitFakeBackend{b}
	s.win = &mygo.Window{}
	config, _ := s.materialsGitConfig()
	root := t.TempDir()
	config.MaterialsGitSync.LocalRoot = root
	config.MaterialsGitSync.RemoteName = "origin"
	config.MaterialsGitSync.BindingID = "11111111111111111111111111111111"
	config.ProjectInstructionsGitLocalRoot = root
	config.ProjectInstructionsGitURL = config.MaterialsGitSync.RepositoryURL
	config.ProjectInstructionsGitBranch = config.MaterialsGitSync.Branch
	config.ProjectInstructionsGitRemoteName = "origin"
	config.ProjectInstructionsGitBindingID = "22222222222222222222222222222222"
	applyMaterialsGitTestConfig(t, s, config)
	materials := s.materialsUnit.state.materialsGit
	materials.scope = application.MaterialsGitScope(s.snapshot.Home, config)
	view := &projectInstructionsView{scope: application.ProjectInstructionsGitScope(s.snapshot.Home, config), ctx: t.Context()}
	s.projectInstructions = view
	view.port = newProjectMaterialsBackend(s.backend, view.scope, view.ctx, nil)
	view.browser = s.newProjectMaterialsBrowser(view)

	projectDraft := filepath.Join(root, domain.ProjectInstructionsDirectory, "guide.md")
	view.browser.materialsUnit.state.documentDrafts[projectDraft] = "unsaved project draft"
	s.beginMaterialsGitSync(true)
	if materials.running || s.lastError == "" || view.browser.materialsUnit.state.documentDrafts[projectDraft] != "unsaved project draft" {
		t.Fatal("task materials sync ignored or changed the project draft")
	}
	delete(view.browser.materialsUnit.state.documentDrafts, projectDraft)

	materialsDraft := filepath.Join(root, domain.RequirementsDirectory, "2026", "task", "研发", "note.md")
	s.materialsUnit.state.documentDrafts[materialsDraft] = "unsaved task draft"
	s.lastError = ""
	s.syncProjectInstructions(view, true)
	if view.syncing || s.lastError == "" || s.materialsUnit.state.documentDrafts[materialsDraft] != "unsaved task draft" {
		t.Fatal("project instructions sync ignored or changed the task materials draft")
	}

	config.ProjectInstructionsGitLocalRoot = t.TempDir()
	applyMaterialsGitTestConfig(t, s, config)
	if s.sharedDocumentsGitPeerBusy(config, projectInstructionsGit) {
		t.Fatal("a task draft blocked an independent project instructions checkout")
	}
}

func TestMaterialsGitUnsavedSettingsCannotSyncPreviousTarget(t *testing.T) {
	s, b, _ := materialsGitDesktopFixture(t)
	*s.input("setting:materialsGitSync:intervalMinutes", "5") = "15"
	s.beginMaterialsGitSync(true)
	s.beginMaterialsGitSync(false)
	if b.count.Load() != 0 || s.materialsUnit.state.materialsGit.running {
		t.Fatal("unsaved interval allowed synchronization")
	}
}

func TestMaterialsGitBranchSettingsPreserveRepositoryBinding(t *testing.T) {
	s, _, _ := materialsGitDesktopFixture(t)
	backend := &settingsAuditCaptureBackend{calls: make(chan application.Action, 1)}
	s.backend = backend
	config, _ := s.materialsGitConfig()
	settings := config.MaterialsGitSettings()
	settings.Branch = "docs/team"
	scope := taskMaterialsGit.scope(s.snapshot.Home, config)
	s.saveDocumentsGitSetting(taskMaterialsGit, scope, settings, "branch")

	select {
	case action := <-backend.calls:
		update, ok := action.Payload["update"].(application.ConfigFieldsUpdate)
		patch := update.ObjectFields["materialsGitSync"]
		if action.Kind != "config.fields.save" || action.Payload["documentsGitScope"] != scope || action.Payload["documentsGitRole"] != taskMaterialsGit.role() ||
			!ok || len(update.Fields) != 0 || len(patch) != 2 ||
			string(patch["branch"]) != `"`+settings.Branch+`"` || string(patch["enabled"]) != "false" {
			t.Fatalf("branch save did not preserve binding fields: %+v", action)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("missing saved settings")
	}
}

func TestMaterialsGitRepositoryURLSpellingKeepsScheduledSync(t *testing.T) {
	for _, tc := range []struct{ name, before, after string }{
		{"add suffix", "https://github.com/example/docs", "https://github.com/example/docs.git"},
		{"remove suffix", "https://github.com/example/docs.git", "https://github.com/example/docs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s, backend, updates := materialsGitDesktopFixture(t)
				config, _ := s.materialsGitConfig()
				config.MaterialsGitSync.RepositoryURL = tc.before
				config.MaterialsGitSync.Enabled = true
				config.MaterialsGitSync.IntervalMinutes = 1
				applyMaterialsGitTestConfig(t, s, config)
				state := s.materialsUnit.state.materialsGit
				state.scope = application.MaterialsGitScope(s.snapshot.Home, config)
				state.intervalMinutes = 1
				s.scheduleMaterialsGit(state, 20*time.Second)
				time.Sleep(20 * time.Second)
				synctest.Wait()
				var oldTick func()
				select {
				case oldTick = <-updates:
				default:
					t.Fatal("previous scheduled sync did not queue")
				}

				config.MaterialsGitSync.RepositoryURL = tc.after
				applyMaterialsGitTestConfig(t, s, config)
				oldTick()
				s.observeMaterialsGitSync()
				materialsGitNextUpdate(t, updates)
				if status, _, _ := s.settingsMaterialsGitStatus(true, palette{}); status != "等待下次同步" {
					t.Fatalf("unexpected sync status after changing URL spelling: %q", status)
				}

				time.Sleep(time.Minute - time.Millisecond)
				synctest.Wait()
				if len(updates) != 0 || backend.count.Load() != 0 {
					t.Fatal("changing the URL spelling triggered sync before the scheduled time")
				}
				time.Sleep(time.Millisecond)
				materialsGitNextUpdate(t, updates)
				select {
				case scope := <-backend.start:
					if scope.URL != tc.after {
						t.Fatalf("scheduled sync used the stale URL %q", scope.URL)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("scheduled sync did not start")
				}
				backend.release <- struct{}{}
				materialsGitNextUpdate(t, updates)
			})
		})
	}
}

func TestMaterialsGitDifferentTargetRequiresResumeAfterURLSpellingEdit(t *testing.T) {
	for _, name := range []string{"repository", "branch", "materials subdirectory"} {
		t.Run(name, func(t *testing.T) {
			s, _, updates := materialsGitDesktopFixture(t)
			config, _ := s.materialsGitConfig()
			config.MaterialsGitSync.Enabled = true
			config.MaterialsGitSync.RepositoryURL = "https://github.com/example/docs"
			switch name {
			case "repository":
				config.MaterialsGitSync.RepositoryURL = "https://github.com/example/other"
			case "branch":
				config.MaterialsGitSync.Branch = "docs/other"
			case "materials subdirectory":
				subdirectory := "文档"
				config.RequirementMaterialsSubdirectory = &subdirectory
			}
			assertResumeRequired := func() {
				t.Helper()
				applyMaterialsGitTestConfig(t, s, config)
				s.observeMaterialsGitSync()
				materialsGitNextUpdate(t, updates)
				if status, _, _ := s.settingsMaterialsGitStatus(true, palette{}); status != "同步目标已变化，请重新检查并恢复" {
					t.Fatalf("unconfirmed target lost the warning: %q", status)
				}
			}
			assertResumeRequired()
			config.MaterialsGitSync.RepositoryURL += ".git"
			assertResumeRequired()
		})
	}
}

func applyMaterialsGitTestConfig(t *testing.T, s *app, config domain.AppConfig) {
	t.Helper()
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	s.settingsUnit.state.configJSON = string(data)
}

func TestMaterialsGitQueuedTimerDoesNotDuplicateManualSync(t *testing.T) {
	s, b, updates := materialsGitDesktopFixture(t)
	config, _ := s.materialsGitConfig()
	config.MaterialsGitSync.Enabled = true
	data, _ := json.Marshal(config)
	s.settingsUnit.state.configJSON = string(data)
	s.materialsUnit.state.materialsGit.scope = application.MaterialsGitScope(s.snapshot.Home, config)
	s.scheduleMaterialsGit(s.materialsUnit.state.materialsGit, 0)
	var queued func()
	select {
	case queued = <-updates:
	case <-time.After(3 * time.Second):
		t.Fatal("timer did not queue")
	}
	s.beginMaterialsGitSync(true)
	select {
	case <-b.start:
	case <-time.After(3 * time.Second):
		t.Fatal("not started")
	}
	queued()
	if b.count.Load() != 1 {
		t.Fatal("queued timer duplicated manual sync")
	}
	b.release <- struct{}{}
	materialsGitNextUpdate(t, updates)
}

func TestMaterialsGitImportedSettingsPreserveNewerInput(t *testing.T) {
	s, _, _ := materialsGitDesktopFixture(t)
	before := s.settingsUnit.state.configJSON
	interval := s.input("setting:materialsGitSync:intervalMinutes", "5")
	config, _ := s.materialsGitConfig()
	config.MaterialsGitSync.IntervalMinutes = 10
	data, _ := json.Marshal(config)
	s.syncSettingsConfigInputs(before, string(data))
	if *interval != "10" {
		t.Fatal("import did not refresh input")
	}
	*interval = "15"
	config.MaterialsGitSync.IntervalMinutes = 20
	newData, _ := json.Marshal(config)
	s.syncSettingsConfigInputs(string(data), string(newData))
	if *interval != "15" {
		t.Fatal("late refresh replaced settings draft")
	}
}
func TestMaterialsGitLateResultAndProgressDoNotUpdateNewTarget(t *testing.T) {
	s, b, updates := materialsGitDesktopFixture(t)
	s.beginMaterialsGitSync(true)
	select {
	case <-b.start:
	case <-time.After(3 * time.Second):
		t.Fatal("not started")
	}
	b.progress("late stage")
	config, _ := s.materialsGitConfig()
	config.MaterialsGitSync.Branch = "docs/other"
	data, _ := json.Marshal(config)
	s.settingsUnit.state.configJSON = string(data)
	materialsGitNextUpdate(t, updates)
	if s.materialsUnit.state.materialsGit.phase == "late stage" {
		t.Fatal("late progress applied")
	}
	b.release <- struct{}{}
	materialsGitNextUpdate(t, updates)
	if s.lastMessage != "" || s.materialsUnit.state.materialsGit.scope.Branch != "docs/other" {
		t.Fatal("late result affected new target")
	}
	materialsGitNextUpdate(t, updates) // read-only status for new binding
	if b.count.Load() != 1 || !s.materialsUnit.state.materialsGit.needsResume {
		t.Fatal("target change auto-started sync")
	}
}
func TestMaterialsGitCloseCancelsWithoutStartingAnotherJob(t *testing.T) {
	s, b, _ := materialsGitDesktopFixture(t)
	s.beginMaterialsGitSync(true)
	select {
	case <-b.start:
	case <-time.After(3 * time.Second):
		t.Fatal("not started")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.materialsUnit.closeMaterialsGitSync(ctx); err != nil {
		t.Fatal(err)
	}
	s.beginMaterialsGitSync(true)
	if b.count.Load() != 1 {
		t.Fatal("launched after closing")
	}
}
func TestMaterialsGitSettingsLayout(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s, _, _ := materialsGitDesktopFixture(t)
				s.materialsUnit.state.materialsGit.status.Paused = true
				s.materialsUnit.state.materialsGit.status.Error = "请使用 Git 工具解决冲突并提交，然后重新检查。"
				tester := ui.NewTester(func(c *ui.Context) {
					p := setTheme(c, dark)
					ui.Column(c).Fill().Padding(24).Gap(8).Background(p.bg).Children(func() {
						settingsCard(c, p, "任务资料目录设置", "", func() { s.settingsMaterialsGitSync(c, p) })
					})
				}, width, 760)
				for _, label := range []string{"Git 自动同步", "Git 仓库地址", "同步分支", "同步周期（分钟）", "打开仓库", "重新检查并恢复"} {
					assertSettingsVisibleBounds(t, tester, label, width, 760)
				}
				captureDesktopTestFrame(t, tester, fmt.Sprintf("materials-git-settings-%d-dark-%t", width, dark))
			})
		}
	}
}

func TestMaterialsGitOpenRepositoryUsesExistingRequirementsCheckout(t *testing.T) {
	root := filepath.Join(t.TempDir(), domain.RequirementsDirectory)
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	config := domain.DefaultAppConfig()
	config.RequirementMaterialsRoot = &root
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	backend := &settingsSystemBackend{actions: make(chan application.Action, 1)}
	s := newApp(backend, Options{})
	s.snapshot.Home = t.TempDir()
	s.settingsUnit.state.configJSON = string(data)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.closeSystemOpenJobs(ctx); err != nil {
			t.Error(err)
		}
	})
	tester := settingsSystemPageTester(s, 960, false, s.settingsMaterialsGitSync)
	if err := tester.Click("打开仓库"); err != nil {
		t.Fatal(err)
	}
	select {
	case action := <-backend.actions:
		if action.Kind != "system.open" || action.Path != root {
			t.Fatalf("opened the wrong synchronization repository: %+v", action)
		}
	case <-time.After(time.Second):
		t.Fatal("opening the synchronization repository did not reach the backend")
	}
}
