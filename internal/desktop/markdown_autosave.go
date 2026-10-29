package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	"silverwing-go/internal/editor"
)

type documentSaveRef struct {
	taskID        string
	taskPath      string
	materialsRoot string
	relativePath  string
	path          string
	hash          string
	overwrite     bool
	kind          string
	scope         string
	groupID       string
	readOnly      bool
	hasUTF8BOM    bool
	newline       string
	textOwner     *materialTextEditorState
}

func (s *app) initMarkdownAutosave() {
	s.materialsUnit.state.markdownAutosave = editor.NewAutosave(func(ctx context.Context, key string, snapshot editor.SaveSnapshot) error {
		ref, ok := s.materialsUnit.documentSaveRef(key)
		if !ok {
			return errors.New("找不到当前 Markdown 文件的保存上下文，请重新读取文件后重试")
		}
		if markdownReadingOnly && ref.kind != "material-text" {
			return errors.New("Markdown 当前仅支持只读阅读")
		}
		if s.backend == nil {
			s.invalidateInstructionEditor()
			return errors.New("应用后端不可用，Markdown 尚未保存")
		}
		if ref.readOnly {
			s.invalidateInstructionEditor()
			return errors.New("此文件由系统生成，不能编辑")
		}
		if err := s.acquireBackend(ctx); err != nil {
			s.invalidateInstructionEditor()
			return err
		}
		payload := markdownDraftPayload(ref)
		if ref.kind == "material-text" {
			payload["hasUtf8Bom"], payload["newline"] = ref.hasUTF8BOM, ref.newline
		}
		payload["overwrite"] = ref.overwrite
		kind := "document.save"
		if ref.kind == "instruction" {
			kind = "instructions.save"
			if ref.scope != "" {
				payload["scope"] = ref.scope
			}
			if ref.groupID != "" {
				payload["groupId"] = ref.groupID
			}
		}
		result, err := s.backend.Dispatch(ctx, application.Action{
			Kind: kind, TaskID: ref.taskID, Path: ref.path, Text: snapshot.Content, Payload: payload,
		})
		s.releaseBackend()
		if err != nil {
			if isDocumentConflictError(err) {
				s.invalidateInstructionEditor()
				return editor.ConflictSaveError{Err: err}
			}
			s.invalidateInstructionEditor()
			return err
		}
		hash := dataString(result.Data, "hash")
		if hash == "" {
			s.invalidateInstructionEditor()
			return errors.New("保存请求已返回，但后端没有提供新版本标识；请重新读取文件确认")
		}
		s.materialsUnit.state.documentSaveMu.Lock()
		current, exists := s.materialsUnit.state.documentSaveRefs[key]
		if !exists || current.path != ref.path || current.taskPath != ref.taskPath || current.materialsRoot != ref.materialsRoot || current.hash != ref.hash || current.textOwner != ref.textOwner {
			s.materialsUnit.state.documentSaveMu.Unlock()
			return nil
		}
		ref.hash, ref.overwrite = hash, false
		s.materialsUnit.state.documentSaveRefs[key] = ref
		s.materialsUnit.state.documentSaveMu.Unlock()
		s.materialsUnit.state.markdownAutosave.SetCacheError(key, dataString(result.Data, "draftError"))
		return nil
	}, nil)
	s.materialsUnit.state.markdownAutosave.SetDraftWriter(func(ctx context.Context, key, content string) error {
		return s.writeMarkdownDraft(ctx, key, content, false)
	}, func(ctx context.Context, key string) error {
		return s.writeMarkdownDraft(ctx, key, "", true)
	})
	s.materialsUnit.state.markdownAutosave.SetOnSaved(func(key string, _ editor.SaveSnapshot) {
		win := s.win
		if win == nil || win.IsDestroyed() {
			return
		}
		win.Update(func() {
			ref, ok := s.materialsUnit.documentSaveRef(key)
			if !ok {
				return
			}
			state := s.materialsUnit.state.markdownAutosave.Status(key)
			if ref.kind == "material-text" {
				s.materialTextSaved(key, ref, state)
				win.Invalidate()
				return
			}
			if ref.kind == "instruction" {
				if ref.taskID != "" && ref.taskID == s.taskUnit.currentTask().id {
					for index, path := range s.taskUnit.state.notePaths {
						if sameInstructionPath(path, ref.path) {
							s.taskUnit.state.noteHashes[index] = ref.hash
						}
					}
				}
				win.Invalidate()
				return
			}
			if ref.taskID != s.taskUnit.currentTask().id || ref.path != s.materialsUnit.state.documentPath {
				return
			}
			s.materialsUnit.state.documentHash = ref.hash
			s.materialsUnit.state.documentDirty = state.Dirty
			s.materialsUnit.state.documentDirtyMap[ref.path] = state.Dirty
			if state.Dirty {
				s.materialsUnit.state.documentDrafts[ref.path] = s.materialsUnit.state.documentContent
			} else {
				delete(s.materialsUnit.state.documentDrafts, ref.path)
				delete(s.materialsUnit.state.documentDirtyMap, ref.path)
				s.lastMessage = "Markdown 已自动保存"
			}
			if state.Error != "" {
				s.lastError = state.Error
			}
		})
	})
}

