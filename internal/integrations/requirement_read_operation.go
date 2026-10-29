package integrations

import (
	"context"
	"sync"

	"silverwing-go/internal/domain"
)

type requirementReadOperationKey struct{}

type requirementReadOperation struct {
	mu      sync.Mutex
	owner   context.Context
	service *Service
	session requirementIdentitySession
	ready   bool
}

// WithRequirementReadOperation scopes adjacent reads in one explicit refresh.
// Only a successful, forced post-read identity probe can supply the next read's
// starting identity. It carries no identity between operations or by elapsed TTL.
func WithRequirementReadOperation(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, requirementReadOperationKey{}, &requirementReadOperation{owner: ctx})
}

func requirementOperation(ctx context.Context) *requirementReadOperation {
	operation, _ := ctx.Value(requirementReadOperationKey{}).(*requirementReadOperation)
	return operation
}

func (s *Service) requirementIdentityAtReadStart(ctx context.Context, config domain.AppConfig, expected string, force bool) (requirementIdentitySession, bool, error) {
	if operation := requirementOperation(ctx); operation != nil {
		operation.mu.Lock()
		session, service, ready := operation.session, operation.service, operation.ready
		operation.ready = false
		operation.mu.Unlock()
		if err := operation.owner.Err(); err != nil {
			return requirementIdentitySession{}, false, err
		}
		if ready {
			if service != s || session.context != s.requirementIdentityContextWithContext(ctx, config) {
				return requirementIdentitySession{}, false, errRequirementReadSuperseded
			}
			if err := s.verifyRequirementReadConfig(config); err != nil {
				return requirementIdentitySession{}, false, err
			}
			if err := s.verifyRequirementIdentitySession(ctx, config, session.raw, session.generation); err != nil {
				return requirementIdentitySession{}, false, err
			}
			return checkRequirementIdentitySession(session, expected)
		}
	}
	return s.requirementIdentityFor(ctx, config, expected, force, s.requirementCacheIdentity)
}

func (s *Service) publishRequirementReadIdentity(ctx context.Context, session requirementIdentitySession) {
	if operation := requirementOperation(ctx); operation != nil {
		operation.mu.Lock()
		defer operation.mu.Unlock()
		if ctx.Err() == nil && operation.owner.Err() == nil {
			operation.service, operation.session, operation.ready = s, session, true
		}
	}
}

func finishRequirementReadOperation(ctx context.Context, data any, err error) {
	operation := requirementOperation(ctx)
	if operation == nil {
		return
	}
	failed := err != nil || ctx.Err() != nil
	switch result := data.(type) {
	case RequirementReadResult:
		failed = failed || result.Stale || result.RefreshError != "" || result.CacheError != ""
	case RequirementMetadataReadResult:
		failed = failed || result.Stale || result.RefreshError != "" || result.CacheError != "" || result.EstimateError != ""
	case RequirementCommentsReadResult:
		failed = failed || result.Stale || result.RefreshError != "" || result.CacheError != ""
	default:
		failed = true
	}
	if failed {
		operation.mu.Lock()
		operation.ready = false
		operation.mu.Unlock()
	}
}
