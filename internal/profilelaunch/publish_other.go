//go:build !windows && !darwin

package profilelaunch

import "errors"

func publishShortcut(_, _ string, _ bool) error {
	return errors.New("此平台不支持创建 Silverwing 快捷方式")
}
