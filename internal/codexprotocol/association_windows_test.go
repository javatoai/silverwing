//go:build windows

package codexprotocol

import (
	"context"
	"errors"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func TestLegacyAliasDetectionIsRestrictedAndDoesNotActivate(t *testing.T) {
	local, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(local, "Microsoft", "WindowsApps", "OpenAI.Codex_2p2nqsd0c76g0", "chatgpt-desktop.exe")
	command := syscall.EscapeArg(alias) + ` --single-argument "%1"`
	if !legacyStoreAliasCommand(command, local) {
		t.Fatal("old SW command not detected")
	}
	for _, other := range []string{`"C:\Program Files\Codex\Codex.exe" --single-argument "%1"`, command + " --custom", syscall.EscapeArg(alias) + ` "%1"`, `"C:\other\chatgpt-desktop.exe" --single-argument "%1"`} {
		if legacyStoreAliasCommand(other, local) {
			t.Fatal("custom association classified as old SW command", other)
		}
	}
	path := isolatedProtocol(t)
	key, _, err := registry.CreateKey(registry.CURRENT_USER, path+`\shell\open\command`, registry.ALL_ACCESS)
	if err != nil {
		t.Fatal(err)
	}
	defer key.Close()
	if err := key.SetStringValue("", command); err != nil {
		t.Fatal(err)
	}
	system := nativeSystem{registryPath: path}
	if err := system.activate(context.Background(), "codex://threads/new"); !errors.Is(err, errAssociationMissing) {
		t.Fatal("old alias was activated instead of repaired", err)
	}
	if err := key.SetStringValue("DelegateExecute", "{valid-existing-delegate}"); err != nil {
		t.Fatal(err)
	}
	if detected, err := system.legacyStoreAlias(); err != nil || detected {
		t.Fatal("valid delegate was replaced", detected, err)
	}
}
