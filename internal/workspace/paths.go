package workspace

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func statPath(path string) (os.FileInfo, error) { return os.Stat(path) }

func canonicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		return filepath.Clean(resolved), nil
	}
	return filepath.Clean(absolute), nil
}

func samePath(a, b string) bool {
	left, err := canonicalPath(a)
	if err != nil {
		left = filepath.Clean(a)
	}
	right, err := canonicalPath(b)
	if err != nil {
		right = filepath.Clean(b)
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}
