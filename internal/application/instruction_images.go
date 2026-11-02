package application

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func (a *App) importInstructionImages(ctx context.Context, action Action) (ActionResult, error) {
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	for _, field := range []string{"root", "directory", "targetDirectory"} {
		if _, supplied := action.Payload[field]; supplied {
			return ActionResult{}, errors.New("说明图片目录由应用确定，请重新打开当前说明")
		}
	}
	path, root, err := a.instructionImageTarget(action)
	if err != nil {
		return ActionResult{}, err
	}
	encoded, err := json.Marshal(action.Payload["clipboard"])
	if err != nil {
		return ActionResult{}, err
	}
	var payload documents.ClipboardPayload
	if err := json.Unmarshal(encoded, &payload); err != nil {
		return ActionResult{}, err
	}
	release, err := documents.AcquireWaitingLock(ctx, filepath.Join(a.Paths.Locks, "instruction-"+documents.Hash([]byte(path))+".lock"))
	if err != nil {
		return ActionResult{}, err
	}
	defer release()
	if err := a.validateInstructionImageTarget(action, path, root); err != nil {
		return ActionResult{}, err
	}
	if a.Documents == nil {
		return ActionResult{}, errors.New("文档服务不可用")
	}
	result, err := a.Documents.ImportInstructionImages(ctx, root, path, payload)
	if err != nil {
		return ActionResult{}, err
	}
	if err := a.validateInstructionImageTarget(action, path, root); err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Path: path, Data: result}, nil
}

func (a *App) validateInstructionImageTarget(action Action, expectedPath, expectedRoot string) error {
	path, root, err := a.instructionImageTarget(action)
	if err != nil {
		return err
	}
	if !sameDraftPath(path, expectedPath) || !sameDraftPath(root, expectedRoot) {
		return errors.New("说明文档身份已变化，请重新打开文件")
	}
	return nil
}

func (a *App) instructionImageTarget(action Action) (path, root string, err error) {
	switch textPayload(action, "scope") {
	case "global":
		if action.TaskID != "" || boolPayload(action, "localTaskRules") {
			return "", "", errors.New("全局说明图片不能使用任务身份")
		}
		path, root = a.Paths.GlobalAgents, a.Paths.Home
	case "group":
		if action.TaskID != "" || boolPayload(action, "localTaskRules") {
			return "", "", errors.New("服务组说明图片不能使用任务身份")
		}
		groupID := textPayload(action, "groupId")
		if groupID == "" {
			groupID = domain.DefaultGroupID
		}
		path, err = a.Paths.GroupAgents(groupID)
		if err != nil {
			return "", "", err
		}
		root = a.Paths.Home
	case "task":
		if action.TaskID == "" || !boolPayload(action, "localTaskRules") {
			return "", "", errors.New("任务说明图片缺少当前任务的本地编辑身份")
		}
		manifest, taskPath, loadErr := a.task(action)
		if loadErr != nil {
			return "", "", loadErr
		}
		if textPayload(action, "taskPath") == "" || !sameDraftPath(textPayload(action, "taskPath"), taskPath) || textPayload(action, "taskCreatedAt") == "" || textPayload(action, "taskCreatedAt") != manifest.CreatedAt {
			return "", "", errors.New("任务身份或目录已变化，请重新打开需求说明")
		}
		root = taskPath
		path = filepath.Join(taskPath, workspace.AgentWorkspaceDir, workspace.AgentDirectoryName, workspace.AgentRulesFileName)
	default:
		return "", "", errors.New("当前说明不支持导入图片")
	}
	if path == "" || root == "" {
		return "", "", errors.New("说明文档目录尚未配置")
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	if requested := actionPath(action); requested != "" && requested != filepath.Base(path) && !sameDraftPath(requested, path) {
		return "", "", errors.New("只能向当前说明文档导入图片")
	}
	return path, root, nil
}
