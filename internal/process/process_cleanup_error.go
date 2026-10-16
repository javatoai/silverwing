package process

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	processCleanupTimeout = 2 * time.Second
	processCleanupPoll    = 25 * time.Millisecond
)

// CleanupError means command descendants could not be confirmed stopped or a
// lifecycle resource could not be released. Callers must stop retries and
// recovery commands, even when Cause also contains cancellation or a timeout.
type CleanupError struct {
	Phase string
	Cause error
}

func (e *CleanupError) Error() string {
	message := "命令进程树清理未确认"
	if e.Phase != "" {
		message += "（" + e.Phase + "）"
	}
	if e.Cause != nil {
		message += "：" + e.Cause.Error()
	}
	return message
}

func (e *CleanupError) Unwrap() error { return e.Cause }

// IsCleanupFailure recognizes cleanup uncertainty through wrapped and joined
// errors. It must be checked before context cancellation and business retries.
func IsCleanupFailure(err error) bool {
	var failure *CleanupError
	return errors.As(err, &failure)
}

func cleanupPhaseError(phase string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s：%w", phase, err)
}

// Cleanup failure takes precedence without losing the original command error.
func cleanupResultError(operationErr error, cleanupErrors ...error) error {
	cleanupErr := errors.Join(cleanupErrors...)
	if cleanupErr == nil {
		return operationErr
	}
	return &CleanupError{Cause: errors.Join(cleanupErr, operationErr)}
}

// Keep exec's one Wait running as the command's reaper, but do not let an
// unconfirmed termination keep its caller blocked indefinitely.
func awaitCommandWait(ctx context.Context, done <-chan error) (error, bool) {
	select {
	case err := <-done:
		return err, true
	case <-ctx.Done():
	}
	timer := time.NewTimer(processCleanupTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err, true
	case <-timer.C:
		select {
		case err := <-done:
			return err, true
		default:
		}
		return &CleanupError{Phase: "等待命令回收", Cause: errors.Join(ctx.Err(),
			fmt.Errorf("%s 内未能确认命令等待结束", processCleanupTimeout))}, false
	}
}
