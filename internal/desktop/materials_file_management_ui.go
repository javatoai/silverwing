package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
)

type materialsFileSelectionEntry struct {
	Key       string
	Path      string
	Relative  string
	Directory bool
}

type materialsFileManagementState struct {
	taskID string
	root   string

	selected    map[string]materialsFileSelectionEntry
	anchor      string
	focused     bool
	tree        FileTreeState
	previewPath string

	pickerBusy       bool
	pickerGeneration uint64
	pathCopyRequest  *materialsPathCopyRequest

	contextOpen bool
	contextPath string

	conflict              *documents.FileOperationConflict
	conflictAlternateName string
	conflictPending       []documents.FileOperationRequest
	flowCompleted         []documents.FileOperationResult
	flowFailures          []documents.FileOperationFailure
	flowTaskID            string
	flowRoot              string
	flowOrigin            string
	flowActionKind        string
	flowPath              string
	flowActive            bool

	trashPreviewOpen    bool
	trashPreviewPending bool
	trashPaths          []string

	pendingSelectPath    string
	pendingEditPath      string
	pendingSelectDir     string
	windowRefreshPending bool
	liveRefresh          *materialsLiveRefreshState

	resultOpen bool
	resultText string
	resultRows []string
	lastError  string
}

func (s *app) ensureMaterialsFileManagementState() *materialsFileManagementState {
	if s.materialsFileManagement == nil {
		s.materialsFileManagement = &materialsFileManagementState{}
	}
	state := s.materialsFileManagement
	task := s.currentTask()
	root := filepath.Clean(strings.TrimSpace(task.materialsPath))
	if task.id != state.taskID || normalizedReadingRoot(root) != normalizedReadingRoot(state.root) {
		if state.liveRefresh != nil {
			s.retireMaterialsLiveRefresh(state.liveRefresh)
		}
		if state.pathCopyRequest != nil {
			state.pathCopyRequest.cancel()
		}
		pickerGeneration := state.pickerGeneration + 1
		*state = materialsFileManagementState{
			taskID:           task.id,
			root:             root,
			selected:         make(map[string]materialsFileSelectionEntry),
			pickerGeneration: pickerGeneration,
		}
	}
	if state.selected == nil {
		state.selected = make(map[string]materialsFileSelectionEntry)
	}
	return state
}

func (s *app) resetMaterialsFileSelection(state *materialsFileManagementState) {
	if state == nil {
		return
	}
	state.selected = make(map[string]materialsFileSelectionEntry)
	state.anchor = ""
}

func materialsFileEntryKey(relative string, directory bool) string {
	prefix := "file:"
	if directory {
		prefix = "directory:"
	}
	return prefix + filepath.ToSlash(relative)
}

func (s *app) materialsEntryForDocument(document application.Document) (materialsFileSelectionEntry, bool) {
	relative := s.materialsRelativePath(document.Path)
	if relative == "" {
		return materialsFileSelectionEntry{}, false
	}
	directory := isDocumentDirectory(document)
	return materialsFileSelectionEntry{
		Key:       materialsFileEntryKey(relative, directory),
		Path:      filepath.Clean(document.Path),
		Relative:  relative,
		Directory: directory,
	}, true
}

func (s *app) visibleMaterialsEntries() []materialsFileSelectionEntry {
	order := s.readingMaterialsSortOrder()
	expanded := s.readingExpandedDirectories()
	entries := make([]materialsFileSelectionEntry, 0, len(s.documents))
	var visit func([]application.Document)
	visit = func(items []application.Document) {
		for _, document := range sortDocumentsByOrder(items, order) {
			entry, ok := s.materialsEntryForDocument(document)
			if !ok {
				continue
			}
			entries = append(entries, entry)
			if entry.Directory && expanded[entry.Relative] {
				visit(document.Children)
			}
		}
	}
	visit(s.documents)
	return entries
}

func (s *app) selectMaterialsEntry(state *materialsFileManagementState, document application.Document, modifiers ui.Modifiers, contextSelect bool) {
	entry, ok := s.materialsEntryForDocument(document)
	if state == nil || !ok {
		return
	}
	if state.selected == nil {
		state.selected = make(map[string]materialsFileSelectionEntry)
	}
	if contextSelect {
		state.contextPath = entry.Path
		return
	}
	ctrl := modifiers&(ui.Ctrl|ui.Super) != 0
	shift := modifiers&ui.Shift != 0
	visible := s.visibleMaterialsEntries()
	if shift && state.anchor != "" {
		from, to := -1, -1
		for index, item := range visible {
			if item.Key == state.anchor {
				from = index
			}
			if item.Key == entry.Key {
				to = index
			}
		}
		if from >= 0 && to >= 0 {
			start, end := min(from, to), max(from, to)
			if !ctrl {
				state.selected = make(map[string]materialsFileSelectionEntry)
			}
			for _, item := range visible[start : end+1] {
				state.selected[item.Key] = item
			}
		} else if !ctrl {
			state.selected = map[string]materialsFileSelectionEntry{entry.Key: entry}
		}
	} else if ctrl {
		if _, selected := state.selected[entry.Key]; selected {
			delete(state.selected, entry.Key)
		} else {
			state.selected[entry.Key] = entry
		}
	} else {
		state.selected = map[string]materialsFileSelectionEntry{entry.Key: entry}
	}
	if !shift {
		state.anchor = entry.Key
	}
	state.focused = true
}

