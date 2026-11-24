//go:build windows

package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func addCLIUserPath(ctx context.Context, directory string) (string, error) {
	if !filepath.IsAbs(directory) || strings.ContainsAny(directory, "\x00;\"") {
		return "", errors.New("安装目录无法作为 Windows PATH 项")
	}
	directory = filepath.Clean(directory)
	changed, err := persistCLIUserPath(ctx, directory)
	if err != nil {
		return "", err
	}
	if changed {
		broadcastEnvironmentChange()
		return "已将安装目录加入用户 PATH。请重新打开终端后运行 sw。", nil
	}
	return "安装目录已在用户 PATH 中。请重新打开终端后运行 sw。", nil
}

func persistCLIUserPath(ctx context.Context, directory string) (bool, error) {
	unlock, err := lockCLIUserPath(ctx)
	if err != nil {
		return false, fmt.Errorf("无法锁定用户 PATH：%w", err)
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return false, fmt.Errorf("无法打开用户环境变量：%w", err)
	}
	defer key.Close()
	changed, err := appendCLIUserPathValue(ctx, key, directory)
	if err != nil {
		return false, err
	}

	// Terminals subsequently opened by this Silverwing process inherit this PATH.
	current := os.Getenv("PATH")
	if !cliPathContainsDirectory(current, directory) {
		_ = os.Setenv("PATH", joinCLIPathEntry(current, directory))
	}
	return changed, nil
}

func appendCLIUserPathValue(ctx context.Context, key registry.Key, directory string) (bool, error) {
	path, kind, err := key.GetStringValue("Path")
	if errors.Is(err, registry.ErrNotExist) {
		kind = registry.SZ
	} else if err != nil {
		return false, fmt.Errorf("无法读取用户 PATH：%w", err)
	}
	search := path
	if kind == registry.EXPAND_SZ {
		search, err = registry.ExpandString(path)
		if err != nil {
			return false, fmt.Errorf("无法展开用户 PATH 的环境变量：%w", err)
		}
		expandedDir, err := registry.ExpandString(directory)
		if err != nil {
			return false, fmt.Errorf("无法检查安装目录中的环境变量：%w", err)
		}
		if expandedDir != directory {
			return false, errors.New("安装目录包含会被用户 PATH 展开的环境变量")
		}
	}
	if cliPathContainsDirectory(search, directory) {
		return false, nil
	}
	if strings.Count(path, "\"")%2 != 0 || strings.Count(search, "\"")%2 != 0 {
		return false, errors.New("用户 PATH 中的引号不完整")
	}
	next := joinCLIPathEntry(path, directory)
	if len(utf16.Encode([]rune(next))) >= 32767 || len(utf16.Encode([]rune(joinCLIPathEntry(search, directory)))) >= 32767 {
		return false, errors.New("用户 PATH 超过 Windows 环境变量长度限制")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if kind == registry.EXPAND_SZ {
		err = key.SetExpandStringValue("Path", next)
	} else {
		err = key.SetStringValue("Path", next)
	}
	if err != nil {
		return false, fmt.Errorf("无法保存用户 PATH：%w", err)
	}
	return true, nil
}

func cliPathContainsDirectory(path, directory string) bool {
	directory = strings.TrimRight(directory, `\/`)
	for _, entry := range filepath.SplitList(path) {
		entry = strings.TrimSpace(entry)
		if filepath.IsAbs(entry) && strings.EqualFold(strings.TrimRight(filepath.Clean(entry), `\/`), directory) {
			return true
		}
	}
	return false
}

func joinCLIPathEntry(path, directory string) string {
	if path == "" || strings.HasSuffix(path, ";") {
		return path + directory
	}
	return path + ";" + directory
}

// Different profiles can install independently, but they share this user's PATH.
func lockCLIUserPath(ctx context.Context) (func(), error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	name, err := windows.UTF16PtrFromString(`Global\Silverwing-CLIUserPath-` + user.User.Sid.String())
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return nil, err
	}
	runtime.LockOSThread()
	deadline := time.Now().Add(5 * time.Second)
	for ctx.Err() == nil && time.Now().Before(deadline) {
		state, waitErr := windows.WaitForSingleObject(handle, 50)
		if waitErr != nil {
			err = waitErr
			break
		}
		if state == windows.WAIT_OBJECT_0 || state == windows.WAIT_ABANDONED {
			return func() {
				_ = windows.ReleaseMutex(handle)
				_ = windows.CloseHandle(handle)
				runtime.UnlockOSThread()
			}, nil
		}
	}
	_ = windows.CloseHandle(handle)
	runtime.UnlockOSThread()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return nil, err
	}
	return nil, errors.New("其他 Silverwing 实例正在更新用户 PATH，请重试")
}
