package desktop

import (
	"os"
	"path/filepath"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/platform"
)

func (s *app) settingsCLIBundle(c *ui.Context, p palette) {
	var status platform.CLIBundleStatus
	if current := s.settingsUnit.state.settingsCLIBundleStatus; current != nil {
		status = *current
	}
	settingsCard(c, p, "sw 命令行", "在终端批量构建 Tag、查询 Genbu 状态和发送通知。", func() {
		s.settingsCLIBundleAction(c, p, "install-sw-cli", "folder", "选择目录并安装", status.CLIPath != "", true, func() {
			var start string
			if status.CLIPath != "" {
				start = filepath.Dir(status.CLIPath)
			} else {
				home, err := os.UserHomeDir()
				if err != nil {
					s.lastError = err.Error()
					return
				}
				start = home
			}
			s.pickNativePath("选择 sw 安装目录", start, true, nil, func(dir string) {
				if s.showingCLIBundleSettings() {
					s.act("cli.bundle.install", "", "", "", dir, "", map[string]any{"artifact": "cli", "confirmed": true})
				}
			})
		})
	})
	settingsCard(c, p, "silverwing-tag Skill", "~/.agents/skills/silverwing-tag", func() {
		s.settingsCLIBundleAction(c, p, "install-sw-skill", "plus", "安装 Skill", status.SkillPath != "", false, func() {
			s.act("cli.bundle.install", "", "", "", "", "", map[string]any{"artifact": "skill", "confirmed": true})
		})
	})
	if message := s.settingsUnit.state.settingsOutput; message != "" {
		label(c, message, 12, p.muted).Wrap().Selectable()
	}
}

func (s *app) settingsCLIBundleAction(c *ui.Context, p palette, key, iconName, text string, installed, primary bool, install func()) {
	if installed {
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			pill(c, "已安装", p)
			ui.Spacer(c)
			if action(c, key, "refresh", "重新安装", p, false) && !s.busy {
				install()
			}
		})
		return
	}
	if s.operationKind == "cli.bundle.status" || s.settingsUnit.state.settingsLoaded == "" {
		settingsMuted(c, p, "正在检测安装状态…")
		return
	}
	if action(c, key, iconName, text, p, primary) && !s.busy {
		install()
	}
}

func (s *app) showingCLIBundleSettings() bool {
	return s.page == "settings" && s.settingsUnit.state.settingsPage == "sw-install"
}

func (s *app) applyCLIBundleStatus(action application.Action, result application.ActionResult) {
	if status, ok := result.Data.(platform.CLIBundleStatus); ok &&
		s.showingCLIBundleSettings() && payloadString(action.Payload, "settingsCLIBundleHome") == s.snapshot.Home {
		s.settingsUnit.state.settingsCLIBundleStatus = &status
	}
}

func (s *app) applyCLIBundleInstall(action application.Action, result application.ActionResult) {
	installed, ok := result.Data.(platform.CLIBundleInstallResult)
	if !ok || !s.showingCLIBundleSettings() {
		return
	}
	if installed.Replacement == "" {
		state := &s.settingsUnit.state
		if state.settingsCLIBundleStatus == nil {
			state.settingsCLIBundleStatus = &platform.CLIBundleStatus{}
		}
		switch payloadString(action.Payload, "artifact") {
		case "cli":
			state.settingsCLIBundleStatus.CLIPath = installed.Path
		case "skill":
			state.settingsCLIBundleStatus.SkillPath = installed.Path
		}
		state.settingsOutput = installed.RecordError
		return
	}
	s.confirmNative("替换已有安装文件", "此位置已有不同版本的文件。", installed.Path, func() {
		action.Payload = map[string]any{"artifact": payloadString(action.Payload, "artifact"), "confirmed": true, "expectedReplacement": installed.Replacement}
		s.dispatch(action)
	})
}
