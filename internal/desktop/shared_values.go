package desktop

import (
	"encoding/json"
)

func decodeWorkspaceValue(value any, target any) error {
	content, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(content, target)
}