func (s *app) materialsFileSelection(state *materialsFileManagementState) []materialsFileSelectionEntry {
	if state == nil || len(state.selected) == 0 {
		return nil
	}
	entries := make([]materialsFileSelectionEntry, 0, len(state.selected))
	for _, entry := range state.selected {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Relative) < strings.ToLower(entries[j].Relative)
	})
	selectedDirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Directory {
			selectedDirs = append(selectedDirs, filepath.Clean(entry.Path))
		}
	}
	filtered := entries[:0]
	for _, entry := range entries {
		covered := false
		for _, parent := range selectedDirs {
			if normalizedReadingPath(parent) == normalizedReadingPath(entry.Path) {
				continue
			}
			relative, err := filepath.Rel(parent, entry.Path)
			if err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) {
				covered = true
				break
			}
		}
		if !covered {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func (s *app) materialsCurrentDirectory(state *materialsFileManagementState) (absolute, relative string) {
	task := s.currentTask()
	if state == nil || task.id == "" || task.id != state.taskID || task.materialsPath == "" {
		return "", ""
	}
	if _, anchorRelative, hasAnchor := strings.Cut(state.anchor, ":"); hasAnchor {
		anchorPath := filepath.Join(task.materialsPath, filepath.FromSlash(anchorRelative))
		if anchor, ok := s.findMaterialsEntry(anchorPath, s.documents); ok && anchor.Key == state.anchor {
			relative = anchor.Relative
			if !anchor.Directory {
				relative = filepath.ToSlash(filepath.Dir(filepath.FromSlash(relative)))
				if relative == "." {
					relative = ""
				}
			}
		}
	}
	if relative == "" {
		return filepath.Clean(task.materialsPath), ""
	}
	absolute = filepath.Clean(filepath.Join(task.materialsPath, filepath.FromSlash(relative)))
	rel, err := filepath.Rel(task.materialsPath, absolute)
	if err != nil || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.Clean(task.materialsPath), ""
	}
	return absolute, filepath.ToSlash(rel)
}

func (s *app) revealMaterialsPath(path string) {
	if path == "" || s.currentTask().materialsPath == "" {
		return
	}
	relative := s.materialsRelativePath(filepath.Clean(path))
	if relative == "" {
		return
	}
	parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(relative)))
	for parent != "" && parent != "." {
		s.setReadingDirectoryExpanded(parent, true)
		parent = filepath.ToSlash(filepath.Dir(filepath.FromSlash(parent)))
	}
}

func (s *app) selectMaterialsPath(path string) {
	state := s.ensureMaterialsFileManagementState()
	path = filepath.Clean(path)
	entry, ok := s.findMaterialsEntry(path, s.documents)
	if !ok {
		return
	}
	state.selected = map[string]materialsFileSelectionEntry{entry.Key: entry}
	state.anchor = entry.Key
	state.focused = true
	s.revealMaterialsPath(path)
	if entry.Directory {
		s.setReadingDirectoryExpanded(entry.Relative, true)
		return
	}
	s.selectDocument(path)
}

func (s *app) findMaterialsEntry(path string, items []application.Document) (materialsFileSelectionEntry, bool) {
	for _, document := range items {
		if normalizedReadingPath(document.Path) == normalizedReadingPath(path) {
			return s.materialsEntryForDocument(document)
		}
		if entry, ok := s.findMaterialsEntry(path, document.Children); ok {
			return entry, true
		}
	}
	return materialsFileSelectionEntry{}, false
}

func (s *app) materialsEntryContextMenu(c *ui.Context, menu *ui.Menu, state *materialsFileManagementState, entry materialsFileSelectionEntry) {
	if entry.Path == "" || state == nil {
		return
	}
	busy := s.materialsFileInputBusy() || s.backend == nil
	systemBusy := s.materialsSystemOpenBusy(state)
	folder := entry.Directory
	if folder {
		labelText := "展开文件夹"
		if s.readingExpandedDirectories()[entry.Relative] {
			labelText = "收起文件夹"
		}
		if menu.Item(labelText).Icon(icons["folder"]).Disabled(busy).Chosen() {
			s.setReadingDirectoryExpanded(entry.Relative, !s.readingExpandedDirectories()[entry.Relative])
		}
	} else if menu.Item("打开文档").Icon(icons["file"]).Disabled(busy).Chosen() {
		s.selectMaterialsPath(entry.Path)
	}
	s.materialsFileContextFeishuActions(menu, s.currentTask(), entry)
	menu.Separator()
	if menu.Item("新建文档").Icon(icons["file"]).Disabled(busy).Chosen() {
		directory := entry.Path
		if !folder {
			directory = filepath.Dir(directory)
		}
		s.openMaterialsFileNameForm("document.create", "新建文档", directory)
	}
	if menu.Item("新建文件夹").Icon(icons["folder"]).Disabled(busy).Chosen() {
		directory := entry.Path
		if !folder {
			directory = filepath.Dir(directory)
		}
		s.openMaterialsFileNameForm("document.mkdir", "新建文件夹", directory)
	}
	menu.Separator()
	if menu.Item("导入文件到此处…").Icon(icons["plus"]).Disabled(busy).Chosen() {
		s.selectMaterialsDirectory(entry)
		s.startMaterialsImportPicker()
	}
	if menu.Item("粘贴到此处").Icon(icons["copy"]).Disabled(busy).Chosen() {
		s.selectMaterialsDirectory(entry)
		s.pasteMaterialsIntoCurrentDirectory()
	}
	if menu.Item("重命名…").Icon(icons["edit"]).Disabled(busy).Chosen() {
		s.openMaterialsRenameForm(entry.Path)
	}
	if menu.Item("复制到剪贴板").Icon(icons["copy"]).Disabled(busy).Chosen() {
		s.withMaterialsContextSelection(state, entry, s.copyMaterialsSelectionToClipboard)
	}
	if menu.Item("复制路径").Icon(icons["copy"]).Disabled(busy || state.pathCopyRequest != nil).Chosen() {
		s.withMaterialsContextSelection(state, entry, func() { s.copyMaterialsSelectionPaths(c.Services()) })
	}
	if !folder && menu.Item("复制为新文档…").Icon(icons["copy"]).Disabled(busy).Chosen() {
		s.openMaterialsCopyForm(entry.Path)
	}
	if materialsOpenWithAvailable() && !folder && menu.Item("选择应用打开").Icon(icons["launch"]).Disabled(systemBusy).Chosen() {
		s.act("system.open-with", s.currentTask().id, "", "", entry.Path, "", nil)
	}
	openBusy := systemBusy
	if folder {
		openBusy = s.materialsSystemRevealBusy(state)
	}
	if menu.Item("在系统中打开").Icon(icons["launch"]).Disabled(openBusy).Chosen() {
		s.act("document.open", s.currentTask().id, "", "", entry.Path, "", map[string]any{"desktopSystemOpenDirectory": folder})
	}
	if menu.Item("显示位置").Icon(icons["folder"]).Disabled(s.materialsSystemRevealBusy(state)).Chosen() {
		s.act("document.reveal", s.currentTask().id, "", "", entry.Path, "", nil)
	}
	menu.Separator()
	selectionCount := len(s.materialsContextSelection(state, entry))
	if menu.Item(fmt.Sprintf("复制已选项目 (%d)", selectionCount)).Icon(icons["copy"]).Disabled(busy || selectionCount == 0).Chosen() {
		s.withMaterialsContextSelection(state, entry, s.copyMaterialsSelectionToClipboard)
	}
	if menu.Item("移入回收站…").Icon(icons["delete"]).Disabled(busy || selectionCount == 0).Dangerous().Chosen() {
		s.withMaterialsContextSelection(state, entry, s.requestMaterialsTrashPreview)
	}
}

