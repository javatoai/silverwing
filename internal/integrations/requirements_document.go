package integrations

import (
	"context"
	"errors"
	"sync"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

type requirementDocumentOperation struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

type requirementBodyRequest struct {
	op                           *requirementDocumentOperation
	forced, finished, superseded bool
	consumers                    int
	result                       RequirementReadResult
	err                          error
}

type requirementImageLock struct {
	token chan struct{}
	users int
}

// Readers, naming and preparation share Source's two body/four image bounds.
// Body subscribers own cancellation; the last subscriber retires its request.
type requirementDocumentCoordinator struct {
	mu                    sync.Mutex
	bodies                map[string]*requirementBodyRequest
	images                map[string]*requirementImageLock
	active                map[*requirementDocumentOperation]struct{}
	bodySlots, imageSlots chan struct{}
	closed                bool
}

var errRequirementBodySuperseded = errors.New("需求正文已有更新请求")

func (s *Service) requirementDocumentCoordinator() *requirementDocumentCoordinator {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.requirementDocuments == nil {
		s.requirementDocuments = &requirementDocumentCoordinator{bodies: make(map[string]*requirementBodyRequest),
			images: make(map[string]*requirementImageLock), active: make(map[*requirementDocumentOperation]struct{}),
			bodySlots: make(chan struct{}, 2), imageSlots: make(chan struct{}, 4)}
	}
	return s.requirementDocuments
}

func (c *requirementDocumentCoordinator) read(ctx context.Context, key string, force bool,
	fetch func(context.Context, *requirementBodyRequest) (RequirementReadResult, error)) (RequirementReadResult, error) {
	for {
		if err := ctx.Err(); err != nil {
			return RequirementReadResult{}, err
		}
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return RequirementReadResult{}, errors.New("需求文档服务正在关闭")
		}
		request := c.bodies[key]
		if request == nil || request.finished || request.op.ctx.Err() != nil || force && !request.forced {
			if request != nil && !request.finished && request.op.ctx.Err() == nil {
				request.superseded = true
			}
			shared, cancel := context.WithCancel(context.WithoutCancel(ctx))
			op := &requirementDocumentOperation{ctx: shared, cancel: cancel, done: make(chan struct{})}
			request = &requirementBodyRequest{op: op, forced: force}
			c.bodies[key] = request
			c.active[op] = struct{}{}
			go c.runBody(key, request, fetch)
		}
		request.consumers++
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			if c.releaseBody(request) {
				<-request.op.done
				if process.IsCleanupFailure(request.err) {
					return RequirementReadResult{}, request.err
				}
			}
			return RequirementReadResult{}, ctx.Err()
		case <-request.op.done:
			c.mu.Lock()
			superseded := request.superseded
			c.mu.Unlock()
			c.releaseBody(request)
			if process.IsCleanupFailure(request.err) {
				return RequirementReadResult{}, request.err
			}
			if err := ctx.Err(); err != nil {
				return RequirementReadResult{}, err
			}
			if superseded && request.err == nil {
				force = false
				continue
			}
			return request.result, request.err
		}
	}
}

func (c *requirementDocumentCoordinator) releaseBody(request *requirementBodyRequest) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	request.consumers--
	if request.consumers == 0 && !request.finished {
		request.op.cancel()
	}
	return request.consumers == 0
}

func (c *requirementDocumentCoordinator) runBody(key string, request *requirementBodyRequest,
	fetch func(context.Context, *requirementBodyRequest) (RequirementReadResult, error)) {
	defer func() {
		request.op.cancel()
		c.mu.Lock()
		defer c.mu.Unlock()
		request.finished = true
		if c.bodies[key] == request {
			delete(c.bodies, key)
		}
		delete(c.active, request.op)
		close(request.op.done)
	}()
	request.result, request.err = fetch(request.op.ctx, request)
	if err := request.op.ctx.Err(); err != nil && !process.IsCleanupFailure(request.err) {
		request.err = err
	}
}

func (c *requirementDocumentCoordinator) commitBody(key string, request *requirementBodyRequest,
	write func() (*requirementDocument, error)) (*requirementDocument, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := request.op.ctx.Err(); err != nil {
		return nil, err
	}
	if c.bodies[key] != request {
		return nil, errRequirementBodySuperseded
	}
	return write()
}

func acquireRequirementDocumentPermit(ctx context.Context, slots chan struct{}) (func(), error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case slots <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-slots
			return nil, err
		}
		return func() { <-slots }, nil
	}
}

