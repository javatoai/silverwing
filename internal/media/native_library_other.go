//go:build !windows && !darwin && !linux

package media

import "errors"

func pdfiumLibraryName() string { return "PDFium native library" }

func openNativeLibrary(string) (loadedLibrary, error) {
	return loadedLibrary{}, errors.New("当前操作系统不支持动态加载 PDFium")
}
