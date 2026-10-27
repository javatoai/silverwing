package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

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
	s.configJSON = string(data)
	s.taskContextUpdate = func(update func()) { updates <- update }
	s.materialsGit = &materialsGitDesktopState{scope: application.MaterialsGitScope(s.snapshot.Home, cfg)}
	t.Cleanup(func() { s.stopMaterialsGitSync() })
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
	if s.materialsGit.running || s.materialsGit.status.LastSuccess == "" {
		t.Fatal("did not settle")
	}
	if b.count.Load() != 1 {
		t.Fatal("settlement started another cycle")
	}
}
func TestMaterialsGitDirtyMaterialsDeferWithoutSaving(t *testing.T) {
	s, b, _ := materialsGitDesktopFixture(t)
	s.documentPath = filepath.Join(s.materialsGit.scope.Root, "123-需求", "研发", "note.txt")
	s.documentContent = "unsaved"
	s.documentDirty = true
	s.beginMaterialsGitSync(true)
	if b.count.Load() != 0 || !s.documentDirty || s.documentContent != "unsaved" {
		t.Fatal("draft was synced or lost")
	}
	if s.lastError == "" {
		t.Fatal("missing save feedback")
	}
}

func TestMaterialsGitUnsavedSettingsCannotSyncPreviousTarget(t *testing.T) {
	s, b, _ := materialsGitDesktopFixture(t)
	*s.input("setting:materialsGitSync:repositoryUrl", s.materialsGit.scope.URL) = "https://example.invalid/new-repo.git"
	s.beginMaterialsGitSync(true)
	s.beginMaterialsGitSync(false)
	if b.count.Load() != 0 || s.materialsGit.running {
		t.Fatal("unsaved URL started the previous target")
	}
}

func TestMaterialsGitQueuedTimerDoesNotDuplicateManualSync(t *testing.T) {
	s, b, updates := materialsGitDesktopFixture(t)
	config, _ := s.materialsGitConfig()
	config.MaterialsGitSync.Enabled = true
	data, _ := json.Marshal(config)
	s.configJSON = string(data)
	s.materialsGit.scope = application.MaterialsGitScope(s.snapshot.Home, config)
	s.scheduleMaterialsGit(s.materialsGit, 0)
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
	before := s.configJSON
	repositoryURL := s.input("setting:materialsGitSync:repositoryUrl", "https://github.com/example/docs.git")
	config, _ := s.materialsGitConfig()
	config.MaterialsGitSync.RepositoryURL = "https://github.com/imported/docs.git"
	data, _ := json.Marshal(config)
	s.syncSettingsConfigInputs(before, string(data))
	if *repositoryURL != "https://github.com/imported/docs.git" {
		t.Fatal("import did not refresh input")
	}
	*repositoryURL = "https://github.com/my-draft/docs.git"
	config.MaterialsGitSync.RepositoryURL = "https://github.com/later/docs.git"
	newData, _ := json.Marshal(config)
	s.syncSettingsConfigInputs(string(data), string(newData))
	if *repositoryURL != "https://github.com/my-draft/docs.git" {
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
	s.configJSON = string(data)
	materialsGitNextUpdate(t, updates)
	if s.materialsGit.phase == "late stage" {
		t.Fatal("late progress applied")
	}
	b.release <- struct{}{}
	materialsGitNextUpdate(t, updates)
	if s.lastMessage != "" || s.materialsGit.scope.Branch != "docs/other" {
		t.Fatal("late result affected new target")
	}
	materialsGitNextUpdate(t, updates) // read-only status for new binding
	if b.count.Load() != 1 || !s.materialsGit.needsResume {
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
	if err := s.closeMaterialsGitSync(ctx); err != nil {
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
				s.materialsGit.status.Paused = true
				s.materialsGit.status.Error = "请使用 Git 工具解决冲突并提交，然后重新检查。"
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
