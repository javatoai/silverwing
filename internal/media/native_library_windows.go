//go:build windows

package media

import (
	"fmt"
	"syscall"
)

func pdfiumLibraryName() string { return "pdfium.dll" }

func openNativeLibrary(path string) (loadedLibrary, error) {
	dll, err := syscall.LoadDLL(path)
	if err != nil {
		return loadedLibrary{}, err
	}
	return loadedLibrary{
		addr: func(name string) (uintptr, error) {
			procedure, err := dll.FindProc(name)
			if err != nil {
				return 0, fmt.Errorf("%s：%w", name, err)
			}
			return procedure.Addr(), nil
		},
		close: dll.Release,
	}, nil
}
