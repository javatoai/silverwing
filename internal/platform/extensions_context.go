package platform

import (
	"context"
	"io"
)

// Keep extension state writes serialized while allowing a canceled request to
// stop waiting immediately. Recheck after acquisition when both select cases
// become ready at once.
var extensionOperations = make(chan struct{}, 1)

func acquireExtensionOperation(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case extensionOperations <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-extensionOperations
			return nil, err
		}
		return func() { <-extensionOperations }, nil
	}
}

func copySkillDirectoryWithContext(ctx context.Context, source, target string) error {
	if err := validateSkillTreeWithContext(ctx, source); err != nil {
		return err
	}
	return walkCopyWithContext(ctx, source, target, true)
}

type extensionContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r extensionContextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
