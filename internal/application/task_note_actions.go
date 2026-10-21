package application

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/workspace"
)

// The explicit local editor uses only the server-resolved task rules file.
// Other instruction callers retain generation/propagation and normalization.
func (a *App) localTaskRulesAction(ctx context.Context, action Action) (ActionResult, error) {
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	if action.TaskID == "" {
		return ActionResult{}, errors.New("任务说明缺少任务身份")
	}
	manifest, root, err := a.task(action)
	if err != nil {
		return ActionResult{}, err
	}
	docs := workspace.NewAgentDocumentService(a.Paths, a.Manifests)
	path := docs.TaskNotesPath(root, manifest)
	if requested := actionPath(action); requested != "" && requested != workspace.AgentRulesFileName && !strings.EqualFold(filepath.Clean(requested), path) {
		return ActionResult{}, errors.New("只能编辑当前任务的 TASK-RULES.md")
	}
	if !strings.EqualFold(filepath.Clean(textPayload(action, "taskPath")), root) || textPayload(action, "taskCreatedAt") != manifest.CreatedAt {
		return ActionResult{}, errors.New("任务身份或目录已变化，请重新打开需求说明")
	}
	if action.Kind == "instructions.read" {
		content, err := a.Documents.ReadTaskRules(root, path)
		return ActionResult{Path: content.Path, Text: content.Text, Data: content}, err
	}
	release, err := documents.AcquireWaitingLock(ctx, filepath.Join(a.Paths.Locks, "instruction-"+documents.Hash([]byte(path))+".lock"))
	if err != nil {
		return ActionResult{}, err
	}
	defer release()
	latest, latestRoot, err := a.task(action)
	if err != nil {
		return ActionResult{}, err
	}
	if !strings.EqualFold(latestRoot, root) || latest.CreatedAt != manifest.CreatedAt {
		return ActionResult{}, errors.New("任务身份或目录已变化，请重新打开需求说明")
	}
	content, err := a.Documents.SaveTaskRules(ctx, root, path, action.Text, textPayload(action, "expectedHash"), boolPayload(action, "expectedExists"))
	return ActionResult{Message: "需求说明已保存", Path: content.Path, Text: content.Text, Data: content}, err
}