func (s *app) selectMaterialsDirectory(entry materialsFileSelectionEntry) {
	if entry.Directory {
		s.selectMaterialsPath(entry.Path)
		return
	}
	s.selectMaterialsPath(filepath.Dir(entry.Path))
}

func (s *app) openMaterialsFileNameForm(kind, title, directory string) {
	state := s.ensureMaterialsFileManagementState()
	if s.materialsFileInputBusy() || s.backend == nil || state.root == "" {
		if s.backend == nil {
			s.lastError = "应用后端不可用。"
		}
		return
	}
	fields := []formField{{key: "name", label: "文件名", placeholder: "例如：说明.txt 或 Dockerfile"}}
	if kind == "document.mkdir" {
		fields[0] = formField{key: "name", label: "文件夹名", value: "新建文件夹"}
	}
	if kind == "document.create" {
		fields = append(fields, formField{key: "text", label: "初始内容", multiline: true})
	}
	s.openForm(kind, title, state.taskID, "", "", directory, fields...)
}

func (s *app) openMaterialsRenameForm(path string) {
	if path == "" || s.materialsFileInputBusy() || s.backend == nil {
		return
	}
	state := s.ensureMaterialsFileManagementState()
	s.openForm("document.rename", "重命名", state.taskID, "", "", path,
		formField{key: "name", label: "新文件名", value: filepath.Base(path)})
}

func (s *app) openMaterialsCopyForm(path string) {
	if path == "" || s.materialsFileInputBusy() || s.backend == nil {
		return
	}
	state := s.ensureMaterialsFileManagementState()
	s.openForm("document.copy", "复制文档", state.taskID, "", "", path,
		formField{key: "name", label: "副本文件名"})
}

func (s *app) startMaterialsImportPicker() {
	state := s.ensureMaterialsFileManagementState()
	if state.pickerBusy || state.root == "" || s.win == nil || s.materialsFileInputBusy() || s.backend == nil {
		return
	}
	directory, relative := s.materialsCurrentDirectory(state)
	state.pickerBusy = true
	state.pickerGeneration++
	generation := state.pickerGeneration
	taskID, root, win := state.taskID, state.root, s.win
	win.Invalidate()
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: win, Title: "导入到任务资料", DefaultPath: directory,
			Message: "选择要复制到任务资料中的文件。", Multiple: true,
		})
		if win.IsDestroyed() {
			return
		}
		win.Update(func() {
			current := s.materialsFileManagement
			if current != state || state.pickerGeneration != generation || s.currentTask().id != taskID || normalizedReadingRoot(s.currentTask().materialsPath) != normalizedReadingRoot(root) {
				return
			}
			state.pickerBusy = false
			if err != nil {
				state.lastError = err.Error()
				s.lastError = err.Error()
				return
			}
			if len(paths) > 0 {
				s.startMaterialsImport(paths, "document.import", relative)
			}
		})
	}()
}

func (s *app) startMaterialsImport(paths []string, kind, relativeDirectory string) {
	state := s.ensureMaterialsFileManagementState()
	if s.materialsFileInputBusy() || s.backend == nil || state.root == "" || len(paths) == 0 {
		if s.backend == nil {
			s.lastError = "应用后端不可用。"
		}
		return
	}
	paths = distinctMaterialsSourcePaths(paths)
	if len(paths) == 0 {
		return
	}
	state.beginFlow(kind)
	state.focused = true
	s.act(kind, state.taskID, "", "", "", "", map[string]any{
		"directory": filepath.ToSlash(relativeDirectory),
		"paths":     paths,
	})
}

func distinctMaterialsSourcePaths(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	result := make([]string, 0, len(paths))
	for _, raw := range paths {
		path := filepath.Clean(strings.TrimSpace(raw))
		if path == "" || path == "." || !filepath.IsAbs(path) {
			continue
		}
		key := normalizedReadingPath(path)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, path)
	}
	return result
}

func (state *materialsFileManagementState) beginFlow(kind string) {
	state.flowActive = true
	state.flowOrigin = kind
	state.flowActionKind = kind
	state.flowTaskID = state.taskID
	state.flowRoot = state.root
	state.flowPath = ""
	state.flowCompleted = nil
	state.flowFailures = nil
	state.conflict = nil
	state.conflictAlternateName = ""
	state.conflictPending = nil
	state.lastError = ""
	state.resultRows = nil
}

