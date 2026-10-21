package desktop

import (
	"context"
	"errors"
	"path/filepath"

	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	"silverwing-go/internal/editor"
)

func markdownDraftPayload(ref documentSaveRef) map[string]any {
	return map[string]any{"expectedHash": ref.hash, "draftTaskPath": ref.taskPath, "draftMaterialsRoot": ref.materialsRoot, "hasUtf8Bom": ref.hasUTF8BOM, "newline": ref.newline}
}

func (s *app) writeMarkdownDraft(ctx context.Context, key, content string, discard bool) error {
	if ref, ok := s.documentSaveRef(key); ok && ref.kind == "material-text" {
		return s.writeMaterialTextDraft(ctx, key, ref, content, discard)
	}
	ref, ok := s.documentSaveRef(key)
	if !ok {
		return errors.New("找不到 Markdown 草稿的任务身份，请重新打开文件")
	}
	if markdownReadingOnly || ref.kind == "instruction" {
		return editor.ErrDraftUnsupported
	}
	if ref.readOnly {
		return editor.ErrDraftUnsupported
	}
	if s.backend == nil {
		return errors.New("应用后端不可用，Markdown 草稿尚未缓存")
	}
	kind := "document.draft"
	if discard {
		kind = "document.draft-discard"
	}
	// Cache writes have their own serialized journal. They must remain able to
	// preserve typing while an unrelated operation holds the backend gate.
	_, err := s.backend.Dispatch(ctx, application.Action{Kind: kind, TaskID: ref.taskID, Path: ref.path, Text: content, Payload: markdownDraftPayload(ref)})
	if err != nil {
		s.invalidateInstructionEditor()
	}
	return err
}

func markdownDraftMatches(identity documents.MarkdownDraftIdentity, taskID, taskPath, root, path string) bool {
	expected, err := documents.NewMarkdownDraftIdentity(taskID, taskPath, root, path)
	return err == nil && identity.TaskID == expected.TaskID && normalizeIdentityPath(identity.TaskPath) == normalizeIdentityPath(expected.TaskPath) &&
		normalizeIdentityPath(identity.MaterialsRoot) == normalizeIdentityPath(expected.MaterialsRoot) &&
		normalizeIdentityPath(filepath.FromSlash(identity.RelativePath)) == normalizeIdentityPath(filepath.FromSlash(expected.RelativePath))
}
