package desktop

import (
	"context"
	"errors"
	"sync"

	"silverwing-go/internal/application"
)

const requirementCommentImageConcurrency = 2

type requirementCommentImageScope struct {
	identity                                       requirementListIdentity
	config, taskPath, taskCreated, root, cachePath string
	epoch                                          uint64
	fetched                                        int64
}

type requirementCommentImageBatch struct {
	scope     requirementCommentImageScope
	ctx       context.Context
	cancel    context.CancelFunc
	pending   map[string]bool // UI thread only; every subscriber retains its token
	images    *nativeImagePool
	owner     *requirementCommentImageBatches
	closeOnce sync.Once
	done      chan struct{}
}

type requirementCommentImageBatches struct {
	mu       sync.Mutex
	running  map[*requirementCommentImageBatch]struct{}
	failures []error
	closed   bool
}

func (s *app) ensureRequirementCommentImageBatch(reader *requirementReaderState) *requirementCommentImageBatch {
	scope := requirementCommentImageScope{identity: s.currentRequirementListIdentity(), config: s.settingsUnit.state.configJSON, epoch: reader.epoch}
	if reader.comments != nil {
		scope.cachePath, scope.fetched = reader.comments.CachePath, reader.comments.FetchedAt
	}
	if task := s.taskUnit.taskByID(reader.taskID); reader.taskID != "" {
		scope.taskPath, scope.taskCreated, scope.root = task.path, task.createdAt, task.materialsPath
	}
	if reader.commentImageBatch != nil && reader.commentImageBatch.scope == scope {
		return reader.commentImageBatch
	}
	if reader.commentImageBatch != nil {
		s.retireRequirementCommentImages(reader)
	}
	base, cancel := context.WithCancel(context.Background())
	ctx := application.WithRequirementImageReadOperation(base)
	if s.requirementUnit.state.commentImageBatches == nil {
		s.requirementUnit.state.commentImageBatches = &requirementCommentImageBatches{running: make(map[*requirementCommentImageBatch]struct{})}
	}
	owner := s.requirementUnit.state.commentImageBatches
	batch := &requirementCommentImageBatch{scope: scope, ctx: ctx, cancel: cancel, pending: make(map[string]bool), images: s.newMarkdownImagePool(), owner: owner, done: make(chan struct{})}
	owner.mu.Lock()
	owner.running[batch] = struct{}{}
	closed := owner.closed
	owner.mu.Unlock()
	if closed {
		batch.close()
	}
	reader.commentImageBatch = batch
	for _, state := range reader.commentMarkdown {
		if state.imagePoolOwned {
			state.imagePool.close()
		}
		state.imagePool, state.imagePoolOwned = batch.images, false
	}
	return batch
}

func (batch *requirementCommentImageBatch) close() {
	if batch == nil {
		return
	}
	batch.closeOnce.Do(func() {
		batch.cancel()
		batch.images.close()
		go func() {
			err := application.CloseRequirementImageReadOperation(context.Background(), batch.ctx)
			err = errors.Join(err, batch.images.wait(context.Background()))
			batch.owner.mu.Lock()
			delete(batch.owner.running, batch)
			if err != nil && len(batch.owner.failures) < 16 {
				batch.owner.failures = append(batch.owner.failures, err)
			}
			close(batch.done)
			batch.owner.mu.Unlock()
		}()
	})
}

func finishRequirementCommentImage(reader *requirementReaderState, key string) {
	if batch := reader.commentImageBatch; batch != nil {
		delete(batch.pending, key)
		reader.commentResourceLoading = ""
		for pending := range batch.pending {
			reader.commentResourceLoading = pending
			break
		}
	} else {
		reader.commentResourceLoading = ""
	}
}

func commentImageRequestKind(key string) string { return "requirements.attachment:comment:" + key }

func (s *requirementController) closeRequirementCommentImageBatches(ctx context.Context) error {
	owner := s.state.commentImageBatches
	if owner == nil {
		return nil
	}
	owner.mu.Lock()
	owner.closed = true
	batches := make([]*requirementCommentImageBatch, 0, len(owner.running))
	for batch := range owner.running {
		batches = append(batches, batch)
	}
	owner.mu.Unlock()
	for _, batch := range batches {
		batch.close()
	}
	for _, batch := range batches {
		select {
		case <-batch.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	owner.mu.Lock()
	err := errors.Join(owner.failures...)
	owner.mu.Unlock()
	return err
}
