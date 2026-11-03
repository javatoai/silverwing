package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
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

func TestSettingsAuditMCPExplicitScope(t *testing.T) {
	for _, scenario := range []struct {
		name, scope    string
		backgroundTask bool
	}{
		{"task-from-global", "当前任务", false},
		{"global-from-task", "全局配置", true},
		{"task-from-task", "当前任务", true},
		{"global-from-global", "全局配置", false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			b := &settingsAuditCaptureBackend{calls: make(chan application.Action, 1)}
			s := newApp(b, Options{Page: "settings", CapturePath: "audit-form-probe"})
			s.loading = false
			s.snapshot.Home = t.TempDir()
			s.settingsUnit.state.configJSON = `{}`
			s.settingsUnit.state.settingsPage = "codex-mcp"
			s.settingsUnit.state.settingsMCPTaskScope = scenario.backgroundTask
			taskPath := t.TempDir()
			s.taskUnit.state.tasks = []task{{id: "audit-task", path: taskPath, createdAt: "audit-created"}}
			s.win = &mygo.Window{}
			payload := map[string]any{"name": "audit-service", "transport": "本地命令", "endpoint": "audit-command", "args": "", "env": "{}", "httpHeaders": "{}", "startupTimeoutSec": "10", "toolTimeoutSec": "60", "scope": scenario.scope}
			if !s.settingsActionForm(actionForm{kind: "codex.extensions/mcp/add"}, payload) || s.confirmationDialog == nil {
				t.Fatal("valid form did not prepare confirmation", s.lastError)
			}
			d := s.confirmationDialog
			s.win = nil // Dispatch to a recording backend; never create a native window.
			s.finishConfirmationDialog(d, 0)
			var action application.Action
			select {
			case action = <-b.calls:
			case <-time.After(3 * time.Second):
				t.Fatal("confirmed form did not dispatch isolated action", s.lastError)
			}
			wantCwd, wantFile := "", ""
			if scenario.scope == "当前任务" {
				wantCwd, wantFile = taskPath, filepath.Join(taskPath, ".codex", "config.toml")
			}
			if payloadString(action.Payload, "cwd") != wantCwd || payloadString(action.Payload, "file") != wantFile {
				t.Errorf("explicit scope lost: got cwd=%q file=%q, want cwd=%q file=%q", payloadString(action.Payload, "cwd"), payloadString(action.Payload, "file"), wantCwd, wantFile)
			}
		})
	}
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

func TestSettingsAuditImportUsesPreviewFingerprint(t *testing.T) {
	s := newApp(nil, Options{Page: "settings", CapturePath: "audit-import-probe"})
	s.loading = false
	s.snapshot.Home = t.TempDir()
	s.settingsUnit.state.configJSON = `{}`
	s.settingsUnit.state.settingsPage = "config-backup"
	s.applySettingsConfigPreview(application.ConfigImportPreview{Path: filepath.Join(t.TempDir(), "preview.zip"), Fingerprint: "immutable-preview-digest"})
	action, ok := s.settingsConfigImportAction()
	if !ok || payloadString(action.Payload, "expectedFingerprint") != "immutable-preview-digest" {
		t.Errorf("import confirmation did not carry preview fingerprint: valid=%t payload=%v", ok, action.Payload)
	}
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
