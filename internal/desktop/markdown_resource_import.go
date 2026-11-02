package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	"silverwing-go/internal/editor"
	markdownui "silverwing-go/internal/ui/markdown"
	"silverwing-go/internal/workspace"
)

func (s *app) markdownClipboardContent(ctx context.Context) (markdownui.ClipboardContent, error) {
	var result markdownui.ClipboardContent
	if backend, ok := s.backend.(application.RichClipboardBackend); ok {
		content, err := backend.ReadRichClipboard()
		if err != nil && !errors.Is(err, application.ErrRichClipboardUnsupported) {
			return result, err
		}
		result.Text, result.HTML = content.Text, content.HTML
	}
	payload, err := documents.NewClipboardService().Read(ctx)
	if err != nil {
		return result, err
	}
	result.Files, result.ImagePNG = payload.Files, payload.ImagePNG
	return result, nil
}

func (s *app) importMarkdownResources(state *markdownEditorState, ctx context.Context, request markdownui.ImportRequest, complete func(markdownui.ImportResult, error)) {
	if state == nil || state.componentEditor == nil || !s.markdownEditorIsCurrent(state) || !s.materialsMarkdownWritable(state.path) || s.win == nil || s.win.IsDestroyed() {
		complete(markdownui.ImportResult{}, errors.New("当前文档不可导入图片"))
		return
	}
	scope, valid := s.materialsFileScope(state.taskID)
	if !valid {
		complete(markdownui.ImportResult{}, errors.New("资料目录身份已变化"))
		return
	}
	taskID := state.taskID
	owner, identity, path, win, backend := state.componentEditor, state.liveIdentity, state.path, s.win, s.backend
	documentRelative := path
	if filepath.IsAbs(documentRelative) {
		var err error
		documentRelative, err = filepath.Rel(scope.root, documentRelative)
		if err != nil {
			complete(markdownui.ImportResult{}, err)
			return
		}
	}
	folder := strings.TrimSuffix(filepath.Base(documentRelative), filepath.Ext(documentRelative)) + ".assets"
	directory := filepath.Join(filepath.Dir(documentRelative), folder)
	if !filepath.IsAbs(directory) {
		directory = filepath.Join(scope.root, directory)
	}
	absoluteDocument := path
	if !filepath.IsAbs(absoluteDocument) {
		absoluteDocument = filepath.Join(scope.root, absoluteDocument)
	}
	payload := map[string]any{"draftTaskPath": scope.taskPath, "draftMaterialsRoot": scope.root, "desktopRenameTaskCreatedAt": scope.createdAt, "materialsHome": scope.home}
	if config, ok := s.materialsGitConfig(); ok {
		payload["materialsConfigScope"] = application.MaterialsGitScope(scope.home, config)
	}
	action := func(kind string) application.Action {
		p := map[string]any{}
		for k, v := range payload {
			p[k] = v
		}
		return application.Action{Kind: kind, TaskID: taskID, Payload: p}
	}
	go func() {
		result := markdownui.ImportResult{}
		var err error
		files := append([]string(nil), request.Files...)
		if request.Pick {
			files, err = mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Title: "插入本地图片", DefaultPath: scope.root, Multiple: true, Filters: []mygo.FileFilter{{Name: "图片", Extensions: []string{"png", "jpg", "jpeg", "gif", "webp"}}}})
		}
		if err == nil && (len(files) > 0 || len(request.ImagePNG) > 0) {
			mkdir := action("document.mkdir")
			mkdir.Path = filepath.Dir(directory)
			mkdir.Payload["name"] = folder
			var created application.ActionResult
			created, err = s.dispatchMaterialsBackend(ctx, backend, scope.taskPath, mkdir)
			if err == nil {
				var entry documents.FileOperationResult
				encoded, _ := json.Marshal(created.Data)
				err = json.Unmarshal(encoded, &entry)
				if err == nil && entry.Status == documents.FileOperationConflicted {
					list := action("document.list")
					list.Path = directory
					_, err = s.dispatchMaterialsBackend(ctx, backend, scope.taskPath, list)
				}
			}
			inputs := make([]documents.ClipboardPayload, 0, len(files)+1)
			for _, file := range files {
				inputs = append(inputs, documents.ClipboardPayload{Files: []string{file}})
			}
			if len(files) == 0 && len(request.ImagePNG) > 0 {
				inputs = append(inputs, documents.ClipboardPayload{ImagePNG: request.ImagePNG})
			}
			for _, input := range inputs {
				if err != nil {
					break
				}
				paste := action("document.paste")
				paste.Payload["directory"] = directory
				paste.Payload["capturedClipboard"] = true
				paste.Payload["clipboard"] = input
				var imported application.ActionResult
				imported, err = s.dispatchMaterialsBackend(ctx, backend, scope.taskPath, paste)
				if err != nil {
					break
				}
				var batch documents.FileOperationBatchResult
				encoded, _ := json.Marshal(imported.Data)
				err = json.Unmarshal(encoded, &batch)
				if err != nil {
					break
				}
				if len(batch.Failures) > 0 || batch.Conflict != nil || len(batch.Pending) > 0 || len(batch.Completed) == 0 {
					err = errors.New("部分图片未能导入，请查看资料目录")
					break
				}
				for _, entry := range batch.Completed {
					var target string
					target, err = markdownResourceReference(absoluteDocument, imported.Path, entry.RelativePath)
					if err != nil {
						break
					}
					extension := strings.ToLower(filepath.Ext(entry.RelativePath))
					image := extension == ".png" || extension == ".jpg" || extension == ".jpeg" || extension == ".gif" || extension == ".webp"
					result.Resources = append(result.Resources, markdownui.ImportedResource{Target: target, Text: strings.TrimSuffix(filepath.Base(entry.RelativePath), filepath.Ext(entry.RelativePath)), Image: image})
				}
			}
		}
		if win.IsDestroyed() {
			return
		}
		win.Update(func() {
			if state.componentEditor != owner || state.closed || state.detached || state.readOnly || state.liveIdentity != identity || state.path != path || !s.markdownEditorIsCurrent(state) || !s.materialsMarkdownWritable(path) || owner.Snapshot().Version != request.Version {
				complete(markdownui.ImportResult{}, fmt.Errorf("%w：图片导入结果未写入已变化的文档", markdownui.ErrStale))
				return
			}
			complete(result, err)
		})
	}()
}

