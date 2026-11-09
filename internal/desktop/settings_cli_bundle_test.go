package desktop

import (
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/platform"
)

func TestSettingsCLIBundleShowsInstalledInsteadOfInstall(t *testing.T) {
	s := newSettingsCLITabsTestApp(t)
	s.navigateSettingsPage("sw-install", "")
	s.settingsUnit.state.settingsLoaded = "fixture"
	view := ui.NewTester(func(c *ui.Context) { s.settingsCLIBundle(c, setTheme(c, false)) }, 650, 360)
	if !view.HasText("选择目录并安装") || !view.HasText("安装 Skill") || view.HasText("已安装") {
		t.Fatal("missing installations did not retain their installation actions")
	}
	s.applyCLIBundleStatus(application.Action{Payload: map[string]any{"settingsCLIBundleHome": s.snapshot.Home}},
		application.ActionResult{Data: platform.CLIBundleStatus{CLIPath: filepath.Join(s.snapshot.Home, "sw")}})
	view.Frame()
	if !view.HasText("已安装") || !view.HasText("重新安装") || view.HasText("选择目录并安装") || !view.HasText("安装 Skill") {
		t.Fatal("installed CLI did not get its own independent status")
	}
	s.applyCLIBundleInstall(application.Action{Payload: map[string]any{"artifact": "skill"}},
		application.ActionResult{Data: platform.CLIBundleInstallResult{Path: filepath.Join(s.snapshot.Home, "SKILL.md")}})
	view.Frame()
	if !view.HasText("已安装") || view.HasText("安装 Skill") {
		t.Fatal("successful Skill installation did not update its status immediately")
	}
}
