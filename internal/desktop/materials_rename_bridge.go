package desktop

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
)

// A rename belongs to the task and materials root that issued it. An old task's
// result may migrate its cached reading state, but cannot select a new document
// in the task currently shown on screen.
func (s *app) applyMaterialsRenameResult(action application.Action, result application.ActionResult) {
	task := s.taskUnit.taskByID(action.TaskID)
	if expectedPath := payloadString(action.Payload, "desktopRenameTaskPath"); expectedPath != "" {
		if normalizedReadingPath(expectedPath) != normalizedReadingPath(task.path) ||
			payloadString(action.Payload, "desktopRenameTaskCreatedAt") != task.createdAt ||
			normalizedReadingRoot(payloadString(action.Payload, "desktopRenameMaterialsRoot")) != normalizedReadingRoot(task.materialsPath) {
			return
		}
	}
	operation, oldPath, newPath, ok := materialsRenamePaths(task.materialsPath, action, result)
	if !ok || task.id == "" {
		return
	}
	previous, replacement := operation.PreviousRelativePath, operation.RelativePath
	s.remapMaterialTextEditors(task.id, task.path, task.materialsPath, oldPath, newPath)
	s.remapMaterialsReading(task.path, task.materialsPath, previous, replacement)
	if s.taskUnit.currentTask().id != action.TaskID {
		return
	}
	s.remapMaterialsFileSelectionAfterRename(task, operation, oldPath, newPath)
	remap := func(path string) string { return remapMaterialsAbsolutePath(path, oldPath, newPath) }
	s.materialsUnit.state.documentDrafts = remapMaterialsEntries(s.materialsUnit.state.documentDrafts, oldPath, newPath)
	s.materialsUnit.state.documentDirtyMap = remapMaterialsEntries(s.materialsUnit.state.documentDirtyMap, oldPath, newPath)
	for index, path := range s.materialsUnit.state.documentTabs {
		s.materialsUnit.state.documentTabs[index] = remap(path)
	}
	s.materialsUnit.state.documentTabs = uniqueMaterialsPaths(s.materialsUnit.state.documentTabs)
	selectedPath := remap(s.materialsUnit.state.documentPath)
	adapterChanged := selectedPath != s.materialsUnit.state.documentPath && (documentKindForPath(s.materialsUnit.state.documentPath) == "markdown") != (documentKindForPath(selectedPath) == "markdown")
	renamedMedia := selectedPath != s.materialsUnit.state.documentPath && (s.materialsUnit.state.documentKind == "image" || s.materialsUnit.state.documentKind == "pdf")
	if selectedPath != s.materialsUnit.state.documentPath {
		s.materialsUnit.closeNativeMedia()
		s.materialsUnit.state.documentPath = selectedPath
		s.materialsUnit.state.documentKind = documentKindForPath(selectedPath)
		if s.materialsUnit.state.markdown != nil {
			s.materialsUnit.state.markdown.path = remap(s.materialsUnit.state.markdown.path)
			if s.materialsUnit.state.documentKind != "markdown" {
				s.syncMarkdownEditor()
				s.closeMarkdownEditor()
				s.materialsUnit.state.documentEditing = false
			}
		}
	}
	if !operation.IsDirectory {
		if !containsMaterialsPath([]string{s.materialsUnit.state.documentPath}, newPath) {
			// Renaming a different tree entry must not display the previously
			// selected file's bytes under the new filename.
			s.selectDocument(newPath)
		}
		if !containsMaterialsPath(s.materialsUnit.state.documentTabs, newPath) {
			s.materialsUnit.state.documentTabs = append(s.materialsUnit.state.documentTabs, newPath)
		}
		for parent := filepath.Dir(newPath); normalizedReadingPath(parent) != normalizedReadingPath(task.materialsPath); parent = filepath.Dir(parent) {
			if parent == filepath.Dir(parent) {
				break
			}
			s.setReadingDirectoryExpanded(parent, true)
		}
	} else {
		s.setReadingDirectoryExpanded(newPath, true)
	}
	s.materialsUnit.state.documentsLoaded, s.materialsUnit.state.documentsRequested = false, false
	if s.readingSession != nil {
		s.readingSession.userInteracted = true
	}
	if renamedMedia {
		// The selected path was already remapped, so selectDocument would
		// short-circuit. Read the new path to reopen its native media object.
		s.rereadSelectedMaterial(false)
	}
	if adapterChanged {
		for key, state := range s.materialsUnit.state.materialTextEditors {
			if normalizeIdentityPath(state.path) == normalizeIdentityPath(selectedPath) {
				state.closed = true
				delete(s.materialsUnit.state.materialTextEditors, key)
			}
		}
		s.closeMarkdownEditor()
		s.materialsUnit.state.documentEditing = false
		s.materialsUnit.state.documentReadPending = true
		s.act("document.read", task.id, "", "", selectedPath, "", nil)
	}
}