func markdownResourceReference(documentPath, root, relativePath string) (string, error) {
	relative, err := filepath.Rel(filepath.Dir(documentPath), filepath.Join(root, filepath.FromSlash(relativePath)))
	if err != nil {
		return "", err
	}
	return (&url.URL{Path: filepath.ToSlash(relative)}).String(), nil
}

type instructionMarkdownImportOwner struct {
	binding  *markdownEditorBinding
	editor   *markdownui.EditorState
	document *editor.Document
	identity string
	path     string
	key      string
	ref      documentSaveRef
	request  markdownui.ImportRequest
}

func instructionImageImportRefAllowed(ref documentSaveRef) bool {
	if ref.kind != "instruction" || ref.readOnly || ref.path == "" {
		return false
	}
	switch ref.scope {
	case "global", "group":
		return ref.taskID == "" && !ref.localTaskRules
	case "task":
		return ref.localTaskRules && ref.taskID != "" && ref.taskPath != "" && ref.createdAt != "" &&
			sameInstructionPath(ref.path, filepath.Join(ref.taskPath, workspace.AgentWorkspaceDir, workspace.AgentDirectoryName, workspace.AgentRulesFileName))
	}
	return false
}

func (s *app) instructionMarkdownImportTaskCurrent(ref documentSaveRef) bool {
	if ref.scope != "task" {
		return true
	}
	task := s.taskUnit.currentTask()
	return task.id == ref.taskID && task.createdAt == ref.createdAt && sameInstructionPath(task.path, ref.taskPath)
}

func (s *app) captureInstructionMarkdownImport(state *markdownEditorState, request markdownui.ImportRequest) (instructionMarkdownImportOwner, error) {
	if state == nil || state.binding == nil || state.binding.id == "" || state.componentEditor == nil || state.readOnly || state.detached || !s.markdownEditorIsCurrent(state) {
		return instructionMarkdownImportOwner{}, errors.New("当前说明不可导入图片")
	}
	key := state.binding.id
	ref, ok := s.instructionSaveRef(key)
	if !ok || !instructionImageImportRefAllowed(ref) || !sameInstructionPath(ref.path, state.path) || !s.instructionMarkdownImportTaskCurrent(ref) {
		return instructionMarkdownImportOwner{}, errors.New("说明文档身份已变化，请重新打开文件")
	}
	owner := instructionMarkdownImportOwner{binding: state.binding, editor: state.componentEditor, document: state.doc, identity: state.liveIdentity, path: state.path, key: key, ref: ref, request: request}
	owner.request.Files = append([]string(nil), request.Files...)
	owner.request.ImagePNG = append([]byte(nil), request.ImagePNG...)
	if !s.instructionMarkdownImportCurrent(state, owner) {
		return instructionMarkdownImportOwner{}, markdownui.ErrStale
	}
	return owner, nil
}

func (s *app) instructionMarkdownImportCurrent(state *markdownEditorState, owner instructionMarkdownImportOwner) bool {
	if state == nil || owner.editor == nil || state.componentEditor != owner.editor || state.doc != owner.document || state.binding != owner.binding || state.binding == nil || state.binding.id != owner.key ||
		state.closed || state.detached || state.readOnly || state.path != owner.path || state.liveIdentity != owner.identity || !s.markdownEditorIsCurrent(state) {
		return false
	}
	ref, ok := s.instructionSaveRef(owner.key)
	if !ok || !instructionImageImportRefAllowed(ref) || !sameInstructionImageImportRef(ref, owner.ref) || !s.instructionMarkdownImportTaskCurrent(ref) {
		return false
	}
	snapshot := owner.editor.Snapshot()
	return owner.request.DocumentID == owner.key && owner.request.DocumentPath == owner.path && snapshot.DocumentID == owner.request.DocumentID &&
		snapshot.Version == owner.request.Version && !snapshot.Composing && owner.editor.Mode() == markdownui.Live
}

