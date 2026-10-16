//go:build (!windows && !darwin) || ((windows || darwin) && !amd64 && !arm64)

package platform

import (
	"errors"
	"fmt"
)

var ErrWordBoundariesUnavailable = errors.New("system ICU word boundaries are unavailable")

// WordBoundaries is unavailable outside Windows/macOS amd64/arm64, where
// this package can bind the system ICU C API without a different segmenter.
func WordBoundaries(string) ([]int, error) {
	return nil, fmt.Errorf("%w on this platform", ErrWordBoundariesUnavailable)
}
