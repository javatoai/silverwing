package application

import (
	"context"
	"errors"
	"silverwing-go/internal/domain"
)

// Optional desktop capability; the regular action/result boundary is unchanged.
type RequirementCommentPublishBackend interface {
	DispatchRequirementComment(context.Context, Action, func(domain.CommentPublishProgress)) (ActionResult, error)
}

func (a *App) DispatchRequirementComment(ctx context.Context, action Action, progress func(domain.CommentPublishProgress)) (ActionResult, error) {
	if action.Kind != "requirements.comments/create" && action.Kind != "requirements.comments/update" {
		return ActionResult{}, errors.New("评论进度入口仅用于新增或编辑评论")
	}
	payload, err := a.integrationActionPayload(action)
	if err != nil {
		return ActionResult{}, err
	}
	var data any
	if action.Kind == "requirements.comments/update" {
		data, err = a.Integrations.UpdateRequirementComment(ctx, payload, progress)
	} else {
		data, err = a.Integrations.PublishRequirementComment(ctx, payload, progress)
	}
	if err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Message: "评论发布结果已读取", Data: data}, nil
}
