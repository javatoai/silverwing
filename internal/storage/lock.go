package storage

import "errors"

// ErrLockBusy means another process currently owns the requested file lock.
var ErrLockBusy = errors.New("silverwing resource is locked")
