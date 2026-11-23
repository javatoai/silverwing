package desktop

import (
	"encoding/json"
	"strings"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

func (s *app) requestSettingsCLIStatus(tool string, force bool) {
	if !desktopCLIJobSupported(tool) {
		return
	}
	if desktopHasOptionalCLI(tool) {
		s.requestOptionalCLIProbe(tool, force)
		if !s.optionalCLIReady(tool) || tool != "lark" || force {
			return
		}
		if jobs := s.toolsUnit.state.cliJobs; jobs != nil && jobs.requests["status:"+tool] != nil {
			return
		}
	}
	s.act("cli.status", "", "", "", "", "", map[string]any{"tool": tool, "force": force})
}

func (s *app) settingsCLIPathDiscoveryHint(c *ui.Context, p palette, tool string) {
	if s.cliConfiguredPath(tool) != "" {
		return
	}
	status := s.settingsUnit.state.settingsCLIStatus[tool]
	if len(status) > 0 && !dataBool(status, "installed") || s.settingsUnit.state.settingsCLIErrors[tool] != "" {
		label(c, "未检测到可用命令，请手动填写可执行文件路径，修改后自动保存。", 10, p.amber).Wrap()
		return
	}
	// The card's help already explains automatic detection and manual paths.
	// Keep only the actionable detection failure inline.
}

// Merge the path detected or repaired by this status request while retaining
// unrelated settings and edits the user made during the probe.
func (s *app) applySettingsCLIDiscoverySnapshot(tool, previousConfig string, snapshot application.Snapshot) {
	key := map[string]string{"lark": "larkExecutablePath", "git": "gitExecutablePath", "codex": "codexExecutablePath", "meegle": "meegleExecutablePath"}[tool]
	if key == "" || snapshot.Home != s.snapshot.Home || snapshot.ConfigJSON == "" {
		return
	}
	var before, detected map[string]any
	if json.Unmarshal([]byte(previousConfig), &before) != nil || json.Unmarshal([]byte(snapshot.ConfigJSON), &detected) != nil {
		return
	}
	previousPath := mapString(before, key)
	configuredPath := strings.TrimSpace(previousPath)
	current, err := s.settingsConfigMap()
	if err != nil || strings.TrimSpace(mapString(current, key)) != configuredPath {
		return
	}
	path := strings.TrimSpace(mapString(detected, key))
	status := s.settingsUnit.state.settingsCLIStatus[tool]
	command := firstNonEmpty(mapString(status, "command"), mapString(status, "commandPath"))
	if configuredPath != "" && (tool != "codex" || mapString(status, "clearedConfiguredPath") != configuredPath) {
		return
	}
	if path == "" {
		if configuredPath == "" {
			return
		}
		current[key] = nil
	} else {
		if path != command || !dataBool(status, "installed") {
			return
		}
		current[key] = path
	}
	encoded, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return
	}
	next := string(encoded)
	if configuredPath != "" {
		// The backend already saved the repair. Rebase the input so it does not
		// enqueue another settings save and turn recovery into another probe.
		s.syncSettingsConfigInputs(s.settingsUnit.state.configJSON, next)
	} else if draft := s.inputs["setting:"+key]; draft != nil && *draft == "" {
		*draft = path
	}
	s.settingsUnit.state.configJSON, s.snapshot.ConfigJSON = next, next
	if desktopHasOptionalCLI(tool) {
		s.observeOptionalCLIs()
	}
}
