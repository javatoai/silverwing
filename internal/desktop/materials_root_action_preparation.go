package desktop

import (
	"context"
	"errors"

	"silverwing-go/internal/application"
)

// Completed file mutations must update their UI and save identities before a
// root operation captures the immutable state its worker will migrate.
func (s *app) settleMaterialsBeforeRootAction(ctx context.Context, action application.Action, barrier *materialsFileJobBarrier, plan *markdownTaskRootMigrationPlan) (*markdownTaskRootMigrationPlan, error) {
	if barrier == nil {
		return plan, nil
	}
	window := s.win
	if window == nil || window.IsDestroyed() {
		barrier.Abandon()
		return nil, errors.New("资料窗口已关闭，目录变更已停止")
	}
	type outcome struct {
		plan *markdownTaskRootMigrationPlan
		err  error
	}
	ack := make(chan outcome, 1)
	window.Update(func() {
		if window.IsDestroyed() {
			barrier.Abandon()
			ack <- outcome{err: errors.New("资料窗口已关闭，目录变更已停止")}
			return
		}
		err := s.settleMaterialsFileBarrier(barrier)
		if err == nil {
			err = ctx.Err()
		}
		if err == nil && (action.Kind == "migration.apply" || action.Kind == "config.task-root.save") {
			plan, err = s.captureMarkdownTaskRootMigration(action)
		}
		ack <- outcome{plan: plan, err: err}
	})
	select {
	case settled := <-ack:
		return settled.plan, settled.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