func (s *app) remapMaterialsFileSelectionAfterRename(task task, operation documents.FileOperationResult, oldPath, newPath string) {
	state := s.materialsUnit.state.materialsFileManagement
	if state == nil || state.taskID != task.id || normalizedReadingRoot(state.root) != normalizedReadingRoot(task.materialsPath) {
		return
	}
	selected := make(map[string]materialsFileSelectionEntry, len(state.selected))
	anchor := state.anchor
	for key, entry := range state.selected {
		entry.Path = remapMaterialsAbsolutePath(entry.Path, oldPath, newPath)
		entry.Relative = relativeDocumentPath(state.root, entry.Path)
		entry.Key = materialsFileEntryKey(entry.Relative, entry.Directory)
		selected[entry.Key] = entry
		if key == state.anchor {
			anchor = entry.Key
		}
	}
	state.materialsSelection.replace(selected, anchor)
	state.previewPath = remapMaterialsAbsolutePath(state.previewPath, oldPath, newPath)
	if operation.IsDirectory {
		// Source selects the renamed directory immediately and retains that
		// intent across the subsequent tree reconciliation.
		entry := materialsFileSelectionEntry{Key: materialsFileEntryKey(operation.RelativePath, true), Path: newPath, Relative: operation.RelativePath, Directory: true}
		state.materialsSelection.selectOnly(entry)
		state.pendingSelectDir = operation.RelativePath
	}
}

func materialsRenameCompleted(result application.ActionResult) bool {
	encoded, err := json.Marshal(result.Data)
	if err != nil {
		return false
	}
	var operation documents.FileOperationResult
	return json.Unmarshal(encoded, &operation) == nil && operation.Status == documents.FileOperationCompleted
}

func completeMaterialsRenameLease(lease *markdownAutosaveRenameLease, root string, action application.Action, result application.ActionResult) error {
	if !materialsRenameCompleted(result) {
		return lease.Complete(false, "")
	}
	operation, _, path, ok := materialsRenamePaths(root, action, result)
	if !ok {
		lease.Abandon(true)
		return errors.New("重命名结果与原任务资料路径不一致，请重新打开文件确认内容")
	}
	return lease.complete(true, path, operation.DraftError)
}

func materialsRenamePaths(root string, action application.Action, result application.ActionResult) (documents.FileOperationResult, string, string, bool) {
	var operation documents.FileOperationResult
	if root == "" || action.Kind != "document.rename" {
		return operation, "", "", false
	}
	encoded, err := json.Marshal(result.Data)
	if err != nil {
		return operation, "", "", false
	}
	if err := json.Unmarshal(encoded, &operation); err != nil || operation.Status != documents.FileOperationCompleted {
		return operation, "", "", false
	}
	previous, validPrevious := validReadingRelativePath(operation.PreviousRelativePath)
	replacement, validReplacement := validReadingRelativePath(operation.RelativePath)
	if !validPrevious || !validReplacement {
		return operation, "", "", false
	}
	oldPath := filepath.Join(root, filepath.FromSlash(previous))
	newPath := filepath.Join(root, filepath.FromSlash(replacement))
	if normalizedReadingPath(action.Path) != normalizedReadingPath(oldPath) || normalizedReadingPath(result.Path) != normalizedReadingPath(newPath) {
		return operation, "", "", false
	}
	operation.PreviousRelativePath, operation.RelativePath = previous, replacement
	return operation, oldPath, newPath, true
}

