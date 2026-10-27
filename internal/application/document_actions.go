package application

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/workspace"
)

func (a *App) documentAction(ctx context.Context, action Action) (result ActionResult, resultErr error) {
	if action.Kind == "document.list" {
		return a.documentActionLocked(ctx, action)
	}
	_, taskPath, err := a.task(action)
	if err != nil {
		return ActionResult{}, err
	}
	err = workspace.NewOperationLocks(a.Paths.Locks).WithTask(taskPath, func() error {
		result, resultErr = a.documentActionLocked(ctx, action)
		return resultErr
	})
	return result, err
}

func (a *App) documentActionLocked(ctx context.Context, action Action) (result ActionResult, resultErr error) {
	if !a.materialsGitGate.TryRLock() {
		return ActionResult{}, errors.New("资料 Git 同步正在执行，请稍后重试")
	}
	defer a.materialsGitGate.RUnlock()
	root, err := a.materialRoot(action)
	if err != nil {
		return ActionResult{}, err
	}
	release, err := a.lockMaterialsGitDocumentMutation(action.Kind, root)
	if err != nil {
		return ActionResult{}, err
	}
	defer release()
	if err := a.validateMaterialsActionScope(action, root); err != nil {
		return ActionResult{}, err
	}
	action, err = a.authorizeMaterialsAction(root, action)
	if err != nil {
		return ActionResult{}, err
	}
	defer func() {
		if scope, ok, _ := a.documentAnnualScope(root); ok && !scope.Writable(actionPath(action), true) {
			if content, yes := result.Data.(documents.Content); yes {
				content.ReadOnly = true
				content.Draft = nil
				content.TextDraft = nil
				result.Data = content
			}
		}
	}()
	path := actionPath(action)
	switch action.Kind {
	case "document.search":
		query := textPayload(action, "query")
		result, err := a.Documents.SearchMaterials(ctx, root, query)
		return ActionResult{Message: "全文搜索已完成", Path: root, Data: result}, err
	case "document.list":
		if scope, ok, err := a.documentAnnualScope(root); err != nil {
			return ActionResult{}, err
		} else if ok {
			if err := scope.EnsureLocal(); err != nil {
				return ActionResult{}, err
			}
		}
		entries, err := a.Documents.List(root)
		message := "资料已刷新"
		if config, configErr := a.Config.Load(); configErr == nil {
			if catalogErr := a.refreshMaterialsAgentCatalogs(ctx, MaterialsGitScope(a.Paths.Home, config)); catalogErr != nil {
				message += "；本地说明更新失败（刷新资料可单独重试）：" + catalogErr.Error()
			}
		}
		return ActionResult{Message: message, Path: root, Data: entries}, err
	case "document.read":
		if documents.EditableMaterialText(path) {
			return a.materialTextReadResult(ctx, action, root, path)
		}
		if documents.Kind(path) == "markdown" {
			content, snapshotErr := a.Documents.MarkdownSnapshot(root, path)
			if snapshotErr == nil {
				return markdownReadResult(content), nil
			}
			preview, previewErr := a.Documents.ReadMaterialsPreview(root, path)
			if previewErr != nil {
				return ActionResult{}, snapshotErr
			}
			preview.ReadOnly = true
			return ActionResult{Message: "文档以只读方式打开：" + snapshotErr.Error(), Path: preview.Path, Text: preview.Text, Data: preview}, nil
		}
		content, err := a.Documents.ReadMaterialsPreview(root, path)
		return ActionResult{Message: "文档已读取", Path: content.Path, Text: content.Text, Data: content}, err
	case "document.save":
		if documents.EditableMaterialText(path) {
			return a.materialTextSaveResult(ctx, action, root, path)
		}
		if documents.Kind(path) == "markdown" {
			return ActionResult{}, errMarkdownMaterialReadOnly
		}
		content, err := a.Documents.Save(root, path, actionText(action), textPayload(action, "expectedHash"), boolPayload(action, "overwrite"))
		return ActionResult{Message: "文档已保存", Path: content.Path, Text: content.Text, Data: content}, err
	case "document.draft", "document.draft-read", "document.draft-discard":
		return a.documentDraftAction(ctx, action, root, path)
	case "document.create", "document.mkdir":
		if _, provided := action.Payload["name"]; provided {
			if err := documents.ValidateFileName(documentEntryName(action)); err != nil {
				return ActionResult{}, err
			}
		}
		directory, name := fileCreationLocation(root, path, documentEntryName(action))
		kind := documents.FileOperationCreateFile
		if action.Kind == "document.mkdir" {
			kind = documents.FileOperationCreateDirectory
		}
		result, err := a.Documents.ExecuteFileOperation(ctx, root, documents.FileOperationRequest{Kind: kind, Directory: directory, Name: name, Content: []byte(actionText(action))})
		return fileOperationActionResult(root, result), err
	case "document.rename":
		identity, err := a.documentDraftIdentity(action, root, path)
		if err != nil {
			return ActionResult{}, err
		}
		newPath := filepath.Join(filepath.Dir(path), documentEntryName(action))
		if (documents.Kind(path) == "markdown") != (documents.Kind(newPath) == "markdown") || documents.EditableMaterialText(path) && !documents.EditableMaterialText(newPath) {
			textDraft, draftErr := a.Documents.ReadMaterialTextDraft(ctx, identity)
			if draftErr != nil {
				return ActionResult{}, draftErr
			}
			markdownDraft, draftErr := a.Documents.ReadMarkdownDraft(ctx, identity)
			if draftErr != nil {
				return ActionResult{}, draftErr
			}
			if textDraft != nil || markdownDraft != nil {
				return ActionResult{}, errors.New("该文件仍有草稿，请先保存或丢弃草稿，再更改资料格式")
			}
		}
		result, err := a.Documents.ExecuteFileOperation(ctx, root, documents.FileOperationRequest{Kind: documents.FileOperationRename, Directory: filepath.Dir(path), RelativePath: path, Name: documentEntryName(action)})
		if err == nil && result.Status == documents.FileOperationCompleted {
			if draftErr := a.Documents.MoveMarkdownDrafts(ctx, identity, result.PreviousRelativePath, result.RelativePath); draftErr != nil {
				result.DraftError = draftErr.Error()
			}
			if draftErr := a.Documents.MoveMaterialTextDrafts(ctx, identity, result.PreviousRelativePath, result.RelativePath); draftErr != nil {
				result.DraftError = draftErr.Error()
			}
		}
		return fileOperationActionResult(root, result), err
	case "document.copy":
		selected, err := a.Documents.ResolveDeleteItems(root, []string{path})
		if err != nil {
			return ActionResult{}, err
		}
		target := textPayload(action, "destination")
		if target == "" && documentEntryName(action) != "" {
			target = filepath.Join(filepath.Dir(path), documentEntryName(action))
		}
		if target == "" {
			return ActionResult{}, errors.New("请选择复制目标")
		}
		result, err := a.Documents.ExecuteFileOperation(ctx, root, documents.FileOperationRequest{Kind: documents.FileOperationImport, Directory: filepath.Dir(target), Name: filepath.Base(target), SourcePath: selected[0]})
		return fileOperationActionResult(root, result), err
	case "document.import", "document.drop":
		paths, err := documentSelectedPaths(action, "sourcePaths", "paths")
		if err != nil {
			return ActionResult{}, err
		}
		result, err := a.Documents.ImportDroppedPaths(ctx, root, documentDirectory(action, root), paths)
		return fileBatchActionResult(root, result), err
	case "document.copy-paths":
		paths, err := documentSelectedPaths(action, "paths")
		if err != nil {
			return ActionResult{}, err
		}
		selected, err := a.Documents.ResolveCopyPaths(ctx, root, paths)
		return ActionResult{Data: selected}, err
	case "document.copy-files":
		paths, err := documentSelectedPaths(action, "paths")
		if err != nil {
			return ActionResult{}, err
		}
		selected, err := a.Documents.ResolveCopyFiles(ctx, root, paths)
		if err != nil {
			return ActionResult{}, err
		}
		return ActionResult{Message: "已复制到系统文件剪贴板", Data: selected}, documents.NewClipboardService().CopyFiles(ctx, selected)
	case "document.paste":
		payload, err := documents.NewClipboardService().Read(ctx)
		if err != nil {
			return ActionResult{}, err
		}
		if scope, ok, _ := a.documentAnnualScope(root); ok {
			for _, source := range payload.Files {
				if !scope.Writable(filepath.Join(documentDirectory(action, root), filepath.Base(source)), false) {
					return ActionResult{}, materialWriteDenied(source)
				}
			}
		}
		result, err := a.Documents.PasteClipboard(ctx, root, documentDirectory(action, root), payload)
		return fileBatchActionResult(root, result), err
	case "document.conflict-resolve":
		return a.resolveDocumentConflict(ctx, root, action)
	case "document.trash-preview", "document.trash":
		paths, err := documentSelectedPaths(action, "paths")
		if err != nil {
			return ActionResult{}, err
		}
		selected, err := a.Documents.ResolveDeleteItems(root, paths)
		if err != nil {
			return ActionResult{}, err
		}
		if action.Kind == "document.trash-preview" || !boolPayload(action, "confirmed") {
			return ActionResult{Message: "请确认移入系统回收站的项目", Data: map[string]any{"paths": selected, "requiresConfirmation": true}}, nil
		}
		results, err := documents.NewTrashService().MoveToTrash(ctx, root, selected)
		failed, cancelled := 0, 0
		var draftFailures []string
		for _, item := range results {
			if item.Error != "" {
				failed++
			}
			if item.Cancelled {
				cancelled++
			}
			if item.Error == "" && !item.Cancelled {
				identity, identityErr := a.documentDraftIdentity(action, root, item.Path)
				if identityErr == nil {
					identityErr = a.Documents.RemoveMaterialTextDrafts(ctx, identity, []string{item.Path})
				}
				if identityErr == nil {
					identityErr = a.Documents.RemoveMarkdownDrafts(ctx, identity, []string{item.Path})
				}
				if identityErr != nil {
					draftFailures = append(draftFailures, identityErr.Error())
				}
			}
		}
		message := fmt.Sprintf("回收操作已返回：完成 %d，失败 %d，取消 %d", len(results)-failed-cancelled, failed, cancelled)
		if len(draftFailures) > 0 {
			message += "；草稿缓存清理失败：" + strings.Join(draftFailures, "；")
		}
		return ActionResult{Message: message, Data: results}, err
	case "document.open", "document.reveal":
		if path == "" || filepath.Clean(path) == filepath.Clean(root) {
			if _, err := a.Documents.List(root); err != nil {
				return ActionResult{}, err
			}
			path = root
		} else {
			selected, err := a.Documents.ResolveDeleteItems(root, []string{path})
			if err != nil {
				return ActionResult{}, err
			}
			path = selected[0]
		}
		kind := "system.open"
		if action.Kind == "document.reveal" {
			kind = "system.reveal"
		}
		return a.systemAction(ctx, Action{Kind: kind, Path: path, Payload: action.Payload})
	}
	return ActionResult{}, fmt.Errorf("资料操作 %s 尚未接入", action.Kind)
}