func (s *app) acquireBackend(ctx context.Context) error {
	if s.backendGate == nil {
		return errors.New("应用操作队列尚未初始化")
	}
	select {
	case s.backendGate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *app) releaseBackend() {
	if s.backendGate == nil {
		return
	}
	<-s.backendGate
}

func (s *app) documentAutosaveKey(path string) string {
	return s.documentAutosaveKeyForTask(s.taskUnit.currentTask().id, path)
}

// documentAutosaveKeyForTask mirrors the source MarkdownDraftIdentity:
// task path + materials root + path relative to that root. The synthetic key
// is only an in-memory identifier and never names a user document.
func (s *app) documentAutosaveKeyForTask(taskID, path string) string {
	taskPath, root, ok := s.markdownTaskPaths(taskID)
	if !ok || taskID == "" || path == "" {
		return ""
	}
	return s.documentAutosaveKeyForIdentity(taskID, taskPath, root, path)
}

func (s *app) documentAutosaveKeyForIdentity(taskID, taskPath, materialsRoot, path string) string {
	taskPath, taskOK := absoluteCleanPath(taskPath)
	root, ok := absoluteCleanPath(materialsRoot)
	if !taskOK || !ok || strings.TrimSpace(taskID) == "" {
		return ""
	}
	absolute, ok := absoluteCleanPath(path)
	if !ok {
		return ""
	}
	relative, ok := pathRelativeToRoot(root, absolute)
	if !ok || relative == "." {
		return ""
	}
	identity := strings.Join([]string{
		normalizeIdentityPath(taskID), normalizeIdentityPath(taskPath), normalizeIdentityPath(root), normalizeIdentityPath(relative),
	}, "\x00")
	return s.autosaveStorageKey("documents", identity, root)
}

func (s *app) markdownTaskPaths(taskID string) (taskPath, materialsRoot string, ok bool) {
	for _, item := range s.taskUnit.state.tasks {
		if item.id == taskID {
			return item.path, item.materialsPath, item.path != "" && item.materialsPath != ""
		}
	}
	return "", "", false
}

func (s *app) autosaveStorageKey(namespace, identity, fallback string) string {
	digest := sha256.Sum256([]byte(identity))
	root := strings.TrimSpace(s.snapshot.Home)
	if root == "" {
		root = fallback
	}
	root, ok := absoluteCleanPath(root)
	if !ok {
		return ""
	}
	return filepath.Join(root, ".silverwing-autosave-keys", namespace, hex.EncodeToString(digest[:]))
}

func absoluteCleanPath(path string) (string, bool) {
	if strings.TrimSpace(path) == "" {
		return "", false
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	return filepath.Clean(absolute), true
}

func normalizeIdentityPath(path string) string {
	path = filepath.ToSlash(filepath.Clean(path))
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}

func pathRelativeToRoot(root, path string) (string, bool) {
	relative, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return relative, true
}

func relativeDocumentPath(root, path string) string {
	absoluteRoot, rootOK := absoluteCleanPath(root)
	absolutePath, pathOK := absoluteCleanPath(path)
	if !rootOK || !pathOK {
		return ""
	}
	relative, ok := pathRelativeToRoot(absoluteRoot, absolutePath)
	if !ok {
		return ""
	}
	return filepath.ToSlash(relative)
}

type markdownAutosaveRenamePlan struct {
	autosave      *editor.Autosave
	taskID        string
	taskPath      string
	materialsRoot string
	previous      string
	proposed      string
	lockKeys      []string
	moves         map[string]string
	sourceEntries []markdownAutosaveRenameEntry
}

type markdownAutosaveRenameEntry struct {
	oldKey  string
	newKey  string
	oldPath string
	newPath string
	ref     documentSaveRef
}

// captureMarkdownAutosaveRename runs on the UI thread before dispatch. The
// resulting immutable plan contains every cached Markdown identity under the
// source path, plus destination identities that must be quiesced before the
// source-compatible rename overwrites them.
func (s *app) captureMarkdownAutosaveRename(taskID, materialsRoot, previousAbsolute, proposedAbsolute string) (*markdownAutosaveRenamePlan, error) {
	if err := s.captureMarkdownEditorInput(s.materialsUnit.state.markdown); err != nil {
		return nil, err
	}
	for _, state := range s.materialsUnit.state.materialTextEditors {
		if state.taskID != taskID {
			continue
		}
		if _, inside := pathRelativeToRoot(previousAbsolute, state.path); !inside {
			continue
		}
		if err := s.captureMaterialTextState(state); err != nil {
			return nil, err
		}
		newPath := remapMaterialsAbsolutePath(state.path, previousAbsolute, proposedAbsolute)
		if !documents.EditableMaterialText(newPath) && s.materialsUnit.state.markdownAutosave != nil && s.materialsUnit.state.markdownAutosave.Status(state.key).Dirty {
			return nil, errors.New("该文件仍有文本草稿，请先保存或丢弃草稿，再改为只读资料格式")
		}
	}
	taskPath, taskRoot, ok := s.markdownTaskPaths(taskID)
	if !ok || strings.TrimSpace(taskID) == "" {
		return nil, errors.New("无法确认重命名所属任务")
	}
	taskPath, ok = absoluteCleanPath(taskPath)
	if !ok {
		return nil, errors.New("任务路径无效，无法迁移 Markdown 草稿")
	}
	taskRoot, ok = absoluteCleanPath(taskRoot)
	if !ok {
		return nil, errors.New("资料目录无效，无法迁移 Markdown 草稿")
	}
	requestedRoot, ok := absoluteCleanPath(materialsRoot)
	if !ok || normalizeIdentityPath(requestedRoot) != normalizeIdentityPath(taskRoot) {
		return nil, errors.New("重命名资料目录与任务当前目录不一致")
	}
	previous, ok := absoluteCleanPath(previousAbsolute)
	if !ok {
		return nil, errors.New("原文件路径无效，无法迁移 Markdown 草稿")
	}
	proposed, ok := absoluteCleanPath(proposedAbsolute)
	if !ok {
		return nil, errors.New("目标文件路径无效，无法迁移 Markdown 草稿")
	}
	if _, ok := pathRelativeToRoot(taskRoot, previous); !ok {
		return nil, errors.New("原文件不在任务资料目录中")
	}
	if _, ok := pathRelativeToRoot(taskRoot, proposed); !ok {
		return nil, errors.New("目标文件不在任务资料目录中")
	}
	if normalizeIdentityPath(previous) == normalizeIdentityPath(taskRoot) || normalizeIdentityPath(proposed) == normalizeIdentityPath(taskRoot) {
		return nil, errors.New("不能将任务资料目录作为普通文件重命名")
	}
	if filepath.Clean(previous) == filepath.Clean(proposed) {
		return nil, errors.New("重命名目标与原路径相同")
	}
	if normalizeIdentityPath(filepath.Dir(previous)) != normalizeIdentityPath(filepath.Dir(proposed)) {
		return nil, errors.New("资料重命名只能在当前目录内进行")
	}
	if relative, err := filepath.Rel(previous, proposed); err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, errors.New("不能将目录重命名到自身子目录")
	}

	plan := &markdownAutosaveRenamePlan{
		autosave: s.materialsUnit.state.markdownAutosave, taskID: taskID, taskPath: taskPath,
		materialsRoot: taskRoot, previous: previous, proposed: proposed,
		moves: make(map[string]string),
	}
	s.materialsUnit.state.documentSaveMu.Lock()
	refs := make(map[string]documentSaveRef, len(s.materialsUnit.state.documentSaveRefs))
	for key, ref := range s.materialsUnit.state.documentSaveRefs {
		refs[key] = ref
	}
	s.materialsUnit.state.documentSaveMu.Unlock()

	keySet := make(map[string]bool)
	for key, ref := range refs {
		if ref.kind == "instruction" || ref.taskID != taskID ||
			normalizeIdentityPath(ref.taskPath) != normalizeIdentityPath(taskPath) ||
			normalizeIdentityPath(ref.materialsRoot) != normalizeIdentityPath(taskRoot) {
			continue
		}
		oldPath, valid := absoluteCleanPath(ref.path)
		if !valid {
			continue
		}
		relative, err := filepath.Rel(previous, oldPath)
		if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		newPath := proposed
		if relative != "." {
			newPath = filepath.Join(proposed, relative)
		}
		if s.materialsUnit.state.markdownAutosave != nil && s.materialsUnit.state.markdownAutosave.Status(key).Dirty && (documentKindForPath(oldPath) == "markdown") != (documentKindForPath(newPath) == "markdown") {
			return nil, errors.New("该文件仍有草稿，请先保存或丢弃草稿，再更改资料格式")
		}
		if _, ok := pathRelativeToRoot(taskRoot, newPath); !ok {
			return nil, errors.New("Markdown 草稿迁移目标超出任务资料目录")
		}
		newKey := s.documentAutosaveKeyForIdentity(taskID, taskPath, taskRoot, newPath)
		if newKey == "" {
			return nil, errors.New("无法生成 Markdown 草稿的新身份")
		}
		plan.sourceEntries = append(plan.sourceEntries, markdownAutosaveRenameEntry{
			oldKey: key, newKey: newKey, oldPath: oldPath, newPath: newPath, ref: ref,
		})
		plan.moves[key] = newKey
		keySet[key] = true
		keySet[newKey] = true
	}
	for key := range keySet {
		plan.lockKeys = append(plan.lockKeys, key)
	}
	return plan, nil
}

// prepareMarkdownAutosaveRename runs in the operation worker before it takes
// the backend gate. It only reads the captured plan and autosave engine; it
// never inspects app/task/UI state while waiting for in-flight saves.
func (s *app) prepareMarkdownAutosaveRename(ctx context.Context, plan *markdownAutosaveRenamePlan) (*markdownAutosaveRenameLease, error) {
	if plan == nil {
		return nil, errors.New("Markdown 重命名计划缺失")
	}
	lease := &markdownAutosaveRenameLease{app: s, plan: plan}
	if plan.autosave == nil {
		return lease, nil
	}
	engineLease, err := plan.autosave.Quiesce(ctx, plan.lockKeys)
	if err != nil {
		return nil, err
	}
	lease.engineLease = engineLease
	return lease, nil
}

type markdownAutosaveRenameLease struct {
	mu          sync.Mutex
	app         *app
	plan        *markdownAutosaveRenamePlan
	engineLease *editor.AutosaveLease
	closed      bool
}

// Complete is called on the UI thread after the backend result has been
// validated. On successful filesystem rename, any in-memory migration failure
// is fail-closed so no dirty draft can recreate the old path.
func (l *markdownAutosaveRenameLease) Complete(success bool, actualAbsolute string) error {
	return l.complete(success, actualAbsolute, "")
}

func (l *markdownAutosaveRenameLease) complete(success bool, actualAbsolute, cacheError string) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return errors.New("Markdown 重命名保存租约已释放")
	}
	if !success {
		l.closed = true
		if l.engineLease != nil {
			l.engineLease.Release()
		}
		return nil
	}
	plan := l.plan
	if plan == nil {
		l.closed = true
		l.failClosed("文件已重命名，但 Markdown 草稿迁移计划缺失；请重新打开文件确认内容")
		return errors.New("Markdown 草稿迁移计划缺失")
	}
	s := l.app
	if s == nil {
		l.closed = true
		l.failClosed("文件已重命名，但桌面状态不可用；请重新打开文件确认内容")
		return errors.New("Markdown 桌面状态不可用")
	}
	actual, ok := absoluteCleanPath(actualAbsolute)
	if !ok || filepath.Clean(actual) != filepath.Clean(plan.proposed) {
		l.closed = true
		l.failClosed("文件已重命名，但 Markdown 草稿路径校验失败；请重新打开文件确认内容")
		return errors.New("重命名结果路径与已验证目标不一致")
	}

	updates := make([]markdownAutosaveRenameEntry, len(plan.sourceEntries))
	copy(updates, plan.sourceEntries)
	s.materialsUnit.state.documentSaveMu.Lock()
	for i := range updates {
		current, exists := s.materialsUnit.state.documentSaveRefs[updates[i].oldKey]
		if exists {
			if current.taskID != plan.taskID || normalizeIdentityPath(current.taskPath) != normalizeIdentityPath(plan.taskPath) ||
				normalizeIdentityPath(current.materialsRoot) != normalizeIdentityPath(plan.materialsRoot) ||
				normalizeIdentityPath(current.path) != normalizeIdentityPath(updates[i].oldPath) {
				s.materialsUnit.state.documentSaveMu.Unlock()
				l.closed = true
				l.failClosed("文件已重命名，但 Markdown 草稿身份发生变化；请重新打开文件确认内容")
				return errors.New("Markdown 草稿身份在重命名期间发生变化")
			}
			updates[i].ref = current
		}
		updates[i].ref.path = updates[i].newPath
		updates[i].ref.relativePath = relativeDocumentPath(plan.materialsRoot, updates[i].newPath)
		if updates[i].ref.kind == "material-text" && !documents.EditableMaterialText(updates[i].newPath) {
			updates[i].ref.readOnly = true
		}
	}
	s.materialsUnit.state.documentSaveMu.Unlock()

	if l.engineLease != nil {
		if err := l.engineLease.Move(plan.moves); err != nil {
			l.closed = true
			l.failClosed("文件已重命名，但 Markdown 草稿标识迁移失败；请重新打开文件确认内容")
			return fmt.Errorf("Markdown 草稿迁移失败：%w", err)
		}
	}

	s.materialsUnit.state.documentSaveMu.Lock()
	for _, key := range plan.lockKeys {
		delete(s.materialsUnit.state.documentSaveRefs, key)
	}
	for _, entry := range updates {
		s.materialsUnit.state.documentSaveRefs[entry.newKey] = entry.ref
	}
	s.materialsUnit.state.documentSaveMu.Unlock()

	l.closed = true
	if l.engineLease != nil {
		if cacheError != "" {
			l.engineLease.FailClosed("草稿缓存迁移失败：" + cacheError)
		} else {
			l.engineLease.Release()
		}
	}
	if cacheError != "" {
		return errors.New(cacheError)
	}
	return nil
}