func (s *Service) fetchRequirementDocument(ctx context.Context, config domain.AppConfig, item domain.ParticipatedWorkItem,
	identity string, identityGeneration uint64, key string, request *requirementBodyRequest, fullRefresh bool) (RequirementReadResult, error) {
	var saved *requirementDocument
	cacheError := ""
	if identity != "" {
		var err error
		saved, err = s.readRequirementDocument(key)
		if err != nil {
			saved = nil
			cacheError = err.Error()
		}
		if !request.forced && saved != nil {
			if err := s.verifyRequirementIdentitySession(ctx, config, identity, identityGeneration); err != nil {
				return RequirementReadResult{}, err
			}
			return requirementReadFromDocument(saved, "CACHE", false, "", ""), nil
		}
	}
	c := s.requirementDocumentCoordinator()
	release, err := acquireRequirementDocumentPermit(ctx, c.bodySlots)
	if err != nil {
		return RequirementReadResult{}, err
	}
	root, fetchErr := s.readWorkItem(ctx, config, item.ProjectKey, item.ID)
	var body string
	if fetchErr == nil {
		body, fetchErr = s.requirementBody(ctx, config, root, item.ProjectKey, item.Type, item.ID)
	}
	release()
	if process.IsCleanupFailure(fetchErr) {
		return RequirementReadResult{}, fetchErr
	}
	if err := ctx.Err(); err != nil {
		return RequirementReadResult{}, err
	}
	if fetchErr != nil {
		if saved == nil {
			return RequirementReadResult{}, fetchErr
		}
		if err := s.verifyRequirementIdentitySession(ctx, config, identity, identityGeneration); err != nil {
			return RequirementReadResult{}, err
		}
		return requirementReadFromDocument(saved, "CACHE", true, fetchErr.Error(), cacheError), nil
	}
	if title := requirementTitle(root); title != "" {
		item.Title = title
	}
	fetchedAt := requirementUnixMillis()
	if identity == "" {
		return requirementReadFromRemote(item, body, fetchedAt, "无法确认当前账户，正文尚未持久缓存"), nil
	}
	if err := s.verifyRequirementIdentitySession(ctx, config, identity, identityGeneration); err != nil {
		return RequirementReadResult{}, err
	}
	if fullRefresh {
		if err := s.verifyRequirementRefresh(ctx, config, identity); err != nil {
			return RequirementReadResult{}, err
		}
	}
	document, err := c.commitBody(key, request, func() (*requirementDocument, error) {
		if fullRefresh {
			if err := s.verifyRequirementReadConfig(config); err != nil {
				return nil, err
			}
		}
		return s.writeRequirementDocument(key, item, body, fetchedAt)
	})
	if errors.Is(err, errRequirementBodySuperseded) {
		return RequirementReadResult{}, nil
	}
	if err != nil {
		if ctx.Err() != nil {
			return RequirementReadResult{}, ctx.Err()
		}
		return requirementReadFromRemote(item, body, fetchedAt, "全文缓存失败："+err.Error()), nil
	}
	return requirementReadFromDocument(document, "REMOTE", false, "", ""), nil
}

func expectedRequirementDocumentIdentity(payload map[string]any, identity string, loggedOut bool, probeErr error) error {
	value, present := payload["expectedIdentity"]
	if !present {
		return nil
	}
	expected, ok := value.(string)
	if !ok || !validOpaqueRequirementIdentity(expected) {
		return errors.New("需求读取预期账户身份无效")
	}
	if loggedOut {
		return &RequirementIdentityChangedError{State: requirementIdentityLoggedOut}
	}
	if probeErr != nil || identity == "" {
		return errors.Join(errors.New("无法确认当前 Meegle 账户，请重试；本地需求已保留"), probeErr)
	}
	if !requirementIdentityMatches(identity, expected) {
		return &RequirementIdentityChangedError{State: requirementIdentityAuthenticated, Identity: opaqueRequirementIdentity(identity)}
	}
	return nil
}

func (c *requirementDocumentCoordinator) beginRetirement(closeService bool) []*requirementDocumentOperation {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = c.closed || closeService
	operations := make([]*requirementDocumentOperation, 0, len(c.active))
	for op := range c.active {
		op.cancel()
		operations = append(operations, op)
	}
	c.bodies = make(map[string]*requirementBodyRequest)
	return operations
}

func (c *requirementDocumentCoordinator) retire(ctx context.Context, closeService bool) error {
	operations := c.beginRetirement(closeService)
	for _, op := range operations {
		select {
		case <-op.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// InvalidateRequirementDocuments cancels the old scope synchronously. It does
// not wait on CLI shutdown and cannot retire requests registered afterwards.
func (s *Service) InvalidateRequirementDocuments() {
	s.requirementDocumentCoordinator().beginRetirement(false)
}

func (s *Service) CancelRequirementDocuments(ctx context.Context) error {
	return s.requirementDocumentCoordinator().retire(ctx, false)
}

func (s *Service) CloseRequirementDocuments(ctx context.Context) error {
	documentsErr := s.requirementDocumentCoordinator().retire(ctx, true)
	identityErr := s.CloseRequirementIdentityRequests(ctx)
	return errors.Join(documentsErr, identityErr)
}
