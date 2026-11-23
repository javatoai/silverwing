package profilelaunch

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"silverwing-go/internal/productprofile"
)

// Shortcut identifies a desktop executable and the profile it must open.
type Shortcut struct {
	Name, Executable, Home, ProfileID string
}

func (s Shortcut) args() []string {
	return []string{"--" + ConfigDirFlag, s.Home, "--" + ExpectedProfileIDFlag, s.ProfileID}
}

func ShortcutExtension() string { return shortcutExtension() }

func SuggestedShortcutName(home string) string {
	clean := filepath.Clean(home)
	name := filepath.Base(clean)
	if name == string(filepath.Separator) || name == "." {
		name = strings.TrimSuffix(filepath.VolumeName(clean), ":")
	}
	name = strings.Map(func(r rune) rune {
		if r < ' ' || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '-'
		}
		return r
	}, name)
	if name = strings.Trim(name, " ."); name == "" {
		return productprofile.Name + shortcutExtension()
	}
	return productprofile.Name + " - " + name + shortcutExtension()
}

// ShortcutPath returns the final filename, including the native extension.
func ShortcutPath(path string) (string, error) {
	extension := shortcutExtension()
	if extension == "" {
		return "", errors.New("此平台不支持创建 Silverwing 快捷方式")
	}
	if strings.TrimSpace(path) == "" {
		return "", errors.New("请指定快捷方式名称")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	base := filepath.Base(path)
	if base == string(filepath.Separator) || strings.EqualFold(base, extension) {
		return "", errors.New("请指定快捷方式名称")
	}
	if !strings.EqualFold(filepath.Ext(path), extension) {
		path += extension
	}
	return path, nil
}

// ShortcutOccupant reports the existing item at the normalized destination.
// The caller may pass this exact item back after obtaining overwrite confirmation.
func ShortcutOccupant(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	canReplace := info.Mode().IsRegular()
	if shortcutExtension() == ".app" {
		plist, err := os.Lstat(filepath.Join(path, "Contents", "Info.plist"))
		canReplace = info.IsDir() && err == nil && plist.Mode().IsRegular()
	}
	if !canReplace {
		return nil, fmt.Errorf("目标不是可替换的快捷方式：%s", path)
	}
	// Windows may load the file identity lazily from the path. Capture it
	// while this item is still present and before showing the confirmation.
	if !os.SameFile(info, info) {
		return nil, fmt.Errorf("无法识别目标快捷方式：%s", path)
	}
	return info, nil
}

// CreateShortcut builds the native launcher privately, then publishes it.
// A nil confirmed item permits creation only.
func CreateShortcut(path string, spec Shortcut, confirmed os.FileInfo) error {
	path, err := ShortcutPath(path)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(spec.Home) || !filepath.IsAbs(spec.Executable) || spec.ProfileID == "" {
		return errors.New("创建快捷方式需要程序路径、应用数据目录和目录标识")
	}
	if err := VerifyShortcut(spec.Home, spec.ProfileID); err != nil {
		return err
	}
	name := filepath.Base(path)
	spec.Name = strings.TrimSuffix(name, filepath.Ext(name))
	stage, err := os.MkdirTemp(filepath.Dir(path), ".silverwing-shortcut-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	staged := filepath.Join(stage, name)
	if err := writeNativeShortcut(staged, spec); err != nil {
		return err
	}
	if confirmed == nil {
		return publishShortcut(staged, path, false)
	}
	current, err := ShortcutOccupant(path)
	if err != nil {
		return err
	}
	if current == nil {
		return publishShortcut(staged, path, false)
	}
	if !os.SameFile(confirmed, current) || confirmed.Size() != current.Size() || !confirmed.ModTime().Equal(current.ModTime()) {
		return fmt.Errorf("目标快捷方式已发生变化，请重新确认：%s", path)
	}
	return publishShortcut(staged, path, true)
}
