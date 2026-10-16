//go:build darwin || linux

package media

import (
	"fmt"
	"runtime"

	"github.com/ebitengine/purego"
)

func pdfiumLibraryName() string {
	if runtime.GOOS == "darwin" {
		return "libpdfium.dylib"
	}
	return "libpdfium.so"
}

func openNativeLibrary(path string) (loadedLibrary, error) {
	handle, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return loadedLibrary{}, err
	}
	return loadedLibrary{
		addr: func(name string) (uintptr, error) {
			address, err := purego.Dlsym(handle, name)
			if err != nil {
				return 0, fmt.Errorf("%s：%w", name, err)
			}
			return address, nil
		},
		close: func() error { return purego.Dlclose(handle) },
	}, nil
}
