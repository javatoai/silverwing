package storage

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

// WriteFileAtomic replaces one file using a completed temporary file in the
// same directory. Callers own directory creation, path validation and locking.
// A failed replacement leaves the existing target intact.
func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".silverwing-write-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err = temporary.Chmod(mode); err == nil {
		_, err = temporary.Write(data)
	}
	if err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	// Windows readers/scanners can briefly hold the target without delete
	// sharing. Retry only those errors; never remove or truncate the old file.
	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		err = replaceFile(temporaryPath, path)
		transient := errors.Is(err, syscall.Errno(5)) || errors.Is(err, syscall.Errno(32)) || errors.Is(err, syscall.Errno(33))
		if err == nil || runtime.GOOS != "windows" || !transient || !time.Now().Before(deadline) {
			return err
		}
		time.Sleep(5 * time.Millisecond)
	}
}