// Abandon is safe from the worker thread when the native window is destroyed.
// It only touches the immutable plan's engine lease, never app/UI maps.
func (l *markdownAutosaveRenameLease) Abandon(renamed bool) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	l.closed = true
	if renamed {
		l.failClosed("文件已重命名，但窗口关闭前 Markdown 草稿标识未迁移；请重新打开文件确认内容")
	} else if l.engineLease != nil {
		l.engineLease.Release()
	}
}

func (l *markdownAutosaveRenameLease) failClosed(message string) {
	if l.engineLease != nil {
		l.engineLease.FailClosed(message)
	}
}

func (s *materialsController) documentSaveRef(key string) (documentSaveRef, bool) {
	s.state.documentSaveMu.Lock()
	defer s.state.documentSaveMu.Unlock()
	ref, ok := s.state.documentSaveRefs[key]
	return ref, ok
}

func (s *app) seedMarkdownAutosave(taskID, path, diskContent, localContent, hash string) {
	s.seedMarkdownAutosaveDraft(taskID, path, diskContent, localContent, hash, nil, "")
}

func (s *app) seedMarkdownAutosaveDraft(taskID, path, diskContent, localContent, hash string, draft *documents.MarkdownDraft, draftError string, snapshots ...documents.Content) {
	if s.materialsUnit.state.markdownAutosave == nil || documentKindForPath(path) != "markdown" {
		return
	}
	taskPath, root, ok := s.markdownTaskPaths(taskID)
	if !ok {
		return
	}
	key := s.documentAutosaveKeyForIdentity(taskID, taskPath, root, path)
	if key == "" {
		return
	}
	hasBOM, newline := false, "\n"
	if len(snapshots) > 0 {
		hasBOM, newline = snapshots[0].HasUTF8BOM, snapshots[0].Newline
	}
	if taskID == s.taskUnit.currentTask().id && normalizeIdentityPath(path) == normalizeIdentityPath(s.materialsUnit.state.documentPath) && s.materialsUnit.state.documentReadOnly {
		s.materialsUnit.state.markdownAutosave.Seed(key, diskContent)
		s.materialsUnit.state.documentSaveMu.Lock()
		s.materialsUnit.state.documentSaveRefs[key] = documentSaveRef{taskID: taskID, taskPath: taskPath, materialsRoot: root,
			relativePath: relativeDocumentPath(root, path), path: path, hash: hash, readOnly: true, hasUTF8BOM: hasBOM, newline: newline}
		s.materialsUnit.state.documentSaveMu.Unlock()
		if taskID == s.taskUnit.currentTask().id && normalizeIdentityPath(path) == normalizeIdentityPath(s.materialsUnit.state.documentPath) {
			s.materialsUnit.state.documentContent, s.materialsUnit.state.documentHash, s.materialsUnit.state.documentDirty = diskContent, hash, false
			delete(s.materialsUnit.state.documentDrafts, path)
			delete(s.materialsUnit.state.documentDirtyMap, path)
		}
		return
	}
	previous, hadPrevious := s.materialsUnit.documentSaveRef(key)
	// documentDrafts is keyed by path for legacy UI bookkeeping. It cannot
	// establish task ownership, so do not import it into a new task identity.
	_ = localContent
	s.materialsUnit.state.markdownAutosave.Seed(key, diskContent)
	restored := !s.materialsUnit.state.markdownAutosave.Status(key).Dirty && draft != nil && markdownDraftMatches(draft.Identity, taskID, taskPath, root, path)
	if restored {
		s.materialsUnit.state.markdownAutosave.RestoreDraft(key, draft.Content)
	}
	s.materialsUnit.state.markdownAutosave.SetCacheError(key, draftError)
	content, _ := s.materialsUnit.state.markdownAutosave.Content(key)
	status := s.materialsUnit.state.markdownAutosave.Status(key)
	ref := documentSaveRef{taskID: taskID, taskPath: taskPath, materialsRoot: root,
		relativePath: relativeDocumentPath(root, path), path: path, hasUTF8BOM: hasBOM, newline: newline}
	if status.Dirty {
		if restored {
			ref.hash = draft.BaselineHash
			if draft.Newline != "" {
				ref.hasUTF8BOM, ref.newline = draft.HasUTF8BOM, draft.Newline
			}
			if hash != draft.BaselineHash {
				s.materialsUnit.state.markdownAutosave.MarkConflict(key, content, "恢复的草稿对应文件已被外部修改，请接受外部版本或明确覆盖")
				status = s.materialsUnit.state.markdownAutosave.Status(key)
			}
		} else if hadPrevious {
			ref.hash, ref.overwrite = previous.hash, previous.overwrite
			ref.hasUTF8BOM, ref.newline = previous.hasUTF8BOM, previous.newline
			switch {
			case hash != "" && previous.hash != "" && hash != previous.hash:
				s.materialsUnit.state.markdownAutosave.MarkConflict(key, content, "文件已被外部修改，请接受外部版本或明确覆盖")
				status = s.materialsUnit.state.markdownAutosave.Status(key)
			case hash != "" && previous.hash == "":
				s.materialsUnit.state.markdownAutosave.MarkConflict(key, content, "草稿没有可验证的原始版本，请重新读取或明确覆盖")
				status = s.materialsUnit.state.markdownAutosave.Status(key)
			}
		} else {
			ref.hash = hash
			if hash != "" {
				s.materialsUnit.state.markdownAutosave.MarkConflict(key, content, "草稿没有可验证的原始版本，请重新读取或明确覆盖")
				status = s.materialsUnit.state.markdownAutosave.Status(key)
			}
		}
	} else {
		ref.hash = hash
	}
	s.materialsUnit.state.documentSaveMu.Lock()
	s.materialsUnit.state.documentSaveRefs[key] = ref
	s.materialsUnit.state.documentSaveMu.Unlock()
	if taskID == s.taskUnit.currentTask().id && normalizeIdentityPath(path) == normalizeIdentityPath(s.materialsUnit.state.documentPath) {
		s.materialsUnit.state.documentContent = content
		s.materialsUnit.state.documentHash = ref.hash
		s.materialsUnit.state.documentDirty = status.Dirty
		if status.Dirty {
			s.materialsUnit.state.documentDrafts[path] = content
			s.materialsUnit.state.documentDirtyMap[path] = true
		} else {
			delete(s.materialsUnit.state.documentDrafts, path)
			delete(s.materialsUnit.state.documentDirtyMap, path)
		}
	}
}

