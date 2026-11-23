package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"silverwing-go/internal/commandruntime"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/dragon"
	"silverwing-go/internal/integrations"
)

func (a *App) RecheckDragonQA(ctx context.Context, task DragonQATask, approvalID string) (DragonQAState, error) {
	return a.withDragonQA(ctx, task, func(cfg domain.AppConfig, form DragonQAState, store dragonQAStore, record dragonQARecord) (DragonQAState, error) {
		if record.Phase != DragonQAUnknown {
			return record.view(form), nil
		}
		ctx, err := a.platformIntegrationContext(ctx, cfg, "dragon")
		if err != nil {
			return record.DragonQAState, err
		}
		if err := a.checkDragonQAAccount(ctx, cfg, record.Account); err != nil {
			return record.DragonQAState, err
		}
		if record.ApprovalID != "" {
			approval, err := a.dragonQADetail(ctx, cfg, record.ApprovalID)
			if err != nil {
				return record.DragonQAState, err
			}
			if approval.ID != record.ApprovalID || !record.matchesApproval(approval) {
				return record.DragonQAState, errors.New("查询到的审批与本次提测不一致")
			}
			if err := a.checkDragonQAAccount(ctx, cfg, record.Account); err != nil {
				return record.DragonQAState, err
			}
			return a.finishDragonQA(ctx, store, record, approval)
		}

		matches, err := a.dragonQACandidates(ctx, cfg, record)
		if err != nil {
			return record.DragonQAState, err
		}
		if err := a.checkDragonQAAccount(ctx, cfg, record.Account); err != nil {
			return record.DragonQAState, err
		}
		for _, match := range matches {
			if match.ID == approvalID {
				return a.finishDragonQA(ctx, store, record, match)
			}
			record.Candidates = append(record.Candidates, DragonQACandidate{
				ApprovalID: match.ID, ApprovalURL: match.URL, CreatedAt: match.CreatedAt, Apps: match.Apps,
			})
		}
		record.Message = "未找到可核对的审批，当前仍无法确认是否已创建，请稍后再查。"
		if len(record.Candidates) > 0 {
			record.Message = "请打开候选审批，核对应用和提交时间后确认本次结果。"
		}
		return record.DragonQAState, nil
	})
}

func (a *App) checkDragonQAAccount(ctx context.Context, cfg domain.AppConfig, expected string) error {
	account, err := a.dragonQAClient.AccountIdentity(ctx, cfg)
	if err != nil {
		return err
	}
	if account != expected {
		return errors.New("请使用提交提测时的 Dragon 账号核对审批")
	}
	return nil
}

func (a *App) dragonQACandidates(ctx context.Context, cfg domain.AppConfig, record dragonQARecord) ([]dragon.Approval, error) {
	if err := commandruntime.CheckAdmission(ctx); err != nil {
		return nil, err
	}
	rows, err := a.dragonQAClient.Query(ctx, cfg, record.RequirementID)
	if err != nil {
		return nil, err
	}
	var matches []dragon.Approval
	// Permit modest local clock skew. Proximity alone does not identify a write.
	earliest := record.AttemptedAt.Add(-2 * time.Minute)
	for _, row := range rows {
		created, err := time.Parse(time.RFC3339Nano, row.CreatedAt)
		if err == nil && created.Before(earliest) {
			continue
		}
		approval, err := a.dragonQADetail(ctx, cfg, row.ID)
		if err != nil {
			return nil, err
		}
		if approval.CreatedAt == "" {
			approval.CreatedAt = row.CreatedAt
		}
		created, err = time.Parse(time.RFC3339Nano, approval.CreatedAt)
		if err == nil && !created.Before(earliest) && approval.ID == row.ID && record.matchesApproval(approval) {
			matches = append(matches, approval)
		}
	}
	return matches, nil
}

func (a *App) dragonQADetail(ctx context.Context, cfg domain.AppConfig, id string) (dragon.Approval, error) {
	if err := commandruntime.CheckAdmission(ctx); err != nil {
		return dragon.Approval{}, err
	}
	return a.dragonQAClient.Detail(ctx, cfg, id)
}

func (r dragonQARecord) matchesApproval(approval dragon.Approval) bool {
	if approval.ID == "" || approval.URL == "" || strings.TrimSpace(approval.Remark) != strings.TrimSpace(r.Remark) {
		return false
	}
	if strings.TrimSpace(approval.Meego) == r.RequirementID {
		return true
	}
	linked := domain.ParseFeishuWorkItemLink(approval.Meego)
	expected := domain.ParseFeishuWorkItemLink(r.RequirementURL)
	return linked != nil && expected != nil && linked.Space == expected.Space && linked.Kind == expected.Kind && linked.WorkItemID == expected.WorkItemID
}

