package application

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

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
		client := larkcli.Client{Runner: a.Runner, Config: config, VerifyUser: true}
		identity, err := client.Identity(ctx)
		if err != nil {
			return err
		}
		if err := identity.ValidateSelf(); err != nil {
			return err
		}

		metadata, _ := a.readNoticeMetadata(ctx, batch.task.RequirementLink, false)
		if err := ctx.Err(); err != nil {
			return err
		}
		title := strings.TrimSpace(value(metadata.Title))
		if title == "" {
			title = batch.task.FolderName
			out.Warnings = append(out.Warnings, "未取得需求标题，卡片使用任务名称")
		}
		store := releasenotice.Store{Directory: filepath.Join(a.Paths.Home, "release-notices")}
		testers := releaseNoticeTesters(metadata)
		if len(testers) == 0 {
			out.Warnings = append(out.Warnings, "未取得测试负责人姓名，卡片标题标为待确认")
		}
		session := releasenotice.Session{Scope: batch.scope, Identity: identity, Title: title, URL: batch.task.RequirementLink, Rows: batch.rows, Testers: testers}
		session.BindOwner(a.noticeOwnerKey(config, batch.task))
		if _, err := store.Prepare(session); err != nil {
			return err
		}
		// Read the current local identity again at the send boundary after
		// preparation. Online verification has already gated this invocation.
		client.VerifyUser = false
		out.Recipients, err = store.SendCLI(ctx, session.ID, client, func(ctx context.Context, saved releasenotice.Session) error {
			current, err := a.releaseNoticeBatch(ctx, action, true)
			if err != nil {
				return err
			}
			if current.scope != batch.scope || current.scope != saved.Scope {
				return errors.New("通知批次或任务已变化，请重新发起通知")
			}
			return nil
		})
		return err
	})
	return out, err
}