func (s *app) copyMaterialsSelectionToClipboard() {
	state := s.ensureMaterialsFileManagementState()
	entries := s.materialsFileSelection(state)
	if len(entries) == 0 {
		return
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.Path)
	}
	if s.materialsFileInputBusy() || s.backend == nil {
		if s.backend == nil {
			s.lastError = "应用后端不可用。"
		}
		return
	}
	state.beginFlow("document.copy-files")
	s.act("document.copy-files", state.taskID, "", "", "", "", map[string]any{"paths": paths})
}

func (s *app) pasteMaterialsIntoCurrentDirectory() {
	state := s.ensureMaterialsFileManagementState()
	_, relative := s.materialsCurrentDirectory(state)
	if s.materialsFileInputBusy() || s.backend == nil {
		if s.backend == nil {
			s.lastError = "应用后端不可用。"
		}
		return
	}
	state.beginFlow("document.paste")
	s.act("document.paste", state.taskID, "", "", "", "", map[string]any{"directory": filepath.ToSlash(relative)})
}

func (s *app) requestMaterialsTrashPreview() {
	state := s.ensureMaterialsFileManagementState()
	entries := s.materialsFileSelection(state)
	if len(entries) == 0 || s.materialsFileInputBusy() || s.backend == nil {
		if s.backend == nil {
			s.lastError = "应用后端不可用。"
		}
		return
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.Path)
	}
	state.beginFlow("document.trash-preview")
	state.trashPreviewOpen = false
	state.trashPaths = nil
	state.trashPreviewPending = true
	s.act("document.trash-preview", state.taskID, "", "", "", "", map[string]any{"paths": paths})
}

func (s *app) confirmMaterialsTrash() {
	state := s.ensureMaterialsFileManagementState()
	if state.trashPreviewPending || len(state.trashPaths) == 0 || s.materialsFileInputBusy() || s.backend == nil {
		return
	}
	paths := append([]string(nil), state.trashPaths...)
	state.trashPreviewOpen = false
	state.beginFlow("document.trash")
	s.act("document.trash", state.taskID, "", "", "", "", map[string]any{"paths": paths, "confirmed": true})
}

func (s *app) cancelMaterialsTrashPreview() {
	state := s.ensureMaterialsFileManagementState()
	if state.trashPreviewPending {
		return
	}
	state.trashPreviewOpen = false
	state.trashPaths = nil
}

func (s *app) requestMaterialsConflictResolution(choice documents.FileConflictChoice, alternateName string) {
	state := s.ensureMaterialsFileManagementState()
	if state.conflict == nil || s.materialsFileInputBusy() || s.backend == nil {
		return
	}
	conflict := *state.conflict
	if conflict.Request.Kind == documents.FileOperationRename {
		if choice == documents.FileConflictCancel {
			state.conflict = nil
			state.conflictAlternateName = ""
			state.conflictPending = nil
			state.finishFlow(s, application.ActionResult{Message: "重命名已取消"}, nil)
			return
		}
		if choice == documents.FileConflictSaveAs {
			oldPath := filepath.Join(state.root, filepath.FromSlash(filepath.ToSlash(conflict.Request.RelativePath)))
			state.conflict = nil
			state.conflictAlternateName = ""
			state.conflictPending = nil
			state.flowActive = false
			state.flowActionKind = ""
			s.openForm("document.rename", "重命名", state.taskID, "", "", oldPath,
				formField{key: "name", label: "新文件名", value: alternateName})
			return
		}
		state.lastError = "重命名不能替换已有文件，请另存或取消。"
		s.lastError = state.lastError
		return
	}
	if choice == documents.FileConflictReplace && (!conflict.CanReplace || conflict.Request.Kind == documents.FileOperationRename) {
		state.lastError = "此文件冲突不能替换，请另存或取消。"
		s.lastError = state.lastError
		return
	}
	if choice == documents.FileConflictSaveAs {
		if err := validateMaterialsFileName(alternateName); err != nil {
			state.lastError = err.Error()
			s.lastError = state.lastError
			return
		}
	}
	state.conflictAlternateName = ""
	state.flowActive = true
	state.flowActionKind = "document.conflict-resolve"
	state.flowTaskID = state.taskID
	state.flowRoot = state.root
	state.lastError = ""
	s.lastError = ""
	s.act("document.conflict-resolve", state.taskID, "", "", "", "", map[string]any{
		"conflict":      conflict,
		"choice":        string(choice),
		"alternateName": alternateName,
		"pending":       append([]documents.FileOperationRequest(nil), state.conflictPending...),
	})
}

func (state *materialsFileManagementState) finishFlow(s *app, result application.ActionResult, resultErr error) {
	if state == nil {
		return
	}
	state.flowActive = false
	state.flowActionKind = ""
	state.conflict = nil
	state.conflictAlternateName = ""
	state.conflictPending = nil
	if resultErr != nil {
		state.lastError = resultErr.Error()
		s.lastError = resultErr.Error()
		return
	}
	state.lastError = ""
	state.resultText = strings.TrimSpace(result.Message)
	if state.resultText == "" {
		state.resultText = materialsFileSourceResultSummary(state.flowCompleted, state.flowFailures)
	}
	if state.resultText != "" {
		s.lastMessage = state.resultText
	}
	state.resultRows = state.resultRows[:0]
	for _, failure := range state.flowFailures {
		path := failure.Request.Name
		if failure.Request.RelativePath != "" {
			path = failure.Request.RelativePath
		}
		state.resultRows = append(state.resultRows, path+"："+failure.Message)
	}
	if len(state.flowFailures) > 0 {
		state.resultOpen = true
	}
	if len(state.flowCompleted) > 0 {
		last := state.flowCompleted[len(state.flowCompleted)-1]
		if last.RelativePath != "" && !last.IsDirectory {
			state.pendingSelectPath = filepath.Join(state.root, filepath.FromSlash(last.RelativePath))
		} else if last.RelativePath != "" && last.IsDirectory {
			state.pendingSelectDir = filepath.ToSlash(last.RelativePath)
		}
	}
	if state.flowOrigin == "document.trash" {
		paths := make([]string, 0, len(state.flowCompleted))
		for _, removed := range state.flowCompleted {
			if removed.RelativePath != "" {
				paths = append(paths, filepath.Join(state.root, filepath.FromSlash(removed.RelativePath)))
			}
		}
		s.removeMaterialTextEditors(paths)
		state.trashPaths = nil
		state.trashPreviewOpen = false
		state.pendingSelectPath = ""
		state.pendingSelectDir = ""
	}
	if state.flowOrigin == "document.copy-files" {
		state.pendingSelectPath, state.pendingSelectDir = "", ""
	}
	if state.flowOrigin == "document.trash-preview" {
		state.flowActive = false
		return
	}
	if (state.flowOrigin != "document.copy-files" && len(state.flowCompleted) > 0) || state.flowOrigin == "document.trash" {
		s.reloadMaterialsDocuments()
	}
}

