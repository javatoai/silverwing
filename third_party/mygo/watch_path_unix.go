//go:build !windows

package mygo

import (
	"errors"
	"io/fs"
	"os"
)

func fileWatchOpenRoot(path string) (*os.Root, error) { return os.OpenRoot(path) }

// fileWatchInaccessible reports whether err says a file can't be looked
// at.
func fileWatchInaccessible(err error) bool { return errors.Is(err, fs.ErrPermission) }
