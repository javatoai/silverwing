package desktop

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSettingsCardActionsStayBesideTitles(t *testing.T) {
	for _, width := range []int{320, 900} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s := newSettingsCLITabsTestApp(t)
				s.navigateSettingsPage("task-creation", "")
				state := s.ensureSettingsExtensionRequests()
				state.workspaceToolsScope = s.settingsExtensionCurrentScope()
				taskPage := ui.NewTester(func(c *ui.Context) {
					p := setTheme(c, dark)
					ui.Column(c).Fill().Gap(12).Children(func() { s.settingsTaskCreation(c, p) })
				}, width, 760)
				refresh := assertSettingsCardActionBesideTitle(t, taskPage, "任务打开工具", "重新检查工具")
				codex := assertSettingsCardActionBesideTitle(t, taskPage, "AI 命名", "配置 Codex CLI")
				status, statusFound := taskPage.Find("Codex CLI")
				enable, enableFound := taskPage.Find("开启 AI 生成分支名和文件夹名")
				if !statusFound || !enableFound || status.Y >= enable.Y || taskPage.HasText("AI 命名所需工具") {
					t.Fatal("the Codex CLI status must appear first in the AI naming card")
				}
				if refresh.X+refresh.W != codex.X+codex.W {
					t.Fatalf("task creation actions have different right edges: %+v and %+v", refresh, codex)
				}
				taskPage.Key(0, ui.KeyTab)
				taskPage.Key(0, ui.KeyTab)
				if !taskPage.Focused("重新检查工具") {
					t.Fatal("the refresh action is not reachable through the card header")
				}
				previousRequest := state.serial
				taskPage.Key(0, ui.KeyEnter)
				if state.serial != previousRequest+1 {
					t.Fatal("refresh did not request workspace tools")
				}

				s.navigateSettingsPage("git", "")
				gitPage := ui.NewTester(func(c *ui.Context) {
					p := setTheme(c, dark)
					ui.Column(c).Fill().Gap(12).Children(func() { s.settingsGit(c, p) })
				}, width, 760)
				git := assertSettingsCardActionBesideTitle(t, gitPage, "Git 命令配置", "配置 Git")
				if git.X+git.W != codex.X+codex.W || git.X+git.W >= float32(width) {
					t.Fatalf("Git command action does not align with the card's right edge: %+v", git)
				}
			})
		}
	}
}

func assertSettingsCardActionBesideTitle(t *testing.T, tester *ui.Tester, title, action string) ui.Rect {
	t.Helper()
	titleBounds, titleFound := tester.Find(title)
	bounds, buttonFound := tester.Find(action)
	if !titleFound || !buttonFound {
		t.Fatalf("missing heading or action: %q (%t), %q (%t)", title, titleFound, action, buttonFound)
	}
	if bounds.W <= 0 || bounds.H <= 0 || bounds.X < titleBounds.X+titleBounds.W || bounds.Y >= titleBounds.Y+titleBounds.H || bounds.Y+bounds.H <= titleBounds.Y {
		t.Fatalf("action %q is not beside title %q: action=%+v title=%+v", action, title, bounds, titleBounds)
	}
	return bounds
}
