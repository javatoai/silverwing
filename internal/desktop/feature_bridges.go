package desktop

import (
	"context"
)

// These view bridges keep rendering and native window code on app while the
// owning feature controller applies state and worker lifecycle rules.

func (s *app) stopTagGenbuAutoProbe() {
	if s == nil {
		return
	}
	s.releaseUnit.stopTagGenbuAutoProbe()
}

func (s *app) closeTagGenbuAutoProbe(ctx context.Context) error {
	if s == nil {
		var zero error
		return zero
	}
	return s.releaseUnit.closeTagGenbuAutoProbe(ctx)
}

func (s *app) cancelTaskCreationAINaming() {
	if s == nil {
		return
	}
	s.taskUnit.cancelTaskCreationAINaming()
}

func (s *app) closeTaskCreationAINaming() {
	if s == nil {
		return
	}
	s.taskUnit.closeTaskCreationAINaming()
}

func (s *app) closeTaskCreationRequirements() {
	if s == nil {
		return
	}
	s.taskUnit.closeTaskCreationRequirements()
}

func (s *app) closeTaskCreationPreflights(ctx context.Context) error {
	if s == nil {
		var zero error
		return zero
	}
	return s.taskUnit.closeTaskCreationPreflights(ctx)
}
