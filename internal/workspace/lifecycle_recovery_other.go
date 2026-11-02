//go:build !windows

package workspace

import "fmt"

func copyIrregularRecoveryFile(source, _ string) error {
	return fmt.Errorf("不支持恢复特殊文件：%s", source)
}
