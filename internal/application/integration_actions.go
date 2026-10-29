package application

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"silverwing-go/internal/integrations"
)

func (a *App) integrationAction(ctx context.Context, action Action) (ActionResult, error) {
	return a.integrationActionWithRequirementProgress(ctx, action, nil)
}

func (a *App) integrationActionWithRequirementProgress(ctx context.Context, action Action, progress func(RequirementListProgress)) (ActionResult, error) {
	switch action.Kind {
	case "requirements.list", "requirements.refresh":
		request, err := RequirementListRequestFromAction(action)
		if err != nil {
			return ActionResult{}, err
		}
		result, err := a.ReadRequirementList(ctx, request, progress)
		return result.ToActionResult(), err
	case "requirements.read":
		request, err := RequirementReadRequestFromAction(action)
		if err != nil {
			return ActionResult{}, err
		}
		result, err := a.ReadRequirement(ctx, request)
		return result.ToActionResult(), err
	case "requirements.comments":
		request, err := RequirementReadRequestFromAction(action)
		if err != nil {
			return ActionResult{}, err
		}
		result, err := a.ReadRequirementComments(ctx, request)
		return result.ToActionResult(), err
	}
	payload, err := a.integrationActionPayload(action)
	if err != nil {
		return ActionResult{}, err
	}
	if action.Kind == "requirements.copy-document" || action.Kind == "requirements.copy-comments" {
		return a.copyRequirementCache(ctx, action, payload)
	}
	var data any
	if action.Kind == "requirements.candidates" {
		data, err = a.Integrations.ReadRequirementCandidates(ctx, payload)

	} else {
		data, err = a.Integrations.Execute(ctx, action.Kind, payload)
	}
	if err != nil {
		var changed *integrations.RequirementIdentityChangedError
		if errors.As(err, &changed) {
			return ActionResult{}, &RequirementIdentityChangedError{Snapshot: RequirementIdentitySnapshot{State: changed.State, Identity: changed.Identity}}
		}
		return ActionResult{}, err
	}
	result := ActionResult{Message: "需求操作结果已读取", Data: requirementViewData(data)}
	if action.Kind == "requirements.candidates" {
		query, ok := data.(integrations.RequirementCandidatesResult)
		if !ok {
			return ActionResult{}, errors.New("需求候选查询返回了未知结果")
		}
		candidates := make([]RequirementCandidate, 0, len(query.Candidates))
		for _, item := range query.Candidates {
			candidates = append(candidates, RequirementCandidate{Title: item.Title, URL: item.URL, SourceID: item.SourceID})
		}
		failures := make([]RequirementCandidateFailure, 0, len(query.Failures))
		for _, failure := range query.Failures {
			failures = append(failures, RequirementCandidateFailure{
				Stage: failure.Stage, ProjectKey: failure.ProjectKey, SprintID: failure.SprintID,
				WorkItemType: failure.WorkItemType, Message: failure.Message,
			})
		}
		result.Data = map[string]any{"candidates": candidates, "failures": failures, "completedQueries": query.CompletedQueries}
		result.Message = "需求候选已读取"
		if len(failures) > 0 {
			result.Message = "需求候选已读取，部分查询失败；请查看失败说明"
		}
	}

	return result, nil
}

type RequirementCandidate struct {
	Title    string `json:"title"`
	URL      string `json:"url"`
	SourceID string `json:"sourceId"`
}

type RequirementCandidateFailure struct {
	Stage        string `json:"stage"`
	ProjectKey   string `json:"projectKey,omitempty"`
	SprintID     string `json:"sprintId,omitempty"`
	WorkItemType string `json:"workItemType,omitempty"`
	Message      string `json:"message"`
}

func (a *App) integrationActionPayload(action Action) (map[string]any, error) {
	payload := map[string]any{}
	for key, value := range action.Payload {
		payload[key] = value
	}
	if action.TaskID != "" {
		manifest, taskPath, err := a.task(action)
		if err != nil {
			return nil, err
		}
		payload["taskPath"] = taskPath
		if _, ok := payload["requirementLink"]; !ok {
			payload["requirementLink"] = manifest.RequirementLink
		}
		if action.Kind == "requirements.metadata" {
			if _, ok := payload["url"]; !ok {
				payload["url"] = manifest.RequirementLink
			}
		}
		if _, ok := payload["requirementId"]; !ok && manifest.RequirementID != nil {
			payload["requirementId"] = *manifest.RequirementID
		}
		if manifest.RequirementMaterials.WriteRoot != nil {
			writeRoot := *manifest.RequirementMaterials.WriteRoot
			if _, ok := payload["writeRoot"]; !ok {
				payload["writeRoot"] = writeRoot
			}
			if _, ok := payload["requirementTitle"]; !ok {
				payload["requirementTitle"] = filepath.Base(filepath.Dir(writeRoot))
			}
		}
	}
	if action.Path != "" {
		payload["path"] = action.Path
		if strings.HasPrefix(action.Path, "https://") || strings.HasPrefix(action.Path, "http://") {
			if _, exists := payload["url"]; !exists {
				payload["url"] = action.Path
			}
		}
	}
	if action.Text != "" {
		payload["text"] = action.Text
		if action.Kind == "requirements.comment" {
			payload["content"] = action.Text
		}
	}
	return payload, nil
}
