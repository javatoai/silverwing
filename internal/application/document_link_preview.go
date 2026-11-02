package application

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
)

// documentLinkPreview is deliberately separate from task-material document
// reads. Explicit local links grant only a bounded read, never a write or draft.
func (a *App) documentLinkPreview(ctx context.Context, action Action) (ActionResult, error) {
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	path := actionPath(action)
	if path == "" || !filepath.IsAbs(path) || strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, "//") {
		return ActionResult{}, errors.New("文件预览需要本地绝对路径")
	}
	path = filepath.Clean(path)
	readRoot := filepath.Dir(path)
	if action.TaskID != "" {
		root, err := a.materialRoot(action)
		if err != nil {
			return ActionResult{}, err
		}
		if err := a.validateMaterialsActionScope(action, root); err != nil {
			return ActionResult{}, err
		}
		if relative, err := filepath.Rel(root, path); err == nil && !filepath.IsAbs(relative) && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			// Retain the validated materials root so a configured TaskRoot alias
			// uses the same canonical operation scope as ordinary document reads.
			readRoot = root
		}
	}
	content, err := a.Documents.ReadMaterialsPreview(readRoot, path)
	if err != nil {
		return ActionResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	content.ReadOnly = true
	content.Draft = nil
	content.TextDraft = nil
	content.MarkdownEditContext = ""
	return ActionResult{Path: content.Path, Text: content.Text, Data: content, Message: "链接文件只读预览"}, nil
}
