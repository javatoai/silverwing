package profilelaunch

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"silverwing-go/internal/productprofile"
	"silverwing-go/internal/storage"
)

const ConfigDirFlag = "conf-dir"
const ExpectedProfileIDFlag = "expected-profile-id"

// DirectoryFlags keeps aliases independent so conflicting values can be reported.
type DirectoryFlags struct {
	confDir, home       string
	confDirSet, homeSet bool
}

func (d *DirectoryFlags) Register(flags *flag.FlagSet) {
	flags.Func(ConfigDirFlag, "指定应用数据目录（省略时使用默认目录）", func(value string) error {
		d.confDir, d.confDirSet = value, true
		return nil
	})
	flags.Func("home", "--conf-dir 的兼容别名", func(value string) error {
		d.home, d.homeSet = value, true
		return nil
	})
}

func (d DirectoryFlags) Explicit() bool { return d.confDirSet || d.homeSet }

// Resolve returns the data directory without creating it.
func (d DirectoryFlags) Resolve() (string, error) {
	if !d.Explicit() {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("无法获取用户家目录：%w", err)
		}
		return CanonicalHome(productprofile.DefaultHome(userHome))
	}
	if !d.confDirSet {
		return CanonicalHome(d.home)
	}
	home, err := CanonicalHome(d.confDir)
	if err != nil || !d.homeSet {
		return home, err
	}
	legacyHome, err := CanonicalHome(d.home)
	if err != nil {
		return "", err
	}
	if !sameHome(home, legacyHome) {
		return "", errors.New("--conf-dir 和 --home 指向不同的应用数据目录")
	}
	return home, nil
}

// CanonicalHome resolves symlinks and junctions in the existing part of a data
// directory. A missing tail is legal so a new profile can be initialized later.
func CanonicalHome(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("请指定应用数据目录")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("解析应用数据目录：%w", err)
	}
	var suffix []string
	for parent := absolute; ; parent = filepath.Dir(parent) {
		if _, err := os.Lstat(parent); err != nil {
			if !errors.Is(err, os.ErrNotExist) || filepath.Dir(parent) == parent {
				return "", fmt.Errorf("读取应用数据目录 %s：%w", parent, err)
			}
			suffix = append(suffix, filepath.Base(parent))
			continue
		}
		real, err := filepath.EvalSymlinks(parent)
		if err != nil {
			return "", fmt.Errorf("解析应用数据目录 %s：%w", parent, err)
		}
		info, err := os.Stat(real)
		if err != nil {
			return "", fmt.Errorf("读取应用数据目录 %s：%w", real, err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("应用数据路径不是目录：%s", real)
		}
		for i := len(suffix) - 1; i >= 0; i-- {
			real = filepath.Join(real, suffix[i])
		}
		return real, nil
	}
}

func sameHome(first, second string) bool {
	if first == second {
		return true
	}
	firstInfo, firstErr := os.Stat(first)
	secondInfo, secondErr := os.Stat(second)
	if firstErr == nil && secondErr == nil {
		return os.SameFile(firstInfo, secondInfo)
	}
	return runtime.GOOS == "windows" && errors.Is(firstErr, os.ErrNotExist) &&
		errors.Is(secondErr, os.ErrNotExist) && strings.EqualFold(first, second)
}

// LockDesktop owns this configuration directory for the life of a desktop
// process. CLI operations have their own resource locks.
func LockDesktop(home string) (func(), error) {
	release, err := storage.AcquireLock(filepath.Join(storage.NewPaths(home).Locks, "desktop.lock"))
	if errors.Is(err, storage.ErrLockBusy) {
		return nil, fmt.Errorf("此应用数据目录已被另一个 Silverwing 窗口使用：%s", home)
	}
	if err != nil {
		return nil, fmt.Errorf("锁定应用数据目录 %s：%w", home, err)
	}
	return release, nil
}

// VerifyShortcut checks a launcher's original profile before any startup write.
func VerifyShortcut(home, expected string) error {
	actual, err := storage.ReadProfileID(home)
	if err == nil && actual == expected && expected != "" {
		return nil
	}
	if err != nil {
		return fmt.Errorf("快捷方式已失效。请重新创建快捷方式。应用数据目录 %s：%w", home, err)
	}
	return fmt.Errorf("快捷方式已失效。应用数据目录 %s 的标识已改变，请重新创建快捷方式", home)
}