func (s *app) reloadMaterialsDocuments() {
	if s.materialsFileReadBlocked() || s.backend == nil || s.currentTask().id == "" {
		return
	}
	s.documentsLoaded = false
	s.documentsRequested = false
	s.loadDocuments()
}

func (s *app) applyPendingMaterialsFileSelection() {
	state := s.materialsFileManagement
	if state == nil || s.currentTask().id != state.taskID || normalizedReadingRoot(s.currentTask().materialsPath) != normalizedReadingRoot(state.root) {
		return
	}
	if state.pendingSelectDir != "" {
		relative := state.pendingSelectDir
		state.pendingSelectDir = ""
		s.setReadingDirectoryExpanded(relative, true)
		absolute := filepath.Join(state.root, filepath.FromSlash(relative))
		if entry, ok := s.materialsEntryForDocument(application.Document{Path: absolute, Name: filepath.Base(absolute), Kind: "directory"}); ok {
			state.selected = map[string]materialsFileSelectionEntry{entry.Key: entry}
			state.anchor = entry.Key
		}
	}
	if state.pendingSelectPath != "" {
		path := state.pendingSelectPath
		state.pendingSelectPath = ""
		s.selectMaterialsPath(path)
	}
}

func (s *app) reconcileMaterialsDocuments(entries []application.Document) {
	state := s.materialsFileManagement
	current := s.currentTask()
	stateBelongsToTask := state != nil && state.taskID == current.id && normalizedReadingRoot(state.root) == normalizedReadingRoot(current.materialsPath)
	available := make(map[string]materialsFileSelectionEntry)
	anchorAvailable := false
	var visit func([]application.Document)
	visit = func(items []application.Document) {
		for _, document := range items {
			if entry, ok := s.materialsEntryForDocument(document); ok {
				available[normalizedReadingPath(entry.Path)] = entry
				if stateBelongsToTask && entry.Key == state.anchor {
					anchorAvailable = true
				}
			}
			visit(document.Children)
		}
	}
	visit(entries)
	if stateBelongsToTask {
		if _, exists := available[normalizedReadingPath(state.previewPath)]; !exists {
			state.previewPath = ""
		}
		for key, entry := range state.selected {
			if current, ok := available[normalizedReadingPath(entry.Path)]; !ok || current.Directory != entry.Directory {
				delete(state.selected, key)
			} else {
				state.selected[key] = current
			}
		}
		if !anchorAvailable {
			state.anchor = ""
		}
	}
	oldPath := s.documentPath
	oldTabs := append([]string(nil), s.documentTabs...)
	filteredTabs := make([]string, 0, len(oldTabs))
	for _, path := range oldTabs {
		if _, ok := available[normalizedReadingPath(path)]; ok {
			filteredTabs = append(filteredTabs, path)
		}
	}
	if stateBelongsToTask && state.pendingEditPath != "" {
		if _, ok := available[normalizedReadingPath(state.pendingEditPath)]; !ok {
			state.pendingEditPath = ""
		}
	}
	if oldPath == "" {
		s.documentTabs = filteredTabs
		return
	}
	if _, ok := available[normalizedReadingPath(oldPath)]; ok {
		s.documentTabs = filteredTabs
		return
	}
	s.closeNativeMedia()
	s.closeMarkdownEditor()
	s.documentPath, s.documentContent, s.documentHash = "", "", ""
	s.documentKind, s.documentReadOnly = "", false
	s.documentDirty, s.documentEditing, s.documentReadPending = false, false, false
	delete(s.documentDrafts, oldPath)
	delete(s.documentDirtyMap, oldPath)
	s.documentTabs = filteredTabs
	if stateBelongsToTask && normalizedReadingPath(state.pendingEditPath) == normalizedReadingPath(oldPath) {
		state.pendingEditPath = ""
	}

	removedTabIndex := -1
	for index, path := range oldTabs {
		if normalizedReadingPath(path) == normalizedReadingPath(oldPath) {
			removedTabIndex = index
			break
		}
	}
	selected := ""
	if removedTabIndex >= 0 {
		for index := removedTabIndex + 1; index < len(oldTabs); index++ {
			if entry, ok := available[normalizedReadingPath(oldTabs[index])]; ok {
				selected = entry.Path
				break
			}
		}
		for index := removedTabIndex - 1; selected == "" && index >= 0; index-- {
			if entry, ok := available[normalizedReadingPath(oldTabs[index])]; ok {
				selected = entry.Path
			}
		}
	} else if len(filteredTabs) > 0 {
		if entry, ok := available[normalizedReadingPath(filteredTabs[0])]; ok {
			selected = entry.Path
		}
	}
	if selected == "" {
		selected = firstDocument(entries)
	}
	if entry, ok := available[normalizedReadingPath(selected)]; ok {
		s.selectDocument(entry.Path)
	}
}