// instructionAutosaveKey creates a stable namespace for task, global, and group
// instruction drafts. The resulting path is only an in-memory key; no file is
// created at this location.
func (s *app) instructionAutosaveKey(scope, taskID, groupID, path string) string {
	identity := strings.Join([]string{scope, taskID, groupID, filepath.Clean(path)}, "\x00")
	root := s.snapshot.Home
	if strings.TrimSpace(root) == "" {
		root = filepath.Dir(path)
	}
	return s.autosaveStorageKey("instructions", identity, root)
}

func instructionIdentity(action application.Action, path string) (scope, taskID, groupID string) {
	scope = payloadString(action.Payload, "scope")
	groupID = payloadString(action.Payload, "groupId")
	taskID = action.TaskID
	if scope == "" {
		scope = "task"
	}
	return scope, taskID, groupID
}

// seedInstructionAutosave seeds the shared autosave engine from an
// instructions.read result. An existing dirty draft remains authoritative;
// a changed disk hash marks it conflicted instead of silently replacing it.
func (s *app) seedInstructionAutosave(action application.Action, result application.ActionResult) (string, string, bool) {
	if s.materialsUnit.state.markdownAutosave == nil {
		return "", result.Text, markdownReadingOnly || dataBool(result.Data, "readOnly")
	}
	path := result.Path
	if path == "" {
		path = action.Path
	}
	if strings.TrimSpace(path) == "" {
		return "", result.Text, markdownReadingOnly || dataBool(result.Data, "readOnly")
	}
	scope, taskID, groupID := instructionIdentity(action, path)
	key := s.instructionAutosaveKey(scope, taskID, groupID, path)
	readOnly := markdownReadingOnly || dataBool(result.Data, "readOnly")
	newHash := dataString(result.Data, "hash")
	if markdownReadingOnly {
		s.materialsUnit.state.markdownAutosave.Seed(key, result.Text)
		ref := documentSaveRef{taskID: taskID, path: path, hash: newHash, kind: "instruction", readOnly: true}
		if scope != "task" {
			ref.scope, ref.groupID = scope, groupID
		}
		s.materialsUnit.state.documentSaveMu.Lock()
		s.materialsUnit.state.documentSaveRefs[key] = ref
		s.materialsUnit.state.documentSaveMu.Unlock()
		return key, result.Text, true
	}
	acceptDisk := dataBool(action.Payload, "acceptDisk")
	previous, hadPrevious := s.materialsUnit.documentSaveRef(key)

	if acceptDisk {
		s.materialsUnit.state.markdownAutosave.AcceptDisk(key, result.Text, true)
	} else {
		s.materialsUnit.state.markdownAutosave.Seed(key, result.Text)
	}
	content, _ := s.materialsUnit.state.markdownAutosave.Content(key)
	status := s.materialsUnit.state.markdownAutosave.Status(key)
	ref := documentSaveRef{taskID: taskID, path: path, kind: "instruction", scope: "", groupID: "", readOnly: readOnly}
	if scope != "task" {
		ref.scope, ref.groupID = scope, groupID
	}
	if hadPrevious && status.Dirty && !acceptDisk {
		ref.hash = previous.hash
		ref.overwrite = previous.overwrite
		if newHash != "" && (previous.hash == "" || newHash != previous.hash) {
			s.materialsUnit.state.markdownAutosave.MarkConflict(key, content, "文件已被外部修改，请接受外部版本或明确覆盖")
		}
	} else {
		ref.hash = newHash
	}
	s.materialsUnit.state.documentSaveMu.Lock()
	s.materialsUnit.state.documentSaveRefs[key] = ref
	s.materialsUnit.state.documentSaveMu.Unlock()
	return key, content, readOnly
}

