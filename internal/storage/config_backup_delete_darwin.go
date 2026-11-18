//go:build darwin && !ios

package storage

import (
	"context"
	"os"

	"golang.org/x/sys/unix"
	"silverwing-go/internal/systemtrash"
)

func removeListedConfigBackup(ctx context.Context, root *os.Root, name string, expected os.FileInfo) error {
	if err := trashListedConfigBackup(ctx, root, name, expected, renameConfigBackupExclusive, systemtrash.Move); err != nil {
		return &configBackupTrashError{err: err}
	}
	return nil
}

func renameConfigBackupExclusive(fromDir *os.File, from string, toDir *os.File, to string) error {
	return unix.RenameatxNp(int(fromDir.Fd()), from, int(toDir.Fd()), to, unix.RENAME_EXCL)
}
