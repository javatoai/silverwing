package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

type settingsAuditCaptureBackend struct{ calls chan application.Action }

func (b *settingsAuditCaptureBackend) Dispatch(_ context.Context, a application.Action) (application.ActionResult, error) {
	b.calls <- a
	return application.ActionResult{}, errors.New("isolated audit: no external mutation")
}
func (*settingsAuditCaptureBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, nil
}

func TestSettingsAuditMeeglePickerPreservesNewerInput(t *testing.T) {
	s, b := newMeeglePopoverTestApp(t)
	v := ui.NewTester(func(c *ui.Context) { s.settingsMeegleProjects(c, setTheme(c, false)) }, 900, 650)
	state := s.settingsUnit.state.meegleProjectCatalog
	state.pickerOpen = true
	s.submitMeegleProjectPickerSelection(state, state.projects[1])
	action := waitMeeglePopoverSave(t, b)
	name := s.input("setting:meegle-project-name:0", "")
	*name = "typed-after-submit"
	v.Frame()
	s.busy = false
	s.finishMeegleProjectPickerSave(action, application.ActionResult{}, nil, nil)
	entry := s.settingsUnit.state.settingsAutosave.entries["settings:meegle-projects"]
	if *name != "typed-after-submit" || entry == nil || !entry.pending {
		t.Errorf("successful add erased newer input/queue: name=%q entry=%+v", *name, entry)
	}
	if !s.meegleProjectKeyAlreadyConfigured("alpha-key") {
		t.Error("successful add was lost while retaining drafts")
	}
	if entry != nil && entry.latest != nil {
		var queued []map[string]any
		if err := json.Unmarshal([]byte(entry.latest()), &queued); err != nil || len(queued) != 2 || mapString(queued[0], "simple_name") != "typed-after-submit" || mapString(queued[1], "project_key") != "alpha-key" {
			t.Fatalf("next autosave would lose the new project or newer draft: %v %v", queued, err)
		}
	}
}

func TestSettingsConfigBackupPageOnlyOffersManualActions(t *testing.T) {
	s := newApp(nil, Options{Page: "settings", CapturePath: "manual-config-backup-probe"})
	s.loading = false
	s.snapshot.Home = t.TempDir()
	s.settingsUnit.state.configJSON = `{}`
	s.navigateSettingsPage("config-backup", "")
	s.ensureSettingsPageData()
	if s.settingsUnit.state.settingsConfigRecovery != nil {
		t.Fatal("entering the settings page started a backup operation")
	}
	for _, width := range []int{480, 1200} {
		tester := settingsSystemPageTester(s, width, false, s.settingsConfigBackup)
		for _, name := range []string{"手动备份", "手动恢复"} {
			assertSettingsVisibleBounds(t, tester, name, width, 720)
		}
		for _, name := range []string{"查看配置", "导出配置", "导入配置", "读取备份", "备份目录", "删除所选"} {
			if tester.HasText(name) {
				t.Fatalf("obsolete backup control is still visible: %s", name)
			}
		}
	}
}

func TestSettingsAuditImportUsesPreviewFingerprint(t *testing.T) {
	s := newApp(nil, Options{Page: "settings", CapturePath: "audit-import-probe"})
	s.loading = false
	s.snapshot.Home = t.TempDir()
	s.settingsUnit.state.configJSON = `{}`
	s.settingsUnit.state.settingsPage = "config-backup"
	archive := filepath.Join(t.TempDir(), "preview.zip")
	preview := application.ConfigImportPreview{Path: archive, Fingerprint: "immutable-preview-digest", Changes: []string{"模拟配置变更"}}
	s.applySettingsConfigPreview(preview)
	tester := ui.NewTester(func(c *ui.Context) { s.settingsImportDialog(c, setTheme(c, false)) }, 800, 740)
	for _, text := range []string{"配置恢复预览", "模拟配置变更", archive, "确认恢复"} {
		if !tester.HasText(text) {
			t.Fatalf("missing restore preview content %q", text)
		}
	}
	action, ok := s.settingsConfigImportAction()
	if !ok || action.Kind != "config.import" || action.Path != archive || payloadString(action.Payload, "expectedFingerprint") != "immutable-preview-digest" {
		t.Errorf("import confirmation did not carry preview fingerprint: valid=%t payload=%v", ok, action.Payload)
	}
	if err := tester.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.settingsConfigImportAction(); ok {
		t.Fatal("canceling the preview did not retire the restore action")
	}
	s.applySettingsConfigPreview(preview)
	s.settingsUnit.state.settingsConfigRecovery.preview.Fingerprint = ""
	if _, ok := s.settingsConfigImportAction(); ok {
		t.Fatal("preview without a fingerprint enabled import")
	}
}

func TestSettingsAuditInformationCopySearch(t *testing.T) {
	for _, result := range settingsSearch("信息复制") {
		if result.page == "task-area" && result.title == "需求链接和需求编号复制" {
			return
		}
	}
	t.Fatal("the visible information-copy card does not find its actual control")
}

func TestSettingsAuditFixedNamesRevealRealControls(t *testing.T) {
	for _, title := range []string{"IntelliJ IDEA", "WebStorm", "PyCharm", "Visual Studio Code", "Android Studio", "DevEco Studio", "不通过代理的地址", "代理目标"} {
		t.Run(title, func(t *testing.T) {
			var chosen settingsSearchEntry
			for _, entry := range settingsSearch(title) {
				if entry.title == title {
					chosen = entry
					break
				}
			}
			if chosen.page == "" {
				t.Fatal("visible setting name does not resolve to its own search target")
			}
			s := newApp(nil, Options{Page: "settings", CapturePath: "audit-search-probe"})
			s.loading = false
			s.snapshot.Home = t.TempDir()
			s.settingsUnit.state.configJSON = `{}`
			s.openSettingsSearchResult(chosen)
			v := ui.NewTester(func(c *ui.Context) { s.settingsScreen(c, setTheme(c, false)) }, 1200, 850)
			for range 8 {
				v.Frame()
			}
			bounds, found := v.Find(title)
			if s.settingsUnit.state.settingsSearchPending || !found || bounds.H <= 0 || bounds.Y < 0 || bounds.Y+bounds.H > 850 {
				t.Errorf("actual control was not revealed: pending=%t found=%t bounds=%+v", s.settingsUnit.state.settingsSearchPending, found, bounds)
			}
		})
	}
	for _, query := range []string{"协议", "代理主机", "端口"} {
		t.Run(query, func(t *testing.T) {
			for _, entry := range settingsSearch(query) {
				if entry.page == "proxy" && entry.title == "网络代理" {
					return
				}
			}
			t.Fatal("proxy's visible field name does not find its card")
		})
	}
	for _, query := range []string{"Git 工具组", "路径工具组"} {
		t.Run(query, func(t *testing.T) {
			found := make(map[string]bool)
			for _, entry := range settingsSearch(query) {
				if entry.page == "task-area" {
					found[entry.title] = true
				}
			}
			if !found["任务详情工具栏"] || !found["工作区卡片工具栏"] {
				t.Fatalf("duplicate group labels did not find both owning cards: %v", found)
			}
		})
	}
}
