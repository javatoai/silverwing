//go:build windows

package codexprotocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows/registry"
)

type protocolValue struct {
	Path   string
	Name   string
	Exists bool
	Kind   uint32
	Text   string
}
type protocolSnapshot struct {
	CreatedAt time.Time
	Keys      map[string]bool
	Values    []protocolValue
}

func captureProtocol(root registry.Key, path string) (protocolSnapshot, error) {
	snapshot := protocolSnapshot{CreatedAt: time.Now().UTC(), Keys: map[string]bool{}}
	for _, suffix := range []string{"", `\shell`, `\shell\open`, `\shell\open\command`} {
		key, err := registry.OpenKey(root, path+suffix, registry.READ)
		if errors.Is(err, registry.ErrNotExist) {
			snapshot.Keys[path+suffix] = false
			continue
		}
		if err != nil {
			return snapshot, err
		}
		snapshot.Keys[path+suffix] = true
		key.Close()
	}
	for _, value := range []protocolValue{{Path: path}, {Path: path, Name: "URL Protocol"}, {Path: path + `\shell\open\command`}} {
		if snapshot.Keys[value.Path] {
			key, err := registry.OpenKey(root, value.Path, registry.READ)
			if err != nil {
				return snapshot, err
			}
			value.Text, value.Kind, err = key.GetStringValue(value.Name)
			key.Close()
			if err != nil && !errors.Is(err, registry.ErrNotExist) {
				return snapshot, fmt.Errorf("无法安全备份协议值：%w", err)
			}
			value.Exists = err == nil
		}
		snapshot.Values = append(snapshot.Values, value)
	}
	return snapshot, nil
}

func backupProtocol(directory string, snapshot protocolSnapshot) (string, error) {
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(directory, "codex-protocol-*.json")
	if err != nil {
		return "", err
	}
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	return file.Name(), errors.Join(writeErr, file.Close())
}

func (s nativeSystem) repair(ctx context.Context, command desktopCommand) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// A registered delegate may have no plain executable command. Leave it to
	// Windows rather than replacing a packaged or user-selected handler.
	for _, keyPath := range []string{s.registryPath + `\shell\open`, s.registryPath + `\shell\open\command`} {
		key, err := registry.OpenKey(registry.CLASSES_ROOT, strings.TrimPrefix(keyPath, `Software\Classes\`), registry.READ)
		if err == nil {
			delegate, _, _ := key.GetStringValue("DelegateExecute")
			key.Close()
			if delegate != "" {
				return errors.New("Codex 已注册系统委托，请在 Windows 默认应用中恢复 Codex 关联")
			}
		}
	}
	snapshot, err := captureProtocol(registry.CURRENT_USER, s.registryPath)
	if err != nil {
		return err
	}
	backup, err := backupProtocol(filepath.Join(s.backups, "codex-protocol"), snapshot)
	if err != nil {
		return fmt.Errorf("备份协议失败：%w", err)
	}
	quoted := []string{syscall.EscapeArg(command.Executable)}
	for _, arg := range command.Arguments {
		quoted = append(quoted, syscall.EscapeArg(arg))
	}
	text := strings.Join(quoted, " ") + ` "%1"`
	if err := writeProtocol(ctx, registry.CURRENT_USER, s.registryPath, snapshot, text); err != nil {
		return fmt.Errorf("%w；备份位于 %s", err, backup)
	}
	notifyAssociation.Call(0x08000000, 0, 0, 0)
	return nil
}

func writeProtocol(ctx context.Context, root registry.Key, path string, snapshot protocolSnapshot, command string) (result error) {
	written := []protocolValue{}
	defer func() {
		if result != nil {
			result = errors.Join(result, restoreProtocol(root, snapshot, written))
		}
	}()
	for _, value := range []protocolValue{{Path: path, Text: "URL:Codex"}, {Path: path, Name: "URL Protocol"}, {Path: path + `\shell\open\command`, Text: command}} {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Keep an existing description and URL marker verbatim.
		if value.Path == path {
			found := false
			for _, old := range snapshot.Values {
				if old.Path == value.Path && old.Name == value.Name && old.Exists {
					found = true
				}
			}
			if found {
				continue
			}
		}
		key, _, err := registry.CreateKey(root, value.Path, registry.READ|registry.WRITE)
		if err != nil {
			return err
		}
		err = key.SetStringValue(value.Name, value.Text)
		if err == nil {
			written = append(written, value)
		}
		if err == nil {
			actual, _, readErr := key.GetStringValue(value.Name)
			if readErr != nil {
				err = readErr
			} else if actual != value.Text {
				err = errors.New("协议写入后校验失败")
			}
		}
		key.Close()
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}

func restoreProtocol(root registry.Key, snapshot protocolSnapshot, written []protocolValue) error {
	var failures []error
	for _, value := range written {
		key, err := registry.OpenKey(root, value.Path, registry.READ|registry.WRITE)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		current, _, err := key.GetStringValue(value.Name)
		if err == nil && current != value.Text {
			err = errors.New("协议已被其他应用修改，请使用备份核对")
		}
		if err == nil {
			for _, old := range snapshot.Values {
				if old.Path != value.Path || old.Name != value.Name {
					continue
				}
				switch {
				case !old.Exists:
					err = key.DeleteValue(old.Name)
				case old.Kind == registry.EXPAND_SZ:
					err = key.SetExpandStringValue(old.Name, old.Text)
				default:
					err = key.SetStringValue(old.Name, old.Text)
				}
			}
		}
		key.Close()
		if err != nil {
			failures = append(failures, err)
		}
	}
	// Delete only keys that this repair created and which remain empty.
	var rootPath string
	for _, old := range snapshot.Values {
		if old.Name == "URL Protocol" {
			rootPath = old.Path
		}
	}
	for _, suffix := range []string{`\shell\open\command`, `\shell\open`, `\shell`, ""} {
		path := rootPath + suffix
		if snapshot.Keys[path] {
			continue
		}
		key, err := registry.OpenKey(root, path, registry.READ)
		if errors.Is(err, registry.ErrNotExist) {
			continue
		}
		if err != nil {
			failures = append(failures, err)
			continue
		}
		children, childErr := key.ReadSubKeyNames(-1)
		values, valueErr := key.ReadValueNames(-1)
		key.Close()
		if childErr != nil || valueErr != nil {
			failures = append(failures, errors.Join(childErr, valueErr))
			continue
		}
		if len(children) == 0 && len(values) == 0 {
			if err := registry.DeleteKey(root, path); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}