func (s *app) applyMaterialsFileReadResult(action application.Action) {
	state := s.materialsFileManagement
	if state == nil || action.TaskID != state.taskID || action.Path != state.pendingEditPath {
		return
	}
	state.pendingEditPath = ""
}

func (s *app) applyMaterialsFileActionFailure(action application.Action, err error) bool {
	if !isMaterialsFileManagementAction(action.Kind) {
		return false
	}
	state := s.materialsFileManagement
	if state == nil || action.TaskID != state.taskID || action.TaskID != s.currentTask().id {
		return true
	}
	state.flowActionKind = ""
	state.flowActive = action.Kind == "document.conflict-resolve" && state.conflict != nil
	state.trashPreviewPending = false
	if action.Kind == "document.trash-preview" {
		state.trashPreviewOpen = false
		state.trashPaths = nil
	}
	state.lastError = err.Error()
	s.lastError = err.Error()
	return true
}

func isMaterialsFileManagementAction(kind string) bool {
	switch kind {
	case "document.create", "document.mkdir", "document.copy", "document.copy-files", "document.import", "document.drop", "document.paste", "document.conflict-resolve", "document.trash-preview", "document.trash":
		return true
	default:
		return false
	}
}

func (s *app) applyMaterialsFileActionResult(action application.Action, result application.ActionResult) bool {
	if action.Kind == "document.rename" {
		operation, err := decodeSingleMaterialsData[documents.FileOperationResult](result.Data)
		state := s.materialsFileManagement
		stateMatchesCurrent := state != nil && action.TaskID == s.currentTask().id && action.TaskID == state.taskID &&
			normalizedReadingRoot(s.currentTask().materialsPath) == normalizedReadingRoot(state.root)
		if err != nil {
			if stateMatchesCurrent {
				state.lastError = "重命名结果无法读取：" + err.Error()
				s.lastError = state.lastError
			}
			return true
		}
		if operation.Status == documents.FileOperationConflicted && operation.Conflict != nil {
			if stateMatchesCurrent {
				state.flowActionKind = ""
				state.lastError = ""
				state.beginFlow("document.rename")
				state.flowPath = action.Path
				state.conflict = operation.Conflict
				state.conflictAlternateName = operation.Conflict.SuggestedName
				state.conflictPending = nil
			}
			return true
		}
		if operation.Status == documents.FileOperationCompleted {
			// The original bridge remaps per-task reading state even when the user
			// has since selected another task. Only its visible editor updates are
			// guarded by current-task identity.
			s.applyMaterialsRenameResult(action, result)
			if stateMatchesCurrent {
				state.flowActionKind = ""
				state.lastError = ""
			}
		}
		return true
	}
	if !isMaterialsFileManagementAction(action.Kind) {
		return false
	}
	state := s.materialsFileManagement
	if state == nil || action.TaskID != s.currentTask().id || action.TaskID != state.taskID || normalizedReadingRoot(s.currentTask().materialsPath) != normalizedReadingRoot(state.root) {
		return true
	}
	state.flowActionKind = ""
	state.lastError = ""
	switch action.Kind {
	case "document.copy-files":
		state.finishFlow(s, result, nil)
		return true
	case "document.trash-preview":
		state.trashPreviewPending = false
		preview, err := decodeSingleMaterialsData[struct {
			Paths                []string `json:"paths"`
			RequiresConfirmation bool     `json:"requiresConfirmation"`
		}](result.Data)
		if err != nil {
			state.flowActive = false
			state.lastError = "删除预览结果无法读取：" + err.Error()
			s.lastError = state.lastError
			return true
		}
		if !preview.RequiresConfirmation || len(preview.Paths) == 0 {
			state.flowActive = false
			state.lastError = "后端没有返回可确认的删除路径，请刷新资料后重试。"
			s.lastError = state.lastError
			return true
		}
		state.trashPaths = append([]string(nil), preview.Paths...)
		state.trashPreviewOpen = true
		state.flowActive = false
		return true
	case "document.trash":
		outcomes, err := decodeData[documents.TrashItemResult](result.Data)
		if err != nil {
			state.lastError = "回收站结果无法读取：" + err.Error()
			s.lastError = state.lastError
			return true
		}
		state.flowOrigin = "document.trash"
		state.flowCompleted = nil
		state.flowFailures = nil
		for _, outcome := range outcomes {
			relative := s.materialsRelativePath(outcome.Path)
			if outcome.Error != "" || outcome.Cancelled {
				message := outcome.Error
				if outcome.Cancelled {
					message = "系统回收操作已取消"
				}
				state.flowFailures = append(state.flowFailures, documents.FileOperationFailure{
					Request: documents.FileOperationRequest{RelativePath: relative}, Message: message,
				})
				continue
			}
			state.flowCompleted = append(state.flowCompleted, documents.FileOperationResult{Status: documents.FileOperationCompleted, RelativePath: relative})
		}
		state.finishFlow(s, result, nil)
		return true
	}
	if action.Kind == "document.create" || action.Kind == "document.mkdir" || action.Kind == "document.copy" {
		operation, err := decodeSingleMaterialsData[documents.FileOperationResult](result.Data)
		if err != nil {
			state.lastError = "文件操作结果无法读取：" + err.Error()
			s.lastError = state.lastError
			return true
		}
		state.beginFlow(action.Kind)
		state.flowTaskID, state.flowRoot = action.TaskID, state.root
		s.acceptMaterialsFileOperation(result, operation, nil)
		return true
	}
	if action.Kind == "document.trash-preview" || action.Kind == "document.trash" {
		return true
	}
	batch, operation, single, err := decodeMaterialsFileOperationResult(result.Data)
	if err != nil {
		state.lastError = "文件操作结果无法读取：" + err.Error()
		s.lastError = state.lastError
		return true
	}
	if !state.flowActive || state.flowTaskID != action.TaskID || normalizedReadingRoot(state.flowRoot) != normalizedReadingRoot(state.root) {
		state.beginFlow(action.Kind)
	}
	state.flowActionKind = action.Kind
	if single {
		s.acceptMaterialsFileOperation(result, operation, nil)
	} else {
		s.acceptMaterialsFileOperation(result, documents.FileOperationResult{}, &batch)
	}
	return true
}

