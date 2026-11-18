//go:build darwin && !ios

package documents

import (
	"context"

	"silverwing-go/internal/systemtrash"
)

type darwinTrashBackend struct{}

func newPlatformTrashBackend() trashBackend { return darwinTrashBackend{} }

func (darwinTrashBackend) moveToTrash(ctx context.Context, path string) error {
	_, err := systemtrash.Move(ctx, path)
	return err
}
