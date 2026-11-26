package platform

import (
	"fmt"
	"unicode/utf8"
)

func readExtensionText(path string, limit int64) ([]byte, error) {
	data, err := readBounded(path, limit)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("文件不是有效的 UTF-8 文本：%s", path)
	}
	return data, nil
}