func decodeMaterialsFileOperationResult(data any) (documents.FileOperationBatchResult, documents.FileOperationResult, bool, error) {
	encoded, err := json.Marshal(data)
	if err != nil {
		return documents.FileOperationBatchResult{}, documents.FileOperationResult{}, false, err
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &shape); err != nil {
		return documents.FileOperationBatchResult{}, documents.FileOperationResult{}, false, err
	}
	if _, ok := shape["status"]; ok {
		var operation documents.FileOperationResult
		if err := json.Unmarshal(encoded, &operation); err != nil {
			return documents.FileOperationBatchResult{}, documents.FileOperationResult{}, true, err
		}
		return documents.FileOperationBatchResult{}, operation, true, nil
	}
	var batch documents.FileOperationBatchResult
	if err := json.Unmarshal(encoded, &batch); err != nil {
		return documents.FileOperationBatchResult{}, documents.FileOperationResult{}, false, err
	}
	if batch.Completed == nil {
		batch.Completed = []documents.FileOperationResult{}
	}
	if batch.Failures == nil {
		batch.Failures = []documents.FileOperationFailure{}
	}
	return batch, documents.FileOperationResult{}, false, nil
}

func decodeSingleMaterialsData[T any](data any) (T, error) {
	var value T
	if data == nil {
		return value, errors.New("操作结果为空")
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal(encoded, &value); err != nil {
		return value, err
	}
	return value, nil
}

func (s *app) acceptMaterialsFileOperation(result application.ActionResult, operation documents.FileOperationResult, batch *documents.FileOperationBatchResult) {
	state := s.materialsFileManagement
	if state == nil {
		return
	}
	if batch != nil {
		state.flowCompleted = append(state.flowCompleted, batch.Completed...)
		state.flowFailures = append(state.flowFailures, batch.Failures...)
		if batch.Conflict != nil {
			conflict := *batch.Conflict
			state.conflict = &conflict
			state.conflictAlternateName = conflict.SuggestedName
			state.conflictPending = append([]documents.FileOperationRequest(nil), batch.Pending...)
			state.flowActive = true
			state.flowActionKind = "document.conflict-resolve"
			return
		}
		state.finishFlow(s, result, nil)
		return
	}
	switch operation.Status {
	case documents.FileOperationConflicted:
		if operation.Conflict == nil {
			state.lastError = "文件冲突结果缺少冲突信息，请重新执行操作。"
			s.lastError = state.lastError
			return
		}
		conflict := *operation.Conflict
		state.conflict = &conflict
		state.conflictAlternateName = conflict.SuggestedName
		state.conflictPending = nil
		state.flowActive = true
		state.flowActionKind = "document.conflict-resolve"
	case documents.FileOperationCompleted:
		state.flowCompleted = append(state.flowCompleted, operation)
		state.finishFlow(s, result, nil)
	case documents.FileOperationCancelled:
		state.finishFlow(s, result, nil)
	default:
		state.lastError = "文件操作返回未知状态，请刷新资料后确认结果。"
		s.lastError = state.lastError
	}
}

func (s *app) renderMaterialsFileManagementDialogs(c *ui.Context, p palette) {
	state := s.materialsFileManagement
	if state == nil {
		return
	}
	if state.conflict != nil {
		s.renderMaterialsFileConflictDialog(c, p, state)
	}
	if state.trashPreviewOpen {
		s.renderMaterialsTrashPreviewDialog(c, p, state)
	}
	if state.resultOpen {
		s.renderMaterialsFileResultDialog(c, p, state)
	}
}

func (s *app) renderMaterialsFileConflictDialog(c *ui.Context, p palette, state *materialsFileManagementState) {
	conflict := state.conflict
	if conflict == nil {
		return
	}
	open := true
	dialog := ui.DialogBase(c.Key("materials-file-conflict"), &open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .34))
		width, _ := c.Size()
		panel.Width(maxFloat32(380, minFloat32(560, width-64))).MaxHeight(560).Padding(18).Gap(12).Background(p.surface).Border(1, p.line).Radius(12)
		panel.OnShortcut(0, ui.KeyEscape, func() { s.requestMaterialsConflictResolution(documents.FileConflictCancel, "") })
		panel.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				label(c, "同名项已存在", 16, p.text).FontWeight(600).Grow(1)
				if action(c, "materials-file-conflict-close", "close", "取消本项", p, false) {
					s.requestMaterialsConflictResolution(documents.FileConflictCancel, "")
				}
			})
			name := conflict.Request.Name
			if name == "" {
				name = conflict.SuggestedName
			}
			label(c, fmt.Sprintf("“%s”已存在。可以另存为新名称，或取消当前项。", name), 12, p.muted)
			if conflict.Request.Directory != "" || conflict.Request.RelativePath != "" {
				parent := conflict.Request.Directory
				if conflict.Request.RelativePath != "" {
					parent = filepath.ToSlash(filepath.Dir(filepath.FromSlash(conflict.Request.RelativePath)))
				}
				if parent != "" && parent != "." {
					label(c, "目标目录："+filepath.Join(state.root, filepath.FromSlash(parent)), 11, p.muted).SingleLine()
				}
			}
			if conflict.CanReplace && conflict.Request.Kind != documents.FileOperationRename {
				label(c, "替换会覆盖目标位置中的现有内容。", 11, p.amber)
			}
			if state.conflictAlternateName == "" {
				state.conflictAlternateName = conflict.SuggestedName
			}
			ui.TextInput(c.Key("materials-file-conflict-name"), &state.conflictAlternateName).FillWidth().Height(36).Placeholder("另存为").AutoFocus()
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				ui.Spacer(c)
				if action(c, "materials-file-conflict-cancel", "close", "取消", p, false) {
					s.requestMaterialsConflictResolution(documents.FileConflictCancel, "")
				}
				if conflict.CanReplace && conflict.Request.Kind != documents.FileOperationRename && action(c, "materials-file-conflict-replace", "refresh", "替换", p, false) {
					s.requestMaterialsConflictResolution(documents.FileConflictReplace, state.conflictAlternateName)
				}
				if action(c, "materials-file-conflict-save-as", "save", "另存为", p, true) {
					s.requestMaterialsConflictResolution(documents.FileConflictSaveAs, state.conflictAlternateName)
				}
			})
			if state.lastError != "" {
				label(c, state.lastError, 11, p.amber)
			}
		})
	})
	if dialog.Dismissed() && state.conflict != nil && !s.materialsFileInputBusy() {
		s.requestMaterialsConflictResolution(documents.FileConflictCancel, "")
	}
}