func (s *app) instructionSaveRef(key string) (documentSaveRef, bool) {
	ref, ok := s.materialsUnit.documentSaveRef(key)
	return ref, ok && ref.kind == "instruction"
}

func (s *app) editInstructionAutosave(key, content string) {
	if markdownReadingOnly {
		return
	}
	if ref, ok := s.instructionSaveRef(key); ok && !ref.readOnly && s.materialsUnit.state.markdownAutosave != nil {
		s.materialsUnit.state.markdownAutosave.Edit(key, content)
	}
}

func (s *app) setInstructionComposing(key string, composing bool) {
	if markdownReadingOnly {
		return
	}
	if ref, ok := s.instructionSaveRef(key); ok && !ref.readOnly && s.materialsUnit.state.markdownAutosave != nil {
		s.materialsUnit.state.markdownAutosave.SetComposing(key, composing)
	}
}

func (s *app) setMarkdownComposing(taskID, path string, composing bool) {
	if s.materialsUnit.state.markdownAutosave == nil {
		return
	}
	key := s.documentAutosaveKeyForTask(taskID, path)
	if ref, exists := s.materialsUnit.documentSaveRef(key); key != "" && exists && !ref.readOnly && ref.kind != "instruction" && ref.kind != "material-text" {
		s.materialsUnit.state.markdownAutosave.SetComposing(key, composing)
	}
}

