package desktop

import (
	"encoding/json"
	"fmt"
	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/domain"
	"strings"
	"testing"
)

func TestQANavigationCannotRestoreDeveloperPages(t *testing.T) {
	config := domain.DefaultAppConfig()
	raw, _ := json.Marshal(config)
	s := newApp(nil, Options{Page: "tasks", CapturePath: "qa-test"})
	s.loading = false
	s.configJSON = string(raw)
	allowed := map[string]bool{"tasks": true, "requirements": true, "tools": true, "settings": true}
	for _, item := range s.sidebarNavigationItems() {
		if !allowed[item.page] {
			t.Fatalf("retired navigation: %s", item.page)
		}
		delete(allowed, item.page)
	}
	if len(allowed) != 0 {
		t.Fatalf("retained navigation missing: %v", allowed)
	}
	for _, page := range []string{"production", "services", "repositories", "tags"} {
		s.page = page
		s.reconcileSidebarNavigation()
		if s.page != "tasks" {
			t.Fatalf("restored developer page %s", page)
		}
		if validPage(page) {
			t.Fatalf("CLI accepts retired page %s", page)
		}
	}
	forbidden := map[string]bool{"dev-tools": true, "task-area": true, "git": true, "branch-naming": true, "tag": true, "cli-genbu": true}
	for _, section := range settingsSections {
		for _, page := range section.pages {
			if forbidden[page.key] {
				t.Fatalf("retired settings page: %s", page.key)
			}
		}
	}
	s.settingsTab, s.settingsPage = "开发工具", "dev-tools"
	s.reconcileQASettingsNavigation()
	if s.settingsPage != "basic" {
		t.Fatal("stale settings did not recover")
	}
}
func TestQATaskContentAtSupportedWidthsAndThemes(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			config := domain.DefaultAppConfig()
			raw, _ := json.Marshal(config)
			s := newApp(nil, Options{Page: "tasks", Tab: 1, CapturePath: "qa-test"})
			s.loading = false
			s.configJSON = string(raw)
			s.snapshot.ConfigJSON = s.configJSON
			s.tasks = []task{{id: "qa-task", title: "测试任务", path: t.TempDir(), createdAt: "2026-10-27T00:00:00Z", groupID: domain.DefaultGroupID, materialsStatus: "NOT_REQUESTED"}}
			tester := ui.NewTester(func(c *ui.Context) { s.taskWorkspace(c, setTheme(c, dark)) }, width, 820)
			tester.Frame()
			for _, name := range []string{"任务资料", "需求详情", "需求说明"} {
				if !tester.HasText(name) {
					t.Fatalf("missing %s width=%d dark=%v", name, width, dark)
				}
			}
			for _, name := range []string{"任务详情", "Tag", "服务与模块", "构建 Tag"} {
				if tester.HasText(name) {
					t.Fatalf("retired control %s width=%d dark=%v", name, width, dark)
				}
			}
		}
	}
}
func TestQATaskCreationPayloadContainsNoCodeWorkspace(t *testing.T) {
	s := newApp(nil, Options{CapturePath: "qa-test"})
	config := domain.DefaultAppConfig()
	raw, _ := json.Marshal(config)
	s.snapshot.ConfigJSON = string(raw)
	s.initTaskCreation("创建测试任务", "测试任务", "")
	s.taskCreation.openCodex = false
	payload, err := s.taskCreationPayload()
	if err != nil {
		t.Fatal(err)
	}
	for key := range payload {
		if strings.Contains(strings.ToLower(key), "branch") || key == "serviceIds" || key == "serviceSelections" {
			t.Fatalf("developer parameter %s", key)
		}
	}
}

func TestQACreationDialogHasOnlyRetainedFields(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			s := newApp(nil, Options{CapturePath: "qa-test"})
			raw, _ := json.Marshal(domain.DefaultAppConfig())
			s.snapshot.ConfigJSON, s.configJSON = string(raw), string(raw)
			s.initTaskCreation("创建任务", "资料验收", "")
			s.dialogOpen, s.form.mode = true, "task-create"
			tester := ui.NewTester(func(c *ui.Context) { s.renderTaskCreationDialog(c, setTheme(c, dark)) }, width, 760)
			for _, name := range []string{"任务名称", "项目组", "需求说明", "创建任务", "创建后在 Codex 中打开"} {
				if !tester.HasText(name) {
					t.Fatalf("missing field %s", name)
				}
			}
			for _, name := range []string{"Worktree", "Tag", "分支名称", "选择服务", "Cursor"} {
				if tester.HasText(name) {
					t.Fatalf("retired field %s", name)
				}
			}
			bounds, ok := tester.Find("创建任务")
			if !ok || bounds.X < 0 || bounds.Y < 0 || bounds.X+bounds.W > float32(width) || bounds.Y+bounds.H > 760 {
				t.Fatalf("submit clipped: %+v", bounds)
			}
			captureDesktopTestFrame(t, tester, fmt.Sprintf("qa-create-%d-dark-%t", width, dark))
		}
	}
}
