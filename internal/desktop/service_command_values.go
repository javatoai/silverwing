package desktop

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// The two command forms share value rules but have different directory and
// identity policies. Their input defaults remain with their existing drafts.
type serviceCommandPurpose uint8

const (
	serviceCommandForModule serviceCommandPurpose = iota
	serviceCommandForBootstrap
)

type serviceCommandValues struct {
	raw                                                          map[string]any
	ID, Name, Program, ArgsText, Directory, TimeoutText, IconKey string
	Enabled                                                      bool
}

// Validation is separate from assembly so the editor can retain its lazy input
// capture. Neither function reads caches, assigns IDs or persists configuration.
func validateServiceCommandValues(values serviceCommandValues, purpose serviceCommandPurpose) (int64, error) {
	if strings.TrimSpace(values.Name) == "" || strings.TrimSpace(values.Program) == "" {
		return 0, fmt.Errorf("命令名称和可执行程序不能为空")
	}
	if strings.TrimSpace(values.Directory) == "" {
		return 0, fmt.Errorf("命令工作目录不能为空")
	}
	if purpose == serviceCommandForModule {
		if values.ID == "" {
			return 0, fmt.Errorf("快捷命令 ID 不能为空")
		}
		if err := validateRelativeConfigPath(values.Directory, "快捷命令工作目录"); err != nil {
			return 0, err
		}
	}
	timeout, err := parsePositiveInt(strings.TrimSpace(values.TimeoutText))
	if err != nil {
		return 0, fmt.Errorf("命令“%s”的超时必须是大于 0 的整数", values.Name)
	}
	return timeout, nil
}

// serviceCommandConfig consumes the validated timeout and owns a copy of raw.
// User text is preserved verbatim except for the established per-line argument
// handling; TrimSpace in validation does not rewrite the stored form values.
func serviceCommandConfig(values serviceCommandValues, purpose serviceCommandPurpose, timeout int64) map[string]any {
	command := cloneConfigMap(values.raw)
	if command == nil {
		command = map[string]any{}
	}
	command["name"], command["executable"], command["arguments"] = values.Name, values.Program, nonBlankLines(values.ArgsText)
	command["workingDirectory"], command["timeoutSeconds"] = values.Directory, timeout
	command["enabled"] = values.Enabled
	if purpose == serviceCommandForModule {
		command["id"] = values.ID
		iconKey := values.IconKey
		if strings.TrimSpace(iconKey) == "" {
			iconKey = "build"
		}
		command["iconKey"] = iconKey
	} else {
		delete(command, "iconKey")
		delete(command, "id")
	}
	return command
}

func cloneConfigMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	encoded, err := json.Marshal(source)
	if err != nil {
		return nil
	}
	var cloned map[string]any
	if json.Unmarshal(encoded, &cloned) != nil {
		return nil
	}
	return cloned
}

func nonBlankLines(text string) []string {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	lines := make([]string, 0)
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func parsePositiveInt(value string) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("invalid integer")
	}
	return parsed, nil
}

func validateRelativeConfigPath(value, label string) error {
	pathValue := filepath.FromSlash(value)
	if filepath.IsAbs(pathValue) {
		return fmt.Errorf("%s 必须使用仓库内相对路径：%s", label, value)
	}
	for _, part := range strings.Split(strings.ReplaceAll(value, "\\", "/"), "/") {
		if part == ".." {
			return fmt.Errorf("%s 不能包含 ..：%s", label, value)
		}
		if strings.EqualFold(part, ".git") {
			return fmt.Errorf("%s 不能访问 .git：%s", label, value)
		}
	}
	return nil
}
