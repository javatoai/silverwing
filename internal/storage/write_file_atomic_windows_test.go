//go:build windows

package storage

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func holdAtomicTarget(t *testing.T, path string) syscall.Handle {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	return handle
}

func TestWriteFileAtomicWindowsOccupiedTargetKeepsOldFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "occupied.txt")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	handle := holdAtomicTarget(t, path)
	defer syscall.CloseHandle(handle)
	started := time.Now()
	err := WriteFileAtomic(path, []byte("replacement"), 0o600)
	elapsed := time.Since(started)
	if !errors.Is(err, syscall.Errno(5)) && !errors.Is(err, syscall.Errno(32)) && !errors.Is(err, syscall.Errno(33)) {
		t.Fatalf("occupied target did not retain its native replacement error: %v", err)
	}
	if elapsed < 240*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("replacement retry escaped its bounded wait: %s", elapsed)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "original" {
		t.Fatalf("occupied original target was truncated/removed: %q %v", data, err)
	}
	assertNoAtomicWriteTemps(t, directory)
	t.Logf("occupied target retained original bytes; bounded retry %s", elapsed)
}

func TestWriteFileAtomicWindowsRetriesTransientOccupation(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "briefly-occupied.txt")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	handle := holdAtomicTarget(t, path)
	released := make(chan error, 1)
	go func() {
		time.Sleep(40 * time.Millisecond)
		released <- syscall.CloseHandle(handle)
	}()
	err := WriteFileAtomic(path, []byte("complete replacement\r\n"), 0o600)
	if closeErr := <-released; closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "complete replacement\r\n" {
		t.Fatalf("transient replacement did not publish complete bytes: %q %v", data, err)
	}
	assertNoAtomicWriteTemps(t, directory)
}
