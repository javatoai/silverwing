package documents

import (
	"errors"
	"os"
)

func validateOpenedMaterialFile(file *os.File, expected os.FileInfo) error {
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	if !opened.Mode().IsRegular() {
		return errors.New("只能读取普通文件")
	}
	if expected == nil || !os.SameFile(expected, opened) {
		return errors.New("资料文件在读取前发生了变化，请刷新后重试")
	}
	return nil
}
