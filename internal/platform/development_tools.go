package platform

import (
	"os"

	"runtime"

	"strings"
)

type terminalCommandResolution struct {
	DisplayName string `json:"displayName"`
	Command     string `json:"command"`
	Source      string `json:"source"`
	Available   bool   `json:"available"`
}

func (s *Service) resolveTerminal(_ map[string]any) (any, error) {
	return resolveTerminalCommand(nil), nil
}

func resolveTerminalCommand(_ *string) terminalCommandResolution {
	switch runtime.GOOS {
	case "windows":
		return terminalCommandResolution{DisplayName: "系统默认终端", Command: "powershell.exe", Source: "SYSTEM_DEFAULT", Available: true}
	case "darwin":
		return terminalCommandResolution{DisplayName: "Terminal", Command: "Terminal.app", Source: "SYSTEM_DEFAULT", Available: true}
	default:
		return terminalCommandResolution{DisplayName: "系统终端", Command: "x-terminal-emulator", Source: "SYSTEM_DEFAULT", Available: true}
	}
}

func applicationExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if runtime.GOOS == "darwin" && strings.HasSuffix(strings.ToLower(path), ".app") {
		return info.IsDir()
	}
	return info.Mode().IsRegular()
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func nonEmpty(values []string) []string {
	result := values[:0]
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, value)
		}
	}
	return result
}

func stringPointerOrNil(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	copy := strings.TrimSpace(value)
	return &copy
}
