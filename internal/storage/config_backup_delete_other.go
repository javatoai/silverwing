//go:build !windows && (!darwin || ios)

package storage

import (
	"context"
	"os"
)

func removeListedConfigBackup(context.Context, *os.Root, string, os.FileInfo) error {
	return errConfigBackupDeleteUnsupported
}
