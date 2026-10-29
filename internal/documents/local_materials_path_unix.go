//go:build !windows

package documents

import (
	"os"
	"path/filepath"
)

func canonicalLocalTaskPath(path string) (string, error) { return filepath.EvalSymlinks(path) }

func openLocalMaterialsDirectory(path string) (*os.File, error) { return os.Open(path) }
