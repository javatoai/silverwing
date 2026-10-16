package application

import (
	"context"
	"path/filepath"

	"silverwing-go/internal/documents"
)

func materialTextActionResult(message string, snapshot documents.MaterialTextSnapshot) ActionResult {
	content := documents.Content{Path: snapshot.Path, Kind: documents.Kind(snapshot.Path), Text: snapshot.Text, Hash: snapshot.Hash, Size: snapshot.Size, Modified: snapshot.Modified, HasUTF8BOM: snapshot.HasUTF8BOM, Newline: snapshot.Newline}
	return ActionResult{Message: message, Path: snapshot.Path, Text: snapshot.Text, Data: content}
}

func (a *App) materialTextReadResult(ctx context.Context, action Action, root, path string) (ActionResult, error) {
	snapshot, err := a.Documents.MaterialTextSnapshot(root, path)
	if err != nil {
		preview, previewErr := a.Documents.ReadMaterialsPreview(root, path)
		if previewErr != nil {
			return ActionResult{}, err
		}
		preview.ReadOnly = true
		return ActionResult{Message: "文档以只读方式打开：" + err.Error(), Path: preview.Path, Text: preview.Text, Data: preview}, nil
	}
	result := materialTextActionResult("文档已读取", snapshot)
	identity, err := a.documentDraftIdentity(action, root, snapshot.Path)
	var draft *documents.MaterialTextDraft
	if err == nil {
		draft, err = a.Documents.ReadMaterialTextDraft(ctx, identity)
	}
	if err != nil {
		return markdownDraftErrorResult(result, "草稿缓存读取失败，磁盘原文仍可读取："+err.Error()), nil
	}
	content := result.Data.(documents.Content)
	content.TextDraft = draft
	result.Data = content
	return result, nil
}

func (a *App) materialTextSaveResult(ctx context.Context, action Action, root, path string) (ActionResult, error) {
	identity, err := a.documentDraftIdentity(action, root, path)
	if err != nil {
		return ActionResult{}, err
	}
	current, err := a.Documents.MaterialTextSnapshot(root, path)
	if err != nil {
		return ActionResult{}, err
	}
	baseline, hasBOM := textPayload(action, "expectedHash"), current.HasUTF8BOM
	if _, supplied := action.Payload["hasUtf8Bom"]; supplied {
		hasBOM = boolPayload(action, "hasUtf8Bom")
	}
	if boolPayload(action, "overwrite") {
		baseline = current.Hash
	}
	snapshot, err := a.Documents.SaveMaterialText(ctx, root, path, actionText(action), baseline, hasBOM)
	result := materialTextActionResult("文档已保存", snapshot)
	if err == nil {
		if draftErr := a.Documents.CompleteMaterialTextDraftSave(ctx, identity, actionText(action), textPayload(action, "expectedHash"), snapshot.Hash); draftErr != nil {
			result = markdownDraftErrorResult(result, "文档已保存，但草稿缓存清理失败："+draftErr.Error())
		}
	}
	return result, err
}

func (a *App) materialTextDraftAction(ctx context.Context, action Action, root, path string) (ActionResult, error) {
	identity, err := a.documentDraftIdentity(action, root, path)
	if err != nil {
		return ActionResult{}, err
	}
	result := ActionResult{Path: filepath.Join(identity.MaterialsRoot, filepath.FromSlash(identity.RelativePath))}
	switch action.Kind {
	case "document.draft-read":
		draft, err := a.Documents.ReadMaterialTextDraft(ctx, identity)
		result.Message, result.Data = "草稿已读取", draft
		if draft != nil {
			result.Text = draft.Content
		}
		return result, err
	case "document.draft-discard":
		result.Message = "草稿已丢弃"
		return result, a.Documents.DiscardMaterialTextDraft(ctx, identity)
	default:
		result.Message = "草稿已缓存"
		return result, a.Documents.WriteMaterialTextDraft(ctx, documents.MaterialTextDraft{Identity: identity, Content: actionText(action), BaselineHash: textPayload(action, "expectedHash"), HasUTF8BOM: boolPayload(action, "hasUtf8Bom"), Newline: textPayload(action, "newline")})
	}
}
