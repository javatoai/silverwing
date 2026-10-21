//go:build aix || dragonfly || freebsd || illumos || netbsd || openbsd || solaris

package documents

import "errors"

// No non-atomic stat/rename fallback: preserving both directories takes
// precedence when this platform has no supported exclusive rename API.
func renameDirectoryNoReplace(source, destination string) error {
	return errors.ErrUnsupported
}
