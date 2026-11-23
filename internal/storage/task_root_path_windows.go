//go:build windows

package storage

import (
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// Resolve through the open handle so junctions and short names identify the
// same directory as their targets.
func canonicalExistingTaskRootPath(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	buffer := make([]uint16, 512)
	for {
		count, err := windows.GetFinalPathNameByHandle(windows.Handle(file.Fd()), &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", err
		}
		if count >= uint32(len(buffer)) {
			buffer = make([]uint16, count+1)
			continue
		}
		resolved := windows.UTF16ToString(buffer[:count])
		if strings.HasPrefix(resolved, `\\?\UNC\`) {
			return `\\` + resolved[len(`\\?\UNC\`):], nil
		}
		return strings.TrimPrefix(resolved, `\\?\`), nil
	}
}
