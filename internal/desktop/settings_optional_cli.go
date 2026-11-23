package desktop

import (
	"encoding/json"
	"strconv"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

func (s *app) settingsOptionalCLISwitch(c *ui.Context, p palette, tool, title string, enabled bool) {
	key := "optional-cli-" + tool
	value, pending := s.settingsConfigSwitchInput(key, enabled)
	settingsConfigSwitchRow(c, p, key, "启用 "+title, "发送飞书消息。", value, pending || s.busy, func() {
		s.saveSettingsConfigFields(application.ConfigFieldsUpdate{ObjectFields: map[string]map[string]json.RawMessage{
			"optionalCLIs": {tool: json.RawMessage(strconv.FormatBool(*value))},
		}})
	})
}

func (s *app) settingsOptionalCLIStatus(c *ui.Context, p palette, tool string) {
	status := s.optionalCLIStatus(tool)
	state, color := "检测中…", p.muted
	switch status.Phase {
	case application.OptionalCLIAvailable:
		state, color = "可用", p.green
	case application.OptionalCLIUnavailable:
		state, color = "不可用", p.amber
	case application.OptionalCLIDisabled:
		state = "已停用"
	}
	settingsToolStatusRow(c, p, "命令状态", state, color)
	if status.Version != "" {
		settingsToolStatusRow(c, p, "版本", status.Version, p.text)
	}
	if status.Source != "" {
		settingsToolStatusRow(c, p, "来源", cliStatusSourceLabel(status.Source), p.text)
	}
	if status.Error != "" {
		settingsToolStatusRow(c, p, "检测错误", status.Error, p.amber)
	}
}
