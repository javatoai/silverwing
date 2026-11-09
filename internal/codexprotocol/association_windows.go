//go:build windows

package codexprotocol

import (
	"errors"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Recognize only the exact user-level command previously written by Silverwing.
// A valid AppX delegate or a custom command is left to Windows.
func (s nativeSystem) legacyStoreAlias() (bool, error) {
	if s.registryPath == "" {
		return false, nil
	}
	var command string
	for _, suffix := range []string{`\shell\open`, `\shell\open\command`} {
		key, err := registry.OpenKey(registry.CURRENT_USER, s.registryPath+suffix, registry.READ)
		if errors.Is(err, registry.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, err
		}
		delegate, _, _ := key.GetStringValue("DelegateExecute")
		if suffix == `\shell\open\command` {
			command, _, _ = key.GetStringValue("")
		}
		key.Close()
		if delegate != "" {
			return false, nil
		}
	}
	if command == "" {
		return false, nil
	}
	local, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		return false, err
	}
	return legacyStoreAliasCommand(command, local), nil
}

func legacyStoreAliasCommand(command, local string) bool {
	args, err := windows.DecomposeCommandLine(command)
	if err != nil || len(args) != 3 || args[1] != "--single-argument" || args[2] != "%1" {
		return false
	}
	for _, alias := range []string{"chatgpt-desktop.exe", "codex-desktop.exe"} {
		path := filepath.Join(local, "Microsoft", "WindowsApps", "OpenAI.Codex_2p2nqsd0c76g0", alias)
		if strings.EqualFold(filepath.Clean(args[0]), path) {
			return true
		}
	}
	return false
}
