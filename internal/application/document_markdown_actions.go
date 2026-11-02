package application

import (
	"context"
	"errors"
	"path/filepath"

	"silverwing-go/internal/documents"
)

// The entry marker routes only the task materials editor. The current task,
// configured materials root and writable subdirectory remain authoritative.
const taskMaterialsMarkdownEditContext = "task-materials"

func (a *App) materialMarkdownWritable(action Action, root, path string) (bool, error) {
	if textPayload(action, "markdownEditContext") != taskMaterialsMarkdownEditContext || action.ServiceID != "" || action.ModuleID != "" {
		return false, nil
	}
	scope, present, err := a.documentAnnualScope(root)
	if err != nil {
		return false, err
	}
	return present && scope.Writable(path, false), nil
}

func (a *App) materialMarkdownWriteIdentity(action Action, root, path string) (documents.MarkdownDraftIdentity, error) {
	writable, err := a.materialMarkdownWritable(action, root, path)
	if err != nil {
		return documents.MarkdownDraftIdentity{}, err
	}
	if !writable {
		return documents.MarkdownDraftIdentity{}, errMarkdownMaterialReadOnly
	}
	manifest, taskPath, err := a.task(action)
	if err != nil {
		return documents.MarkdownDraftIdentity{}, err
	}
	if !sameTaskContextPath(textPayload(action, "draftTaskPath"), taskPath) ||
		!sameTaskContextPath(textPayload(action, "draftMaterialsRoot"), root) ||
		textPayload(action, "desktopRenameTaskCreatedAt") == "" ||
		textPayload(action, "desktopRenameTaskCreatedAt") != manifest.CreatedAt {
		return documents.MarkdownDraftIdentity{}, errors.New("任务或资料身份已变化，请重新打开 Markdown 文件")
	}
	return a.documentDraftIdentity(action, root, path)
}

func materialMarkdownActionResult(message string, snapshot documents.MarkdownFileSnapshot) ActionResult {
	result := markdownActionResult(message, snapshot)
	content := result.Data.(documents.Content)
	content.MarkdownEditContext = taskMaterialsMarkdownEditContext
	result.Data = content
	return result
}

func (a *App) materialMarkdownReadResult(ctx context.Context, action Action, root string, snapshot documents.MarkdownFileSnapshot) (ActionResult, error) {
	writable, err := a.materialMarkdownWritable(action, root, snapshot.Path)
	if err != nil {
		return ActionResult{}, err
	}
	if !writable {
		return markdownReadResult(snapshot), nil
	}
	result := materialMarkdownActionResult("文档已读取", snapshot)
	identity, err := a.documentDraftIdentity(action, root, snapshot.Path)
	var draft *documents.MarkdownDraft
	if err == nil {
		draft, err = a.Documents.ReadMarkdownDraft(ctx, identity)
	}
	if err != nil {
		return markdownDraftErrorResult(result, "草稿缓存读取失败，磁盘原文仍可读取："+err.Error()), nil
	}
	content := result.Data.(documents.Content)
	content.Draft = draft
	result.Data = content
	return result, nil
}

func (a *App) materialMarkdownSaveResult(ctx context.Context, action Action, root, path string) (ActionResult, error) {
	identity, err := a.materialMarkdownWriteIdentity(action, root, path)
	if err != nil {
		return ActionResult{}, err
	}
	baseline := textPayload(action, "expectedHash")
	if boolPayload(action, "overwrite") {
		current, err := a.Documents.MarkdownSnapshot(root, path)
		if err != nil {
			return ActionResult{}, err
		}
		baseline = current.Hash
	}
	snapshot, err := a.Documents.SaveMarkdown(ctx, root, path, actionText(action), baseline)
	result := materialMarkdownActionResult("文档已保存", snapshot)
	if err == nil {
		if draftErr := a.Documents.CompleteMarkdownDraftSave(ctx, identity, actionText(action), textPayload(action, "expectedHash"), snapshot.Hash); draftErr != nil {
			result = markdownDraftErrorResult(result, "文档已保存，但草稿缓存清理失败："+draftErr.Error())
		}
	}
	return result, err
}

func (a *App) materialMarkdownDraftAction(ctx context.Context, action Action, root, path string) (ActionResult, error) {
	identity, err := a.materialMarkdownWriteIdentity(action, root, path)
	if err != nil {
		return ActionResult{}, err
	}
	// A cache write must not recreate the identity of a deleted file or accept a
	// symlink/file-type substitution that the editor itself could not open.
	if _, err := a.Documents.MarkdownSnapshot(root, path); err != nil {
		return ActionResult{}, err
	}
	result := ActionResult{Path: filepath.Join(identity.MaterialsRoot, filepath.FromSlash(identity.RelativePath))}
	switch action.Kind {
	case "document.draft-read":
		draft, err := a.Documents.ReadMarkdownDraft(ctx, identity)
		result.Message, result.Data = "草稿已读取", draft
		if draft != nil {
			result.Text = draft.Content
		}
		return result, err
	case "document.draft-discard":
		result.Message = "草稿已丢弃"
		return result, a.Documents.DiscardMarkdownDraft(ctx, identity)
	default:
		result.Message = "草稿已缓存"
		return result, a.Documents.WriteMarkdownDraft(ctx, documents.MarkdownDraft{Identity: identity, Content: actionText(action), BaselineHash: textPayload(action, "expectedHash"), HasUTF8BOM: boolPayload(action, "hasUtf8Bom"), Newline: textPayload(action, "newline")})
	}
}
