package delivery

import (
	"context"
	"errors"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

type tagGitClient interface {
	RunContext(context.Context, string, time.Duration, bool, bool, ...string) (process.Result, error)
	CommonDirectory(string) (string, error)
}

// TagProgress is an in-process observation, not another persisted state enum.
type TagProgress struct {
	Phase, Step, FailureKind string
	Operation                *domain.TagOperation
}

type UncommittedChangesError struct{ Changes []string }

func (e *UncommittedChangesError) Error() string {
	return "特性工作区存在未提交改动，请先提交或清理"
}

func tagFailureKind(err error) string {
	var dirty *UncommittedChangesError
	var conflict *mergeConflictError
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "canceled"
	case errors.As(err, &dirty):
		return "uncommitted"
	case errors.As(err, &conflict):
		return "conflict"
	default:
		return "other"
	}
}

func (s *Service) notifyTag(phase, step, failure string, operation *domain.TagOperation) {
	if s.progress != nil {
		var copy *domain.TagOperation
		if operation != nil {
			v := *operation
			v.ConflictFiles = append([]string{}, operation.ConflictFiles...)
			copy = &v
		}
		s.progress(TagProgress{Phase: phase, Step: step, FailureKind: failure, Operation: copy})
	}
}

func (s *Service) withTaskLock(directory string, run func() error) error {
	if s.lockedTask != "" && samePath(s.lockedTask, directory) {
		return run()
	}
	return s.locks.WithTask(directory, run)
}
