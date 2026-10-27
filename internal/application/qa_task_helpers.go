package application

import (
	"encoding/json"
	"fmt"
	"silverwing-go/internal/domain"
)

func decodePayload(payload map[string]any, target any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}
func decodeField(action Action, key string, target any) error {
	data, err := json.Marshal(action.Payload[key])
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func codexLaunchFailure(m domain.TaskManifest) error {
	for _, launch := range m.WorkspaceToolLaunches {
		if launch.ToolID == "codex" && launch.Status == domain.WorkspaceToolFailed {
			return fmt.Errorf("Codex 启动失败：%s", value(launch.Message))
		}
	}
	return nil
}
