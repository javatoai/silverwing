package application

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"

	"silverwing-go/internal/documents"
)

var errMarkdownMaterialReadOnly = errors.New("Markdown 资料仅支持只读阅读，不能保存或修改草稿")

func (a *App) documentDraftIdentity(action Action, root, path string) (documents.MarkdownDraftIdentity, error) {
	_, taskPath, err := a.task(action)
	if err != nil {
		return documents.MarkdownDraftIdentity{}, err
	}
	identity, err := documents.NewMarkdownDraftIdentity(action.TaskID, taskPath, root, path)
	if err != nil {
		return identity, err
	}
	for field, current := range map[string]string{"draftTaskPath": identity.TaskPath, "draftMaterialsRoot": identity.MaterialsRoot, "desktopRenameTaskPath": identity.TaskPath, "desktopRenameMaterialsRoot": identity.MaterialsRoot} {
		if captured := textPayload(action, field); captured != "" && !sameDraftPath(captured, current) {
			return identity, errors.New("Markdown 草稿所属任务或资料目录已改变，请重新打开文件")
		}
	}
	return identity, nil
}

func sameDraftPath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func markdownReadResult(snapshot documents.MarkdownFileSnapshot) ActionResult {
	result := markdownActionResult("文档已读取", snapshot)
	content := result.Data.(documents.Content)
	content.ReadOnly = true
	result.Data = content
	return result
}

func markdownDraftErrorResult(result ActionResult, message string) ActionResult {
	content := result.Data.(documents.Content)
	content.DraftError = message
	result.Data, result.Message = content, message
	return result
}

func (a *App) documentDraftAction(ctx context.Context, action Action, root, path string) (ActionResult, error) {
	if documents.EditableMaterialText(path) {
		return a.materialTextDraftAction(ctx, action, root, path)
	}
	if documents.Kind(path) != "markdown" {
		return ActionResult{}, errors.New("资料草稿仅支持 Markdown 原文")
	}
	if action.Kind != "document.draft-read" {
		return ActionResult{}, errMarkdownMaterialReadOnly
	}
	identity, err := a.documentDraftIdentity(action, root, path)
	if err != nil {
		return ActionResult{}, err
	}
	result := ActionResult{Path: filepath.Join(identity.MaterialsRoot, filepath.FromSlash(identity.RelativePath))}
	draft, err := a.Documents.ReadMarkdownDraft(ctx, identity)
	result.Message, result.Data = "草稿已读取", draft
	if draft != nil {
		result.Text = draft.Content
	}
	return result, err
}