func (s *app) observeMarkdownComposition(state *markdownEditorState, owner string, composing bool) {
	if state == nil || state.doc == nil || owner == "" {
		return
	}
	if composing {
		if previous := state.composingOwner; previous != "" && previous != owner {
			state.doc.Composition(previous, false)
		}
		state.composingOwner = owner
		state.doc.Composition(owner, true)
		s.setMarkdownEditorComposing(state, true)
		return
	}
	if state.composingOwner == owner {
		state.doc.Composition(owner, false)
		state.composingOwner = ""
		s.setMarkdownEditorComposing(state, false)
	}
}

func (s *app) clearMarkdownComposition(state *markdownEditorState) {
	if state == nil || state.composingOwner == "" {
		return
	}
	state.doc.Composition(state.composingOwner, false)
	state.composingOwner = ""
	s.setMarkdownEditorComposing(state, false)
}

func (s *app) setMarkdownEditorComposing(state *markdownEditorState, composing bool) {
	if state == nil {
		return
	}
	if state.binding != nil {
		if state.binding.isCurrent != nil && state.binding.isCurrent() && state.binding.setComposing != nil {
			state.binding.setComposing(composing)
		}
		return
	}
	s.setMarkdownComposing(state.taskID, state.path, composing)
}

func sameInstructionPath(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func (s *app) invalidateInstructionEditor() {
	win := s.win
	if win != nil && !win.IsDestroyed() {
		win.Update(func() { win.Invalidate() })
	}
}

func (s *app) editMarkdownAutosave(path, content string) {
	if s.materialsUnit.state.documentReadOnly || s.materialsUnit.state.markdownAutosave == nil || documentKindForPath(path) != "markdown" {
		return
	}
	taskID := s.taskUnit.currentTask().id
	_, _, taskOK := s.markdownTaskPaths(taskID)
	key := s.documentAutosaveKeyForTask(taskID, path)
	if !taskOK || key == "" {
		return
	}
	s.materialsUnit.state.documentSaveMu.Lock()
	ref, exists := s.materialsUnit.state.documentSaveRefs[key]
	s.materialsUnit.state.documentSaveMu.Unlock()
	if !exists || ref.readOnly || markdownReadingOnly || ref.kind == "instruction" || ref.kind == "material-text" {
		return
	}
	s.materialsUnit.state.markdownAutosave.Edit(key, content)
}

func (s *app) markdownSaveState() (string, editor.SaveState) {
	if s.materialsUnit.state.documentReadOnly || s.materialsUnit.state.markdownAutosave == nil || s.materialsUnit.state.documentPath == "" || documentKindForPath(s.materialsUnit.state.documentPath) != "markdown" {
		return "", editor.SaveState{}
	}
	key := s.documentAutosaveKeyForTask(s.taskUnit.currentTask().id, s.materialsUnit.state.documentPath)
	return key, s.materialsUnit.state.markdownAutosave.Status(key)
}

func (s *app) flushMarkdownAutosave(key string) {
	ref, exists := s.materialsUnit.documentSaveRef(key)
	if s.materialsUnit.state.markdownAutosave == nil || key == "" || !exists || ref.readOnly || markdownReadingOnly && ref.kind != "material-text" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		failures := s.materialsUnit.state.markdownAutosave.Flush(ctx)
		win := s.win
		if win == nil || win.IsDestroyed() {
			return
		}
		win.Update(func() {
			for _, failure := range failures {
				ref, ok := s.materialsUnit.documentSaveRef(failure.Path)
				if ok && ref.kind == "instruction" {
					state := s.materialsUnit.state.markdownAutosave.Status(failure.Path)
					if state.Conflict || state.Error != "" || state.Dirty {
						s.lastError = failure.Message
						win.Invalidate()
					}
					continue
				}
				if ok && ref.path == s.materialsUnit.state.documentPath {
					s.lastError = failure.Message
					break
				}
			}
		})
	}()
}

