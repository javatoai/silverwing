//go:build !windows

package storage

import "path/filepath"

func canonicalExistingTaskRootPath(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
