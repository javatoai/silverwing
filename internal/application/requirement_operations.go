package application

import (
	"context"
	"encoding/json"
	"errors"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/integrations"
)

type RequirementSprint struct {
	ProjectKey  string `json:"projectKey"`
	ProjectName string `json:"projectName"`
	ID          string `json:"id"`
	Title       string `json:"title"`
	Status      string `json:"status"`
}
type RequirementList struct {
	Items             []Requirement       `json:"items"`
	Failures          []string            `json:"failures"`
	CompletedQueries  int                 `json:"completedQueries"`
	Sprints           []RequirementSprint `json:"sprints"`
	SelectedSprintKey *string             `json:"selectedSprintKey,omitempty"`
	FetchedAt         int64               `json:"fetchedAt,omitempty"`
}
type RequirementListRequest struct {
	SprintKey, ExpectedIdentity          string
	Refresh, Force                       bool
	legacy                               map[string]any
	sourceTaskID, sourcePath, sourceText string
}
type RequirementReadRequest struct {
	TaskID, URL, Path, RequirementLink, RequirementID, WriteRoot, RequirementTitle, ExpectedIdentity string
	Item                                                                                             *domain.ParticipatedWorkItem
	Force, FullRefresh                                                                               bool
	legacy                                                                                           map[string]any
	sourceText                                                                                       string
}

type RequirementReader interface {
	ReadRequirementList(context.Context, RequirementListRequest, func(RequirementListProgress)) (OperationResult[RequirementList], error)
	ReadRequirement(context.Context, RequirementReadRequest) (OperationResult[RequirementContent], error)
	ReadRequirementComments(context.Context, RequirementReadRequest) (OperationResult[RequirementComments], error)
}

var _ RequirementReader = (*App)(nil)

