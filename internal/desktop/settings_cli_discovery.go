package desktop

import (
	"encoding/json"
	"strings"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

func (s *app) requestSettingsCLIStatus(tool string, force bool) {
	s.act("cli.status", "", "", "", "", "", map[string]any{"tool": tool, "force": force})
}

func (s *app) settingsCLIPathDiscoveryHint(c *ui.Context, p palette, tool string) {
	if s.cliConfiguredPath(tool) != "" {
		return
	}
	status := s.settingsCLIStatus[tool]
	if len(status) > 0 && !dataBool(status, "installed") || strings.HasPrefix(s.settingsOutput, "CLI 状态读取失败") {
		label(c, "未检测到可用命令，请手动填写可执行文件路径，修改后自动保存。", 10, p.amber).Wrap()
		return
	}
	label(c, "未配置时自动检测；也可手动填写可执行文件路径。", 10, p.muted).Wrap()
}

// Discovery changes only an absent persisted path. Keep unrelated configuration
// and any text the user entered while the status worker was reading.
func (s *app) applySettingsCLIDiscoverySnapshot(tool, previousConfig string, snapshot application.Snapshot) {
	key := map[string]string{"git": "gitExecutablePath", "codex": "codexExecutablePath", "meegle": "meegleExecutablePath", "lark": "larkExecutablePath"}[tool]
	if key == "" || snapshot.Home != s.snapshot.Home || snapshot.ConfigJSON == "" {
		return
	}
	var before, detected map[string]any
	if json.Unmarshal([]byte(previousConfig), &before) != nil || json.Unmarshal([]byte(snapshot.ConfigJSON), &detected) != nil || strings.TrimSpace(mapString(before, key)) != "" {
		return
	}
	current, err := s.settingsConfigMap()
	if err != nil || strings.TrimSpace(mapString(current, key)) != "" {
		return
	}
	path := strings.TrimSpace(mapString(detected, key))
	status := s.settingsCLIStatus[tool]
	command := firstNonEmpty(mapString(status, "command"), mapString(status, "commandPath"))
	if path == "" || path != command || !dataBool(status, "installed") {
		return
	}
	current[key] = path
	encoded, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return
	}
	if draft := s.inputs["setting:"+key]; draft != nil && *draft == "" {
		*draft = path
	}
	s.configJSON, s.snapshot.ConfigJSON = string(encoded), string(encoded)
}