func documentEntryName(action Action) string {
	name, _ := action.Payload["name"].(string)
	return name
}

func markdownActionResult(message string, snapshot documents.MarkdownFileSnapshot) ActionResult {
	content := documents.Content{Path: snapshot.Path, Kind: "markdown", Text: snapshot.Text, Hash: snapshot.Hash, Size: snapshot.Size, Modified: snapshot.Modified, HasUTF8BOM: snapshot.HasUTF8BOM, Newline: snapshot.Newline}
	return ActionResult{Message: message, Path: snapshot.Path, Text: snapshot.Text, Data: content}
}

func fileCreationLocation(root, path, name string) (string, string) {
	if name != "" {
		if path == "" {
			path = root
		}
		return path, name
	}
	return filepath.Dir(path), filepath.Base(path)
}

func fileOperationActionResult(root string, result documents.FileOperationResult) ActionResult {
	message := "文件操作已完成"
	if result.Status == documents.FileOperationConflicted {
		message = "目标已存在，请选择另存、替换或取消"
	} else if result.Status == documents.FileOperationCancelled {
		message = "文件操作已取消"
	}
	if result.DraftError != "" {
		message += "，草稿缓存迁移失败：" + result.DraftError
	}
	path := ""
	if result.RelativePath != "" {
		path = filepath.Join(root, filepath.FromSlash(result.RelativePath))
	}
	return ActionResult{Message: message, Path: path, Data: result}
}

