package application

import (
	"context"
	"errors"
	"strconv"

	"silverwing-go/internal/commandruntime"
	"silverwing-go/internal/production"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

func (a *App) genbuApprovalSubmissionPath(id int64) string {
	return a.genbuConfigSubmissionPath("approve-" + strconv.FormatInt(id, 10))
}

func (a *App) approveTaskServiceConfigChange(ctx context.Context, action Action, scope genbuConfigPreviewScope, client taskServiceGenbuClient) (ActionResult, error) {
	if scope.Environment != "uat" || scope.Context.Archived {
		return ActionResult{}, errors.New("仅可在 UAT 服务列表或未归档任务中同意配置审批")
	}
	id, err := strconv.ParseInt(textPayload(action, "approvalId"), 10, 64)
	revision := textPayload(action, "revision")
	if err != nil || id <= 0 || !validGenbuConfigDigest(revision) {
		return ActionResult{}, errors.New("审批详情已变化，请重新查看配置差异")
	}

	var result ActionResult
	submit := func() error {
		cfg, service, release, err := a.beginTaskServiceQuery(ctx, action, scope)
		if err != nil {
			return err
		}
		defer release()
		path := a.genbuApprovalSubmissionPath(id)
		if _, err := a.recordGenbuConfigSubmission(path); err != nil {
			if errors.Is(err, production.ErrGenbuConfigSubmitUnknown) {
				return production.ErrGenbuApprovalUnknown
			}
			return err
		}
		request := commandruntime.WithAdmission(ctx, func(requestCtx context.Context) error {
			if err := commandruntime.CheckAdmission(ctx); err != nil {
				return err
			}
			_, _, err := a.taskServiceIdentity(requestCtx, action, scope)
			return err
		})
		detail, err := client.ApproveUATConfigChange(request, cfg, scope.Environment, service.GenbuServiceName, id, revision)
		if detail.ID == id && detail.Environment == scope.Environment {
			result.Data = detail
		} else if err == nil {
			err = production.ErrGenbuApprovalUnknown
		}
		if errors.Is(err, production.ErrGenbuApprovalUnknown) {
			return err
		}
		if removeErr := storage.RemoveFileIfPresent(path); removeErr != nil {
			result.Message = "本地审批回执记录清理失败"
			if err != nil {
				return errors.Join(err, removeErr)
			}
		}
		return err
	}
	if scope.Runtime.Home != "" {
		err = submit()
	} else {
		err = workspace.NewOperationLocks(a.Paths.Locks).WithTask(scope.Context.TaskPath, submit)
	}
	return result, err
}