func copyRequirementPayload(source map[string]any) map[string]any {
	copy := make(map[string]any, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}
func RequirementListRequestFromAction(action Action) (RequirementListRequest, error) {
	request := RequirementListRequest{SprintKey: textPayload(action, "sprintKey"), ExpectedIdentity: textPayload(action, "expectedIdentity"), Refresh: action.Kind == "requirements.refresh", Force: boolPayload(action, "force"), legacy: copyRequirementPayload(action.Payload), sourceTaskID: action.TaskID, sourcePath: action.Path, sourceText: action.Text}
	if request.SprintKey == "" {
		request.SprintKey = textPayload(action, "selectedSprintKey")
	}
	return request, nil
}
func RequirementReadRequestFromAction(action Action) (RequirementReadRequest, error) {
	request := RequirementReadRequest{TaskID: action.TaskID, Path: action.Path, URL: textPayload(action, "url"), RequirementLink: textPayload(action, "requirementLink"), RequirementID: textPayload(action, "requirementId"), WriteRoot: textPayload(action, "writeRoot"), RequirementTitle: textPayload(action, "requirementTitle"), ExpectedIdentity: textPayload(action, "expectedIdentity"), Force: boolPayload(action, "force"), FullRefresh: boolPayload(action, "fullRefresh"), legacy: copyRequirementPayload(action.Payload), sourceText: action.Text}
	return request, nil
}

func (a *App) ReadRequirementList(ctx context.Context, request RequirementListRequest, progress func(RequirementListProgress)) (OperationResult[RequirementList], error) {
	if err := operationContextError(ctx); err != nil {
		return OperationResult[RequirementList]{}, err
	}
	payload := copyRequirementPayload(request.legacy)
	if request.SprintKey != "" {
		payload["sprintKey"] = request.SprintKey
	}
	if request.ExpectedIdentity != "" {
		payload["expectedIdentity"] = request.ExpectedIdentity
	}
	if request.Force {
		payload["force"] = true
	}
	kind := "requirements.list"
	if request.Refresh {
		kind = "requirements.refresh"
	}
	payload, err := a.integrationActionPayload(Action{Kind: kind, TaskID: request.sourceTaskID, Path: request.sourcePath, Text: request.sourceText, Payload: payload})
	if err != nil {
		return OperationResult[RequirementList]{}, err
	}
	var raw any
	if progress != nil {
		raw, err = a.Integrations.ReadRequirementListWithProgress(ctx, kind, payload, progress)
	} else {
		raw, err = a.Integrations.Execute(ctx, kind, payload)
	}
	if err != nil {
		return OperationResult[RequirementList]{}, requirementOperationError(err)
	}
	return requirementListResult(raw)
}

func requirementListResult(raw any) (OperationResult[RequirementList], error) {
	// The integrations adapter owns its cached domain result. Convert once at
	// this boundary and give all UI consumers the same application view.
	query, err := ResultData[struct {
		Items             []domain.ParticipatedWorkItem `json:"items"`
		Failures          []string                      `json:"failures"`
		CompletedQueries  int                           `json:"completedQueries"`
		Sprints           []RequirementSprint           `json:"sprints"`
		SelectedSprintKey *string                       `json:"selectedSprintKey,omitempty"`
		FetchedAt         int64                         `json:"fetchedAt,omitempty"`
	}](ActionResult{Data: raw})
	if err != nil {
		return OperationResult[RequirementList]{}, err
	}
	items := make([]Requirement, 0, len(query.Items))
	for _, item := range query.Items {
		participants := []string{}
		for _, person := range append(append(append([]domain.RequirementPerson{}, item.Developers...), item.QCOwners...), item.ProductManagers...) {
			participants = append(participants, person.Name)
		}
		items = append(items, Requirement{ID: item.ID, Title: item.Title, Project: item.ProjectName, ProjectKey: item.ProjectKey, Type: item.Type, TypeLabel: item.TypeLabel, URL: item.URL, Status: value(item.Status), Participants: participants, WorkItem: item})
	}
	result := OperationResult[RequirementList]{Message: "参与的工作项已读取", Data: RequirementList{Items: items, Failures: query.Failures, CompletedQueries: query.CompletedQueries, Sprints: query.Sprints, SelectedSprintKey: query.SelectedSprintKey, FetchedAt: query.FetchedAt}}
	if len(query.Failures) > 0 {
		result.Message = "工作项已读取，部分查询失败；请查看失败说明"
	}
	return result, nil
}
func (a *App) ReadRequirement(ctx context.Context, request RequirementReadRequest) (OperationResult[RequirementContent], error) {
	return readRequirementView[RequirementContent](ctx, a, "requirements.read", request)
}
func (a *App) ReadRequirementComments(ctx context.Context, request RequirementReadRequest) (OperationResult[RequirementComments], error) {
	return readRequirementView[RequirementComments](ctx, a, "requirements.comments", request)
}
func readRequirementView[T any](ctx context.Context, app *App, kind string, request RequirementReadRequest) (OperationResult[T], error) {
	if err := operationContextError(ctx); err != nil {
		return OperationResult[T]{}, err
	}
	payload := copyRequirementPayload(request.legacy)
	for key, text := range map[string]string{"url": request.URL, "requirementLink": request.RequirementLink, "requirementId": request.RequirementID, "writeRoot": request.WriteRoot, "requirementTitle": request.RequirementTitle, "expectedIdentity": request.ExpectedIdentity} {
		if text != "" {
			payload[key] = text
		}
	}
	if request.Force {
		payload["force"] = true
	}
	if request.FullRefresh {
		payload["fullRefresh"] = true
	}
	if request.Item != nil && payload["item"] == nil {
		encoded, err := json.Marshal(request.Item)
		if err != nil {
			return OperationResult[T]{}, err
		}
		var item map[string]any
		if err = json.Unmarshal(encoded, &item); err != nil {
			return OperationResult[T]{}, err
		}
		payload["item"] = item
	}
	payload, err := app.integrationActionPayload(Action{Kind: kind, TaskID: request.TaskID, Path: request.Path, Text: request.sourceText, Payload: payload})
	if err != nil {
		return OperationResult[T]{}, err
	}
	raw, err := app.Integrations.Execute(ctx, kind, payload)
	if err != nil {
		return OperationResult[T]{}, requirementOperationError(err)
	}
	return operationResultFromAction[T](ActionResult{Message: "需求操作结果已读取", Data: requirementViewData(raw)})
}
func requirementOperationError(err error) error {
	var changed *integrations.RequirementIdentityChangedError
	if errors.As(err, &changed) {
		return &RequirementIdentityChangedError{Snapshot: RequirementIdentitySnapshot{State: changed.State, Identity: changed.Identity}}
	}
	return err
}