func fileBatchActionResult(root string, batch documents.FileOperationBatchResult) ActionResult {
	message := fmt.Sprintf("已完成 %d 项文件操作", len(batch.Completed))
	if len(batch.Failures) > 0 {
		message += fmt.Sprintf("，失败 %d 项", len(batch.Failures))
	}
	if batch.Conflict != nil {
		message += "，目标已存在，请处理冲突后继续"
	}
	return ActionResult{Message: message, Path: root, Data: batch}
}

func (a *App) resolveDocumentConflict(ctx context.Context, root string, action Action) (ActionResult, error) {
	var conflict documents.FileOperationConflict
	if action.Payload["conflict"] == nil {
		return ActionResult{}, errors.New("文件冲突预览缺失，请重新执行操作")
	}
	if err := decodeField(action, "conflict", &conflict); err != nil {
		return ActionResult{}, err
	}
	choice := documents.FileConflictChoice(strings.ToUpper(textPayload(action, "choice")))
	var pending []documents.FileOperationRequest
	if action.Payload["pending"] != nil {
		if err := decodeField(action, "pending", &pending); err != nil {
			return ActionResult{}, err
		}
	}
	result, err := a.Documents.ResolveFileOperationConflict(ctx, root, conflict, choice, textPayload(action, "alternateName"))
	if err == nil && result.Status == documents.FileOperationCompleted && result.PreviousRelativePath != "" {
		identity, identityErr := a.documentDraftIdentity(action, root, filepath.Join(root, filepath.FromSlash(result.PreviousRelativePath)))
		if identityErr == nil {
			identityErr = a.Documents.MoveMarkdownDrafts(ctx, identity, result.PreviousRelativePath, result.RelativePath)
		}
		if identityErr == nil {
			identityErr = a.Documents.MoveMaterialTextDrafts(ctx, identity, result.PreviousRelativePath, result.RelativePath)
		}
		if identityErr != nil {
			result.DraftError = identityErr.Error()
		}
	}
	if len(pending) == 0 || result.Status == documents.FileOperationConflicted || ctx.Err() != nil {
		if result.Status == documents.FileOperationConflicted && len(pending) > 0 {
			return fileBatchActionResult(root, documents.FileOperationBatchResult{Completed: []documents.FileOperationResult{}, Conflict: result.Conflict, Pending: pending}), err
		}
		return fileOperationActionResult(root, result), err
	}
	// Like the source queue, cancel skips this conflict; an entry failure is
	// reported while later queued imports continue.
	resolutionErr := err
	batch, err := a.Documents.ExecuteFileOperations(ctx, root, pending)
	if result.Status == documents.FileOperationCompleted {
		batch.Completed = append([]documents.FileOperationResult{result}, batch.Completed...)
	}
	if resolutionErr != nil {
		batch.Failures = append([]documents.FileOperationFailure{{Request: conflict.Request, Message: resolutionErr.Error()}}, batch.Failures...)
	}
	return fileBatchActionResult(root, batch), err
}

func documentSelectedPaths(action Action, keys ...string) ([]string, error) {
	for _, key := range keys {
		if action.Payload[key] != nil {
			var paths []string
			if err := decodeField(action, key, &paths); err != nil {
				return nil, err
			}
			if len(paths) > 0 {
				return paths, nil
			}
		}
	}
	if path := actionPath(action); path != "" {
		return []string{path}, nil
	}
	return nil, errors.New("请选择文件或文件夹")
}

func documentDirectory(action Action, root string) string {
	if directory := textPayload(action, "directory"); directory != "" {
		return directory
	}
	if path := actionPath(action); path != "" {
		return path
	}
	return root
}