// Match Source's source-draft-wins replacement when a removed destination has
// stale cached state. Process unchanged keys before moved keys deliberately.
func remapMaterialsEntries[T any](values map[string]T, previous, replacement string) map[string]T {
	result := make(map[string]T, len(values))
	for path, value := range values {
		if remapMaterialsAbsolutePath(path, previous, replacement) == path {
			result[path] = value
		}
	}
	for path, value := range values {
		if mapped := remapMaterialsAbsolutePath(path, previous, replacement); mapped != path {
			result[mapped] = value
		}
	}
	return result
}

func remapMaterialsAbsolutePath(path, previous, replacement string) string {
	if strings.TrimSpace(path) == "" {
		return path
	}
	if normalizedReadingPath(path) == normalizedReadingPath(previous) {
		return replacement
	}
	relative, err := filepath.Rel(previous, path)
	if err != nil || filepath.IsAbs(relative) || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return path
	}
	return filepath.Join(replacement, relative)
}

func remapMaterialsRelativePath(path, previous, replacement string) string {
	if path == previous {
		return replacement
	}
	if strings.HasPrefix(path, previous+"/") {
		return replacement + strings.TrimPrefix(path, previous)
	}
	return path
}

func (s *app) remapMaterialsReading(taskPath, root, previous, replacement string) {
	session := s.readingSession
	if session == nil || !session.loaded || session.closed {
		return
	}
	for index := range session.cache.Materials {
		record := &session.cache.Materials[index]
		if normalizedReadingPath(record.TaskPath) != normalizedReadingPath(taskPath) || normalizedReadingRoot(readingRootValue(record.Root)) != normalizedReadingRoot(root) {
			continue
		}
		session.dirty = true
		remap := func(path string) string { return remapMaterialsRelativePath(path, previous, replacement) }
		if record.SelectedPath != nil {
			value := remap(*record.SelectedPath)
			record.SelectedPath = &value
		}
		files := make(map[string]ReadingPositionSnapshot, len(record.Files))
		for path, position := range record.Files {
			if remap(path) == path {
				files[path] = position
			}
		}
		for path, position := range record.Files {
			if mapped := remap(path); mapped != path {
				files[mapped] = position
			}
		}
		record.Files = files
		for i, path := range record.fileOrder {
			record.fileOrder[i] = remap(path)
		}
		for i, path := range record.ExpandedDirectories {
			record.ExpandedDirectories[i] = remap(path)
		}
		for i := range record.DocumentTabs.Tabs {
			record.DocumentTabs.Tabs[i].Path = remap(record.DocumentTabs.Tabs[i].Path)
		}
		if record.DocumentTabs.ActivePath != nil {
			value := remap(*record.DocumentTabs.ActivePath)
			record.DocumentTabs.ActivePath = &value
		}
	}
	for key, binding := range session.scrollStates {
		if binding.taskPath == normalizedReadingPath(taskPath) && binding.root == normalizedReadingRoot(root) && binding.requirementLink == "" {
			binding.document = remapMaterialsRelativePath(binding.document, previous, replacement)
			newKey := readingScrollBindingKey(binding)
			if newKey != key {
				delete(session.scrollStates, key)
			}
			session.scrollStates[newKey] = binding
		}
	}
}

func containsMaterialsPath(paths []string, value string) bool {
	for _, path := range paths {
		if normalizedReadingPath(path) == normalizedReadingPath(value) {
			return true
		}
	}
	return false
}

func uniqueMaterialsPaths(paths []string) []string {
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if !containsMaterialsPath(result, path) {
			result = append(result, path)
		}
	}
	return result
}
