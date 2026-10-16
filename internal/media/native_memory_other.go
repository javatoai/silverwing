//go:build !windows && !darwin && !linux

package media

import "errors"

func makeNativeInput([]byte) (nativeInput, error) {
	return nativeInput{}, errors.New("PDF 预览暂不支持当前操作系统")
}