func (s *app) retryMarkdownAutosave(key string) {
	ref, exists := s.materialsUnit.documentSaveRef(key)
	if exists && !ref.readOnly && !(markdownReadingOnly && ref.kind != "material-text") && s.materialsUnit.state.markdownAutosave != nil && s.materialsUnit.state.markdownAutosave.Retry(key) {
		s.lastError = ""
	}
}

func (s *app) overwriteMarkdownAfterConflict(key string) {
	if s.materialsUnit.state.markdownAutosave == nil {
		return
	}
	s.materialsUnit.state.documentSaveMu.Lock()
	ref, ok := s.materialsUnit.state.documentSaveRefs[key]
	if ok && !ref.readOnly && !(markdownReadingOnly && ref.kind != "material-text") {
		ref.overwrite = true
		s.materialsUnit.state.documentSaveRefs[key] = ref
	}
	s.materialsUnit.state.documentSaveMu.Unlock()
	if ok && !ref.readOnly && !(markdownReadingOnly && ref.kind != "material-text") {
		s.materialsUnit.state.markdownAutosave.ResumeAfterConflict(key)
	}
}

func isDocumentConflictError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "外部修改") || strings.Contains(message, "冲突") ||
		strings.Contains(message, "baseline") || strings.Contains(message, "changed during")
}

