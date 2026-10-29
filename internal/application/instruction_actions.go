package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func (a *App) instructionAction(ctx context.Context, action Action) (actionResult ActionResult, actionErr error) {
	if boolPayload(action, "localTaskRules") && (action.Kind == "instructions.read" || action.Kind == "instructions.save") {
		return a.localTaskRulesAction(ctx, action)
	}
	templates := documents.NewTemplateStore(a.Paths)
	switch action.Kind {
	case "instructions.templates", "instructions.templates.list":
		items, err := templates.List()
		return ActionResult{Message: "模板已读取", Data: items}, err
	case "instructions.templates.save":
		content := actionText(action)
		if content == "" {
			content = textPayload(action, "content")
		}
		items, err := templates.Save(ctx, textPayload(action, "id"), textPayload(action, "name"), content)
		return ActionResult{Message: "模板已保存", Data: items}, err
	case "instructions.templates.remove":
		items, err := templates.Remove(ctx, textPayload(action, "id"))
		return ActionResult{Message: "模板已删除", Data: items}, err
	}
	config, err := a.Config.Load()
	if err != nil {
		return ActionResult{}, err
	}
	docs := workspace.NewAgentDocumentService(a.Paths, a.Manifests)
	path, root, readOnly, err := a.instructionPath(action, docs)
	if err != nil {
		return ActionResult{}, err
	}
	switch action.Kind {
	case "instructions.read":
		content, err := a.Documents.Read(root, path)
		if err == nil && action.TaskID != "" {
			manifest, taskPath, e := a.task(action)
			if e != nil {
				return ActionResult{}, e
			}
			if strings.EqualFold(path, docs.TaskNotesPath(taskPath, manifest)) {
				content.Text, err = docs.TaskNotesFromDocument(manifest, content.Text)
			}
		}
		content.ReadOnly = content.ReadOnly || readOnly
		return ActionResult{Message: "说明已读取", Path: content.Path, Text: content.Text, Data: content}, err
	case "instructions.draft":
		return ActionResult{Message: "草稿已缓存"}, a.Documents.CacheDraft(path, actionText(action))
	case "instructions.save":
		if readOnly {
			return ActionResult{}, errors.New("此文件由系统生成；请编辑任务规则")
		}
		release, err := documents.AcquireWaitingLock(ctx, filepath.Join(a.Paths.Locks, "instruction-"+documents.Hash([]byte(path))+".lock"))
		if err != nil {
			return ActionResult{}, err
		}
		defer release()
		saveText := actionText(action)
		if action.TaskID != "" {
			manifest, taskPath, e := a.task(action)
			if e != nil {
				return ActionResult{}, e
			}
			if strings.EqualFold(path, docs.TaskNotesPath(taskPath, manifest)) {
				current, e := a.Documents.Read(root, path)
				if e != nil {
					return ActionResult{}, e
				}
				saveText, e = docs.ReplaceTaskNotesDocument(manifest, current.Text, saveText)
				if e != nil {
					return ActionResult{}, e
				}
			}
		}
		content, err := a.Documents.Save(root, path, saveText, textPayload(action, "expectedHash"), boolPayload(action, "overwrite"))
		if err != nil {
			return ActionResult{}, err
		}
		if action.TaskID != "" {
			manifest, taskPath, e := a.task(action)
			if e != nil {
				return ActionResult{}, e
			}
			if strings.EqualFold(path, docs.TaskNotesPath(taskPath, manifest)) {
				service := a.taskService(config)
				defer func() { appendMaterialsCatalogWarnings(&actionResult, service.Documents) }()
				if e = service.SaveTaskNotes(config, taskPath, actionText(action)); e != nil {
					return ActionResult{}, e
				}
				// Task rules are normalized during saving. Return the final disk
				// hash for the editor's next conflict check.
				content, e = a.Documents.Read(root, path)
				if e != nil {
					return ActionResult{}, e
				}
				content.Text = actionText(action)
			}
		}
		scope := textPayload(action, "scope")
		if scope == "global" || scope == "group" {
			instructionScope := workspace.AgentInstructionScope{Kind: workspace.AgentInstructionGlobalScope}
			if scope == "group" {
				instructionScope.Kind = workspace.AgentInstructionGroupScope
				instructionScope.GroupID = textPayload(action, "groupId")
				if instructionScope.GroupID == "" {
					instructionScope.GroupID = domain.DefaultGroupID
				}
			}
			propagation := workspace.NewAgentDocumentPropagationService(a.Manifests, docs, workspace.NewOperationLocks(a.Paths.Locks)).Propagate(config, instructionScope)
			if len(propagation.Failures) > 0 {
				return ActionResult{Data: propagation}, fmt.Errorf("共享规则已保存，但 %d 个任务的规则传播失败：%v", len(propagation.Failures), propagation.Failures)
			}
		}
		return ActionResult{Message: "说明已保存", Path: path, Text: content.Text, Data: content}, nil
	}
	return ActionResult{}, fmt.Errorf("未知说明操作：%s", action.Kind)
}

func (a *App) instructionPath(action Action, docs *workspace.AgentDocumentService) (path, root string, readOnly bool, err error) {
	scope := textPayload(action, "scope")
	switch scope {
	case "global":
		path, err = docs.EnsureGlobalFile()
		return path, filepath.Dir(path), false, err
	case "group":
		groupID := textPayload(action, "groupId")
		if groupID == "" {
			groupID = domain.DefaultGroupID
		}
		path, err = docs.EnsureGroupFile(groupID)
		return path, filepath.Dir(path), false, err
	}
	manifest, taskPath, err := a.task(action)
	if err != nil {
		return "", "", false, err
	}
	requested := actionPath(action)
	if requested == "" {
		switch scope {
		case "context":
			requested = workspace.AgentContextFileName
		case "handoff":
			requested = workspace.HandoffFileName
		default:
			requested = workspace.AgentRulesFileName
		}
	}
	notes := docs.TaskNotesPath(taskPath, manifest)
	allowed := map[string]string{
		workspace.AgentRouterFileName:  filepath.Join(taskPath, workspace.AgentRouterFileName),
		workspace.AgentContextFileName: filepath.Join(taskPath, workspace.AgentWorkspaceDir, workspace.AgentDirectoryName, workspace.AgentContextFileName),
		workspace.AgentRulesFileName:   notes,
		workspace.HandoffFileName:      filepath.Join(taskPath, workspace.AgentWorkspaceDir, workspace.HandoffFileName),
	}
	if target, ok := allowed[requested]; ok {
		path = target
	} else {
		absolute, e := filepath.Abs(requested)
		if e != nil {
			return "", "", false, e
		}
		for _, target := range allowed {
			if strings.EqualFold(filepath.Clean(absolute), filepath.Clean(target)) {
				path = target
				break
			}
		}
	}
	if path == "" {
		return "", "", false, errors.New("请选择任务中已知的说明文件")
	}
	readOnly = filepath.Base(path) == workspace.AgentContextFileName || filepath.Base(path) == workspace.AgentRouterFileName
	if _, err = os.Stat(path); err != nil {
		return "", "", readOnly, err
	}
	return path, taskPath, readOnly, nil
}
