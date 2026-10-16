//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package documents

import (
	"errors"
	"os"
)

func renameNoReplace(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode().IsRegular() {
		if err := os.Link(source, destination); err != nil {
			return err
		}
		if err := os.Remove(source); err != nil {
			_ = os.Remove(destination)
			return err
		}
		return nil
	}
	if !info.IsDir() {
		return &os.LinkError{Op: "rename", Old: source, New: destination, Err: errors.ErrUnsupported}
	}
	if err := renameDirectoryNoReplace(source, destination); err != nil {
		return &os.LinkError{Op: "rename", Old: source, New: destination, Err: err}
	}
	return nil
}
