//go:build windows

package codexprotocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"
)

func isolatedProtocol(t *testing.T) string {
	t.Helper()
	path := fmt.Sprintf(`Software\SilverwingCodexProtocolTests\%d`, time.Now().UnixNano())
	t.Cleanup(func() {
		for _, suffix := range []string{`\shell\open\command`, `\shell\open`, `\shell`, ""} {
			if err := registry.DeleteKey(registry.CURRENT_USER, path+suffix); err != nil && !errors.Is(err, registry.ErrNotExist) {
				t.Error(err)
			}
		}
	})
	return path
}

type cancelAtCheckpoint struct {
	context.Context
	calls, at int
}

func (c *cancelAtCheckpoint) Err() error {
	c.calls++
	if c.calls >= c.at {
		return context.Canceled
	}
	return nil
}

func TestProtocolBackupWriteAndRollback(t *testing.T) {
	for _, original := range []bool{false, true} {
		t.Run(fmt.Sprint(original), func(t *testing.T) {
			path := isolatedProtocol(t)
			if original {
				key, _, err := registry.CreateKey(registry.CURRENT_USER, path+`\shell\open\command`, registry.ALL_ACCESS)
				if err != nil {
					t.Fatal(err)
				}
				if err := key.SetExpandStringValue("", `%LOCALAPPDATA%\old\Codex.exe "%1"`); err != nil {
					t.Fatal(err)
				}
				key.Close()
			}
			before, err := captureProtocol(registry.CURRENT_USER, path)
			if err != nil {
				t.Fatal(err)
			}
			backup, err := backupProtocol(t.TempDir(), before)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(backup)
			if err != nil {
				t.Fatal(err)
			}
			var saved protocolSnapshot
			if err := json.Unmarshal(data, &saved); err != nil || !reflect.DeepEqual(saved.Values, before.Values) {
				t.Fatal(saved, err)
			}
			ctx := &cancelAtCheckpoint{Context: context.Background(), at: 4}
			if err := writeProtocol(ctx, registry.CURRENT_USER, path, before, `"new.exe" "%1"`); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			after, err := captureProtocol(registry.CURRENT_USER, path)
			if err != nil || !reflect.DeepEqual(before.Keys, after.Keys) || !reflect.DeepEqual(before.Values, after.Values) {
				t.Fatal("rollback changed original association", before, after, err)
			}
			if err := writeProtocol(context.Background(), registry.CURRENT_USER, path, before, `"new.exe" "%1"`); err != nil {
				t.Fatal(err)
			}
			after, err = captureProtocol(registry.CURRENT_USER, path)
			if err != nil || after.Values[2].Text != `"new.exe" "%1"` {
				t.Fatal(after, err)
			}
		})
	}
}

func TestStoreAliasAllowsZeroBytesAndRejectsCLI(t *testing.T) {
	local := t.TempDir()
	alias := storeAlias{Family: "OpenAI.Codex_2p2nqsd0c76g0", Alias: "chatgpt-desktop.exe"}
	path := filepath.Join(local, "Microsoft", "WindowsApps", alias.Family, alias.Alias)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	command, err := storeDesktopCommand(local, []storeAlias{alias})
	if err != nil || command.Executable != path || !reflect.DeepEqual(command.Arguments, []string{"--single-argument"}) {
		t.Fatal(command, err)
	}
	alias.Alias = "codex.exe"
	if _, err := storeDesktopCommand(local, []storeAlias{alias}); err == nil {
		t.Fatal("CLI selected as desktop")
	}
	self, _ := os.Executable()
	if desktopGUI(self) {
		t.Fatal("console test binary treated as GUI")
	}
}

func TestActivationErrorRestrictsAutomaticRepair(t *testing.T) {
	for _, code := range []syscall.Errno{2, 3, 1155} {
		if !errors.Is(activationError(code), errAssociationMissing) {
			t.Fatal(code)
		}
	}
	for _, code := range []syscall.Errno{5, 31, 1223} {
		if errors.Is(activationError(code), errAssociationMissing) {
			t.Fatal(code)
		}
	}
}

func TestProtocolBackupFailureDoesNotChangeAssociation(t *testing.T) {
	path := isolatedProtocol(t)
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	system := nativeSystem{backups: blocked, registryPath: path}
	if err := system.repair(context.Background(), desktopCommand{Executable: `C:\Codex.exe`}); err == nil {
		t.Fatal("backup failure was ignored")
	}
	snapshot, err := captureProtocol(registry.CURRENT_USER, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, exists := range snapshot.Keys {
		if exists {
			t.Fatal("association changed before backup succeeded")
		}
	}
}

func TestProtocolRejectsUnrecoverableRegistryValue(t *testing.T) {
	path := isolatedProtocol(t)
	key, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.ALL_ACCESS)
	if err != nil {
		t.Fatal(err)
	}
	defer key.Close()
	if err := key.SetDWordValue("URL Protocol", 42); err != nil {
		t.Fatal(err)
	}
	if _, err := captureProtocol(registry.CURRENT_USER, path); err == nil {
		t.Fatal("unsupported original value cannot be backed up safely")
	}
	if got, _, err := key.GetIntegerValue("URL Protocol"); err != nil || got != 42 {
		t.Fatal("original value changed", got, err)
	}
}

func TestProtocolMutexHonorsCancellationAndReleases(t *testing.T) {
	system := nativeSystem{}
	release, err := system.lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		unlock, err := system.lock(ctx)
		if err == nil {
			unlock()
		}
		done <- err
	}()
	err = <-done
	release()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("waiting repair did not honor cancellation", err)
	}
	release, err = system.lock(context.Background())
	if err != nil {
		t.Fatal("mutex not released", err)
	}
	release()
}

func TestInstalledPathExpandsEnvironmentAndQuotes(t *testing.T) {
	t.Setenv("SW_CODEX_TEST_ROOT", `C:\Codex Desktop`)
	if got := installedPath(` "%SW_CODEX_TEST_ROOT%\Codex.exe" `); got != `C:\Codex Desktop\Codex.exe` {
		t.Fatal(got)
	}
}