func (s *app) discardMarkdownAndReload() {
	if s.materialsUnit.state.documentReadOnly {
		s.rereadSelectedMaterial(false)
		return
	}
	key, state := s.markdownSaveState()
	if key == "" || s.materialsUnit.state.markdownAutosave == nil {
		return
	}
	if state.Composing {
		s.lastError = "中文输入尚未结束，请提交输入后再丢弃草稿"
		return
	}
	ref, ok := s.materialsUnit.documentSaveRef(key)
	if !ok {
		s.lastError = "找不到当前草稿的文档身份"
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		lease, err := s.materialsUnit.state.markdownAutosave.Quiesce(ctx, []string{key})
		if err == nil && s.materialsUnit.state.markdownAutosave.Status(key).Revision != state.Revision {
			err = errors.New("草稿已经产生新的编辑，请再次确认丢弃")
		}
		if err == nil {
			err = s.materialsUnit.state.markdownAutosave.DiscardDraft(ctx, key)
		}
		win := s.win
		if win == nil || win.IsDestroyed() {
			if lease != nil {
				lease.Release()
			}
			return
		}
		win.Update(func() {
			if lease != nil {
				defer lease.Release()
			}
			if err == nil && s.materialsUnit.state.markdownAutosave.Status(key).Revision != state.Revision {
				err = errors.New("草稿已经产生新的编辑，最新原文已保留")
			}
			if err != nil {
				s.lastError = "未能丢弃草稿：" + err.Error()
				win.Invalidate()
				return
			}
			s.materialsUnit.state.markdownAutosave.AcceptDisk(key, "", true)
			if s.taskUnit.currentTask().id != ref.taskID || s.documentAutosaveKey(s.materialsUnit.state.documentPath) != key {
				return
			}
			delete(s.materialsUnit.state.documentDrafts, ref.path)
			delete(s.materialsUnit.state.documentDirtyMap, ref.path)
			s.closeMarkdownEditor()
			s.materialsUnit.state.documentDirty, s.materialsUnit.state.documentEditing = false, false
			s.materialsUnit.state.documentContent, s.lastError = "", ""
			s.act("document.read", ref.taskID, "", "", ref.path, "", nil)
		})
	}()
}

func markdownSaveLabel(state editor.SaveState) string {
	switch {
	case state.Conflict:
		return "文件有外部修改"
	case state.Error != "":
		return fmt.Sprintf("自动保存失败：%s", state.Error)
	case state.CacheError != "":
		return fmt.Sprintf("草稿缓存失败：%s", state.CacheError)
	default:
		return ""
	}
}