func sameInstructionImageImportRef(current, captured documentSaveRef) bool {
	return current.taskID == captured.taskID && current.createdAt == captured.createdAt && current.kind == captured.kind &&
		current.scope == captured.scope && current.groupID == captured.groupID && current.localTaskRules == captured.localTaskRules && current.readOnly == captured.readOnly &&
		sameInstructionPath(current.path, captured.path) && (current.taskPath == captured.taskPath || sameInstructionPath(current.taskPath, captured.taskPath))
}

func instructionMarkdownImportAction(ref documentSaveRef, payload documents.ClipboardPayload) application.Action {
	fields := map[string]any{"scope": ref.scope, "clipboard": payload}
	if ref.scope == "group" {
		fields["groupId"] = ref.groupID
	}
	if ref.scope == "task" {
		fields["localTaskRules"], fields["taskPath"], fields["taskCreatedAt"] = ref.localTaskRules, ref.taskPath, ref.createdAt
	}
	return application.Action{Kind: "instructions.images.import", TaskID: ref.taskID, Path: ref.path, Payload: fields}
}

// Instruction imports have their own server-owned image root. In particular,
// this adapter must never derive a write destination from the materials pane.
func (s *app) importInstructionMarkdownResources(state *markdownEditorState, ctx context.Context, request markdownui.ImportRequest, complete func(markdownui.ImportResult, error)) {
	owner, err := s.captureInstructionMarkdownImport(state, request)
	if err != nil {
		complete(markdownui.ImportResult{}, err)
		return
	}
	win, backend := s.win, s.backend
	if win == nil || win.IsDestroyed() || backend == nil {
		complete(markdownui.ImportResult{}, errors.New("当前说明的图片导入服务不可用"))
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		result := markdownui.ImportResult{}
		err := ctx.Err()
		files := owner.request.Files
		imagePNG := owner.request.ImagePNG
		if err == nil && owner.request.Pick {
			files, err = mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Title: "插入本地图片", DefaultPath: filepath.Dir(owner.ref.path), Multiple: true, Filters: []mygo.FileFilter{{Name: "图片", Extensions: []string{"png", "jpg", "jpeg", "gif", "webp"}}}})
			imagePNG = nil
		}
		if err == nil {
			err = ctx.Err()
		}
		if err == nil && (len(files) != 0 || len(imagePNG) != 0) {
			action := instructionMarkdownImportAction(owner.ref, documents.ClipboardPayload{Files: files, ImagePNG: imagePNG})
			var imported application.ActionResult
			imported, err = s.dispatchMaterialsBackend(ctx, backend, owner.ref.taskPath, action)
			if err == nil {
				result, err = instructionMarkdownImportResult(imported, owner.ref.path)
			}
		}
		if win.IsDestroyed() {
			return
		}
		win.Update(func() {
			if !s.instructionMarkdownImportCurrent(state, owner) {
				complete(markdownui.ImportResult{}, fmt.Errorf("%w：图片导入结果未写入已变化的说明", markdownui.ErrStale))
				return
			}
			if err == nil {
				err = ctx.Err()
			}
			if err != nil {
				complete(markdownui.ImportResult{}, err)
				return
			}
			complete(result, nil)
		})
	}()
}

func instructionMarkdownImportResult(imported application.ActionResult, documentPath string) (markdownui.ImportResult, error) {
	if !sameInstructionPath(imported.Path, documentPath) {
		return markdownui.ImportResult{}, errors.New("说明图片导入返回了不同的文档身份")
	}
	encoded, err := json.Marshal(imported.Data)
	if err != nil {
		return markdownui.ImportResult{}, err
	}
	var copied documents.InstructionImageImportResult
	if err := json.Unmarshal(encoded, &copied); err != nil {
		return markdownui.ImportResult{}, err
	}
	if len(copied.Resources) == 0 {
		return markdownui.ImportResult{}, errors.New("没有可插入的说明图片")
	}
	result := markdownui.ImportResult{Resources: make([]markdownui.ImportedResource, 0, len(copied.Resources))}
	assets := strings.TrimSuffix(filepath.Base(documentPath), filepath.Ext(documentPath)) + ".assets"
	for _, resource := range copied.Resources {
		relative := filepath.FromSlash(resource.RelativePath)
		if filepath.IsAbs(relative) || filepath.Dir(relative) != assets {
			return markdownui.ImportResult{}, errors.New("说明图片导入路径无效")
		}
		switch strings.ToLower(filepath.Ext(relative)) {
		case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		default:
			return markdownui.ImportResult{}, errors.New("说明图片导入格式无效")
		}
		target := (&url.URL{Path: filepath.ToSlash(relative)}).String()
		result.Resources = append(result.Resources, markdownui.ImportedResource{Target: target, Text: resource.Text, Image: true})
	}
	return result, nil
}
