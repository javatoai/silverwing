package application

import (
	"context"
	"fmt"
	"path/filepath"

	"silverwing-go/internal/larkcli"
	"silverwing-go/internal/releasenotice"
)

type CLITagNoticeResult struct {
	BatchID    string                            `json:"batchId"`
	Recipients []releasenotice.RecipientDelivery `json:"recipients"`
	Warnings   []string                          `json:"warnings,omitempty"`
}

func (a *App) CLITagNotify(ctx context.Context, taskID, batchID string) (CLITagNoticeResult, error) {
	out := CLITagNoticeResult{BatchID: batchID, Recipients: []releasenotice.RecipientDelivery{}}
	taskID, _, err := a.findCLITagHistory(ctx, CLITagHistoryQuery{Task: taskID, Batch: batchID})
	if err != nil {
		return out, err
	}
	task, scope, err := a.cliTagTask(ctx, taskID)
	if err != nil {
		return out, err
	}
	err = task.WithLock(func() error {
		config, err := a.validateTagJobScope(ctx, scope)
		if err != nil {
			return err
		}
		action := Action{TaskID: taskID, Payload: map[string]any{
			"groupId": batchID, "noticeConfigIdentity": ReleaseNoticeConfigIdentity(config),
			"tagHistoryHome": scope.Home, "tagHistoryTaskRoot": scope.TaskRoot,
			"tagHistoryTaskPath": scope.TaskPath, "tagHistoryTaskCreatedAt": scope.TaskCreatedAt,
		}}
		batch, err := a.readReleaseNoticeBatch(ctx, action, true, true)
		if err != nil {
			return err
		}
		for _, operation := range batch.operations {
			push := task.PushStatus(ctx, operation)
			if push.State != "PUSHED" {
				return fmt.Errorf("无法确认 %s 的 Tag %s 已推送（%s） %s", operation.ServiceName, value(operation.Tag), push.State, push.Error)
			}
		}
		client := larkcli.Client{Runner: a.Runner, Config: config, AsUser: true}
		session, preparation, err := a.prepareUserTagNotice(ctx, batch, client)
		if err != nil {
			return err
		}
		if err = a.checkUserTagNotice(ctx, action, session, preparation); err != nil {
			return err
		}
		store := releasenotice.Store{Directory: filepath.Join(a.Paths.Home, "release-notices")}
		if session.Warning != "" {
			out.Warnings = append(out.Warnings, session.Warning)
		}
		if session.PreparationStatus != "" {
			out.Warnings = append(out.Warnings, session.PreparationStatus)
		}
		prepared, err := store.Prepare(session)
		if err != nil {
			return err
		}
		sent, sendErr := store.SendTag(ctx, session.ID, prepared.RetryChoice(), client, a.userTagNoticeChecks(action))
		out.Recipients = sent.Deliveries()
		previous := prepared.Deliveries()
		for i := range out.Recipients {
			if i < len(previous) {
				out.Recipients[i].Skipped = previous[i].State == "sent" && previous[i].MessageID == out.Recipients[i].MessageID
			}
		}
		return sendErr
	})
	return out, err
}
