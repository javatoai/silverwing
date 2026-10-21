//go:build darwin && (amd64 || arm64)

package platform

import (
	"errors"
	"fmt"

	"github.com/ebitengine/purego"
)

var ErrWordBoundariesUnavailable = errors.New("system ICU word boundaries are unavailable")

const macSystemICULibraryPath = "/usr/lib/libicucore.A.dylib"

// WordBoundaries returns UBRK_WORD boundaries as UTF-16 code-unit offsets.
// macOS supplies the native ICU and default locale. Its dictionary/data version
// can differ from Compose's bundled Skia ICU, just as on Windows.
func WordBoundaries(text string) ([]int, error) {
	return macICUWordBoundaries(text, loadMacICUWordBoundariesAPI)
}

func loadMacICUWordBoundariesAPI() (*macICUWordBoundariesAPI, error) {
	library, err := purego.Dlopen(macSystemICULibraryPath, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, fmt.Errorf("%w: load system ICU %q: %v", ErrWordBoundariesUnavailable, macSystemICULibraryPath, err)
	}
	api := &macICUWordBoundariesAPI{release: func() error { return purego.Dlclose(library) }}
	bindings := []struct {
		name string
		fn   any
	}{
		{name: "ubrk_open", fn: &api.open},
		{name: "ubrk_first", fn: &api.first},
		{name: "ubrk_next", fn: &api.next},
		{name: "ubrk_close", fn: &api.close},
	}
	for _, binding := range bindings {
		address, findErr := purego.Dlsym(library, binding.name)
		if findErr != nil {
			return nil, errors.Join(
				fmt.Errorf("%w: system ICU %q has no %s export: %v", ErrWordBoundariesUnavailable, macSystemICULibraryPath, binding.name, findErr),
				api.release(),
			)
		}
		purego.RegisterFunc(binding.fn, address)
	}
	return api, nil
}