func (a *App) finishDragonQA(ctx context.Context, store dragonQAStore, record dragonQARecord, approval dragon.Approval) (DragonQAState, error) {
	if approval.ID == "" || approval.URL == "" {
		record.Phase = DragonQAUnknown
		record.ApprovalID = approval.ID
		record.Message = "Dragon 未返回完整的审批回执，请先核对结果"
		return record.DragonQAState, store.save(record)
	}
	record.Phase = DragonQAApproved
	record.Message = ""
	record.ApprovalID, record.ApprovalURL = approval.ID, approval.URL
	if record.SyncComment {
		record.CommentStatus = DragonQACommentPending
	}
	if err := store.save(record); err != nil {
		record.Phase = DragonQAUnknown
		record.ApprovalURL = ""
		record.CommentStatus = ""
		record.Message = "审批回执未能保存，请先核对审批结果"
		return record.DragonQAState, err
	}
	if record.SyncComment {
		result, err := a.dragonQAPublishComment(ctx, store, record)
		var blocked *commandruntime.AdmissionError
		if errors.As(err, &blocked) {
			return result, nil
		}
		return result, err
	}
	return record.DragonQAState, nil
}

func (a *App) RetryDragonQAComment(ctx context.Context, task DragonQATask) (DragonQAState, error) {
	return a.withDragonQA(ctx, task, func(cfg domain.AppConfig, form DragonQAState, store dragonQAStore, record dragonQARecord) (DragonQAState, error) {
		if record.Phase != DragonQAApproved || !record.SyncComment || record.CommentStatus == DragonQACommentSynced {
			return record.view(form), nil
		}
		ctx, err := a.platformIntegrationContext(ctx, cfg, "dragon")
		if err != nil {
			return record.DragonQAState, err
		}
		if record.CommentStatus != DragonQACommentUnknown {
			return a.dragonQAPublishComment(ctx, store, record)
		}
		if err := commandruntime.CheckAdmission(ctx); err != nil {
			return record.DragonQAState, err
		}
		commentID, err := a.dragonQAComments.CheckDragonRequirementComment(ctx, record.RequirementURL, record.ApprovalID, record.ApprovalURL, record.CommentScope)
		if err != nil {
			return record.DragonQAState, err
		}
		if commentID == "" {
			record.CommentError = "暂未查到审批评论，当前仍无法确认是否已发布，请稍后再核对。"
		} else {
			record.CommentStatus, record.CommentError = DragonQACommentSynced, ""
		}
		return record.DragonQAState, store.save(record)
	})
}

func (a *App) dragonQAPublishComment(ctx context.Context, store dragonQAStore, record dragonQARecord) (DragonQAState, error) {
	var blocked *commandruntime.AdmissionError
	if record.CommentScope == "" {
		if err := commandruntime.CheckAdmission(ctx); err != nil {
			return record.DragonQAState, err
		}
		scope, err := a.dragonQAComments.ResolveDragonRequirementCommentScope(ctx, record.RequirementURL)
		if err != nil {
			if errors.As(err, &blocked) {
				return record.DragonQAState, err
			}
			record.CommentStatus, record.CommentError = DragonQACommentFailed, err.Error()
			return record.DragonQAState, store.save(record)
		}
		record.CommentScope = scope
	}
	if err := commandruntime.CheckAdmission(ctx); err != nil {
		return record.DragonQAState, err
	}
	previousStatus, previousError := record.CommentStatus, record.CommentError
	record.CommentStatus, record.CommentError = DragonQACommentUnknown, "评论结果待核实"
	if err := store.save(record); err != nil {
		return record.DragonQAState, err
	}
	err := commandruntime.CheckAdmission(ctx)
	var commentID string
	if err == nil {
		commentID, err = a.dragonQAComments.SyncDragonRequirementComment(ctx, record.RequirementURL, record.ApprovalID, record.ApprovalURL, record.CommentScope)
	}
	if err == nil && commentID != "" {
		record.CommentStatus, record.CommentError = DragonQACommentSynced, ""
	} else if err != nil {
		record.CommentError = err.Error()
		var uncertain *integrations.DragonRequirementCommentUncertainError
		switch {
		case errors.As(err, &uncertain):
			// The write might have happened. Recovery must only check for it.
		case errors.As(err, &blocked):
			record.CommentStatus, record.CommentError = previousStatus, previousError
			if saveErr := store.save(record); saveErr != nil {
				return record.DragonQAState, saveErr
			}
			return record.DragonQAState, err
		default:
			record.CommentStatus = DragonQACommentFailed
		}
	}
	return record.DragonQAState, store.save(record)
}