func (s *app) renderMaterialsTrashPreviewDialog(c *ui.Context, p palette, state *materialsFileManagementState) {
	open := state.trashPreviewOpen || state.trashPreviewPending
	dialog := ui.DialogBase(c.Key("materials-trash-preview"), &open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .34))
		width, _ := c.Size()
		panel.Width(maxFloat32(380, minFloat32(620, width-64))).MaxHeight(600).Padding(18).Gap(12).Background(p.surface).Border(1, p.line).Radius(12)
		panel.OnShortcut(0, ui.KeyEscape, func() { s.cancelMaterialsTrashPreview() })
		panel.Children(func() {
			label(c, "移入系统回收站？", 16, p.text).FontWeight(600)
			if state.trashPreviewPending {
				label(c, "正在校验所选路径…", 12, p.muted)
			} else {
				label(c, fmt.Sprintf("所选 %d 项将移入系统回收站，文件夹包含内部内容。", len(state.trashPaths)), 12, p.muted).Wrap()
				ui.Scroll(c).FillWidth().Height(minFloat32(300, float32(24*len(state.trashPaths)+12))).Gap(3).Children(func() {
					for index, path := range state.trashPaths {
						label(c, materialsTrashDisplayPath(state.root, path), 11, p.text).Wrap().Tooltip(path)
						if index != len(state.trashPaths)-1 {
							ui.Divider(c)
						}
					}
				})
			}
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				ui.Spacer(c)
				if action(c, "materials-trash-cancel", "close", "取消", p, false) {
					s.cancelMaterialsTrashPreview()
				}
				if action(c, "materials-trash-confirm", "trash", "移入回收站", p, true) {
					s.confirmMaterialsTrash()
				}
			})
		})
	})
	if dialog.Dismissed() && !state.trashPreviewPending {
		s.cancelMaterialsTrashPreview()
	}
}

func materialsTrashDisplayPath(root, path string) string {
	root = strings.TrimSpace(root)
	path = strings.TrimSpace(path)
	if root == "" || path == "" {
		return path
	}
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return path
	}
	return filepath.ToSlash(relative)
}

func (s *app) renderMaterialsFileResultDialog(c *ui.Context, p palette, state *materialsFileManagementState) {
	dialog := ui.DialogBase(c.Key("materials-file-result"), &state.resultOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .34))
		width, _ := c.Size()
		panel.Width(maxFloat32(380, minFloat32(620, width-64))).MaxHeight(560).Padding(18).Gap(12).Background(p.surface).Border(1, p.line).Radius(12)
		panel.OnShortcut(0, ui.KeyEscape, func() { state.resultOpen = false })
		panel.Children(func() {
			label(c, "部分资料操作未完成", 16, p.text).FontWeight(600)
			label(c, state.resultText, 12, p.muted)
			ui.Scroll(c).FillWidth().Height(minFloat32(320, float32(25*len(state.resultRows)+12))).Gap(5).Children(func() {
				for _, row := range state.resultRows {
					label(c, row, 11, p.amber).Wrap()
				}
			})
			ui.Row(c).FillWidth().Children(func() {
				ui.Spacer(c)
				if action(c, "materials-file-result-close", "check", "完成", p, true) {
					state.resultOpen = false
				}
			})
		})
	})
	if dialog.Dismissed() {
		state.resultOpen = false
	}
}

func materialsFileManagementActionLabel(kind string) string {
	switch kind {
	case "document.create":
		return "新建文档"
	case "document.mkdir":
		return "新建文件夹"
	case "document.copy-files":
		return "复制所选项"
	case "document.import", "document.drop":
		return "导入资料"
	case "document.paste":
		return "粘贴资料"
	case "document.trash-preview", "document.trash":
		return "移入回收站"
	case "document.conflict-resolve":
		return "处理文件冲突"
	default:
		return kind
	}
}

func materialsFileSourceResultSummary(completed []documents.FileOperationResult, failures []documents.FileOperationFailure) string {
	return fmt.Sprintf("完成 %d 项，失败 %d 项", len(completed), len(failures))
}

func decodeMaterialsPaths(data any) ([]string, error) {
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var paths []string
	if err := json.Unmarshal(encoded, &paths); err != nil {
		return nil, err
	}
	return paths, nil
}

func validateMaterialsFileName(name string) error {
	if err := documents.ValidateFileName(name); err != nil {
		return fmt.Errorf("文件名无效：%w", err)
	}
	return nil
}

func normalizeMaterialsFileDialogPath(root, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("文件路径不能为空")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("文件路径超出任务资料目录")
	}
	return filepath.Clean(absolute), nil
}

func materialsOpenWithAvailable() bool {
	return runtime.GOOS == "windows"
}
