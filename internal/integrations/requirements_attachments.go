package integrations

import (
	"context"
	"errors"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

func (s *Service) readRequirementCommentAttachment(ctx context.Context, config domain.AppConfig, item domain.ParticipatedWorkItem,
	key string, session requirementIdentitySession, commentID, generation, fileURL string, retry bool) (any, error) {
	resource, catalog, err := s.cachedCommentImageSnapshot(key, commentID, generation, fileURL)
	if err != nil {
		return nil, err
	}
	if err := s.verifyRequirementIdentitySession(ctx, config, session.raw, session.generation); err != nil {
		return nil, err
	}
	if resource.Path != "" || resource.Error != "" && !retry {
		return resource, nil
	}
	load := func(downloadContext context.Context) ([]byte, error) {
		// The shared flight owns the permit through CLI cleanup. A cancelled
		// subscriber cannot release a slot while its download is still running.
		release, err := acquireRequirementDocumentPermit(downloadContext, s.requirementDocumentCoordinator().imageSlots)
		if err != nil {
			return nil, &requirementImageAdmissionError{err}
		}
		defer release()
		if err := s.verifyRequirementIdentitySession(downloadContext, config, session.raw, session.generation); err != nil {
			return nil, &requirementImageAdmissionError{err}
		}
		// The permit wait may outlive a refresh. The initiating comment and
		// catalog must still authorize the URL handed to the downloader.
		_, currentCatalog, err := s.cachedCommentImageSnapshot(key, commentID, generation, fileURL)
		if err != nil {
			return nil, &requirementImageAdmissionError{err}
		}
		if currentCatalog != catalog {
			return nil, &requirementImageAdmissionError{errRequirementReadSuperseded}
		}
		return s.downloadMeegleAttachment(downloadContext, config, item, fileURL)
	}
	var image []byte
	if operation := requirementImageReadOperation(ctx); operation != nil {
		byteKey := requirementImageByteKey{service: s, home: s.paths.Home, accountContext: session.context,
			account: session.raw, epoch: session.generation, catalog: catalog, item: key, url: fileURL}
		image, err = operation.read(ctx, byteKey, load)
	} else {
		image, err = load(ctx)
	}
	var admission *requirementImageAdmissionError
	if errors.As(err, &admission) {
		return nil, admission.cause
	}
	if process.IsCleanupFailure(err) {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if verifyErr := s.verifyRequirementIdentity(ctx, config, session.raw); verifyErr != nil {
		return nil, verifyErr
	}
	if err := s.verifyRequirementIdentitySession(ctx, config, session.raw, session.generation); err != nil {
		return nil, err
	}
	if err == nil {
		err = requirementCacheImageBytes(image)
	}
	message := ""
	if err != nil {
		message = redactAttachmentURL(err, fileURL).Error()
	}
	return s.saveCommentImageResource(ctx, key, commentID, generation, fileURL, image, message)
}

func (s *Service) readRequirementAttachment(ctx context.Context, config domain.AppConfig, item domain.ParticipatedWorkItem,
	key string, session requirementIdentitySession, generation, fileURL string, retry, specialAttachment bool) (any, error) {
	document, err := s.readRequirementDocument(key)
	if err != nil || document == nil {
		if err == nil {
			err = errors.New("当前需求正文尚未持久缓存")
		}
		return nil, err
	}
	if generation == "" || document.Index.Generation != generation {
		return nil, errors.New("需求正文已刷新，请重新读取图片")
	}
	resourceKnown := false
	for _, resource := range document.Resources {
		if resource.URL == fileURL {
			resourceKnown = true
			entry := document.Index.Images[fileURL]
			refreshed := entry.Generation == generation && entry.Error == ""
			if resource.Path != "" && (!retry || refreshed) {
				if err := s.verifyRequirementIdentitySession(ctx, config, session.raw, session.generation); err != nil {
					return nil, err
				}
				return RequirementResource{URL: fileURL, Path: resource.Path}, nil
			}
		}
	}
	if !resourceKnown {
		return nil, errors.New("图片不属于当前需求正文")
	}
	if err := s.verifyRequirementIdentitySession(ctx, config, session.raw, session.generation); err != nil {
		return nil, err
	}
	release, err := acquireRequirementDocumentPermit(ctx, s.requirementDocumentCoordinator().imageSlots)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := s.verifyRequirementIdentitySession(ctx, config, session.raw, session.generation); err != nil {
		return nil, err
	}
	current, err := s.readRequirementDocument(key)
	if err != nil || current == nil || current.Index.Generation != generation {
		return nil, errors.New("需求正文已刷新，请重新读取图片")
	}
	var image []byte
	if specialAttachment {
		image, err = s.downloadMeegleAttachment(ctx, config, item, fileURL)
	} else {
		image, err = downloadRequirementImageHTTP(ctx, fileURL)
	}
	if process.IsCleanupFailure(err) {
		return nil, err
	}
	// Cancellation retires this read; it must not persist a download failure.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if verifyErr := s.verifyRequirementIdentity(ctx, config, session.raw); verifyErr != nil {
		return nil, verifyErr
	}
	if err := s.verifyRequirementIdentitySession(ctx, config, session.raw, session.generation); err != nil {
		return nil, err
	}
	if err != nil {
		message := redactAttachmentURL(err, fileURL).Error()
		s.failRequirementImage(ctx, key, generation, fileURL, message)
		return RequirementResource{URL: fileURL, Error: message}, nil
	}
	if err := requirementCacheImageBytes(image); err != nil {
		s.failRequirementImage(ctx, key, generation, fileURL, err.Error())
		return RequirementResource{URL: fileURL, Error: err.Error()}, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	updated, err := s.saveRequirementImage(key, generation, fileURL, image)
	if err != nil {
		return nil, err
	}
	for _, resource := range updated.Resources {
		if resource.URL == fileURL {
			return resource, nil
		}
	}
	return nil, errors.New("图片缓存保存后未能回读")
}

// Serialize each document/URL pair before checking its latest cached image.
// Waiting and active image operations are also cancelled by session retirement.
func (c *requirementDocumentCoordinator) image(ctx context.Context, key string,
	read func(context.Context) (any, error)) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, context.Canceled
	}
	child, cancel := context.WithCancel(ctx)
	op := &requirementDocumentOperation{ctx: child, cancel: cancel, done: make(chan struct{})}
	c.active[op] = struct{}{}
	lock := c.images[key]
	if lock == nil {
		lock = &requirementImageLock{token: make(chan struct{}, 1)}
		lock.token <- struct{}{}
		c.images[key] = lock
	}
	lock.users++
	c.mu.Unlock()
	defer func() {
		cancel()
		c.mu.Lock()
		defer c.mu.Unlock()
		lock.users--
		if lock.users == 0 {
			delete(c.images, key)
		}
		delete(c.active, op)
		close(op.done)
	}()
	select {
	case <-child.Done():
		return nil, child.Err()
	case <-lock.token:
		defer func() { lock.token <- struct{}{} }()
	}
	if err := child.Err(); err != nil {
		return nil, err
	}
	value, err := read(child)
	if process.IsCleanupFailure(err) {
		return nil, err
	}
	if child.Err() != nil {
		return nil, child.Err()
	}
	return value, err
}
