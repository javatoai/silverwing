package desktop

import (
	"errors"
	"path/filepath"

	"silverwing-go/internal/application"
	"silverwing-go/internal/workspace"
)

type markdownTaskRootInstructionEditor struct {
	oldKey, newKey, path string
	state                *markdownEditorState
	taskIndex, tab       int
}

type markdownTaskRootMigrationUI struct {
	tasks                      []task
	snapshotTasks              []application.Task
	documents                  []application.Document
	path                       string
	tabs                       []string
	drafts                     map[string]string
	dirty                      map[string]bool
	notePaths                  [3]string
	markdownPath, markdownRoot string
	instructions               []markdownTaskRootInstructionEditor
}

func rebaseMarkdownTaskRootEntries[V comparable](entries map[string]V, mappings []workspace.TaskRootMigrationPathMapping) (map[string]V, error) {
	result := make(map[string]V, len(entries))
	for path, value := range entries {
		path, _, err := workspace.RebaseTaskRootMigrationPath(path, mappings)
		if err != nil {
			return nil, err
		}
		if previous, exists := result[path]; exists && previous != value {
			return nil, errors.New("任务迁移后的 Markdown 路径状态冲突")
		}
		result[path] = value
	}
	return result, nil
}

func rebaseMarkdownTaskRootDocuments(items []application.Document, mappings []workspace.TaskRootMigrationPathMapping) ([]application.Document, error) {
	result := append([]application.Document(nil), items...)
	for index := range result {
		var err error
		result[index].Path, _, err = workspace.RebaseTaskRootMigrationPath(result[index].Path, mappings)
		if err != nil {
			return nil, err
		}
		result[index].Children, err = rebaseMarkdownTaskRootDocuments(result[index].Children, mappings)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *app) buildMarkdownTaskRootMigrationUI(plan *markdownTaskRootMigrationPlan) (*markdownTaskRootMigrationUI, error) {
	state := &markdownTaskRootMigrationUI{tasks: append([]task(nil), s.taskUnit.state.tasks...), snapshotTasks: append([]application.Task(nil), s.snapshot.Tasks...), tabs: append([]string(nil), s.materialsUnit.state.documentTabs...), notePaths: s.taskUnit.state.notePaths}
	mappings := plan.preview.PathMappings
	rebase := func(path string) (string, error) {
		value, _, err := workspace.RebaseTaskRootMigrationPath(path, mappings)
		return value, err
	}
	var err error
	for index := range state.tasks {
		state.tasks[index].path, err = rebase(state.tasks[index].path)
		if err != nil {
			return nil, err
		}
		state.tasks[index].materialsPath, err = rebase(state.tasks[index].materialsPath)
		if err != nil {
			return nil, err
		}
	}
	for index := range state.snapshotTasks {
		state.snapshotTasks[index].Path, err = rebase(state.snapshotTasks[index].Path)
		if err != nil {
			return nil, err
		}
		state.snapshotTasks[index].MaterialsPath, err = rebase(state.snapshotTasks[index].MaterialsPath)
		if err != nil {
			return nil, err
		}
	}
	state.documents, err = rebaseMarkdownTaskRootDocuments(s.materialsUnit.state.documents, mappings)
	if err != nil {
		return nil, err
	}
	state.path, err = rebase(s.materialsUnit.state.documentPath)
	if err != nil {
		return nil, err
	}
	for index, path := range state.tabs {
		state.tabs[index], err = rebase(path)
		if err != nil {
			return nil, err
		}
	}
	state.drafts, err = rebaseMarkdownTaskRootEntries(s.materialsUnit.state.documentDrafts, mappings)
	if err != nil {
		return nil, err
	}
	state.dirty, err = rebaseMarkdownTaskRootEntries(s.materialsUnit.state.documentDirtyMap, mappings)
	if err != nil {
		return nil, err
	}
	if s.materialsUnit.state.markdown != nil {
		state.markdownPath, err = rebase(s.materialsUnit.state.markdown.path)
		if err != nil {
			return nil, err
		}
		state.markdownRoot, err = rebase(s.materialsUnit.state.markdown.linkRoot)
		if err != nil {
			return nil, err
		}
	}
	for index, path := range state.notePaths {
		state.notePaths[index], err = rebase(path)
		if err != nil {
			return nil, err
		}
	}
	if notes := s.taskUnit.state.taskNotesUI; notes != nil {
		for _, entry := range plan.entries {
			if entry.before.kind != "instruction" {
				continue
			}
			old := notes.markdownEditors[entry.oldKey]
			if old == nil {
				continue
			}
			if notes.markdownEditors[entry.newKey] != nil && entry.newKey != entry.oldKey {
				return nil, errors.New("迁移目标已有说明编辑器")
			}
			taskIndex, tab := -1, -1
			for index, item := range state.tasks {
				if item.id == old.taskID {
					taskIndex = index
					break
				}
			}
			for index, name := range taskNoteNames {
				if filepath.Base(old.path) == name {
					tab = index
					break
				}
			}
			if taskIndex < 0 || tab < 0 || old.doc == nil {
				return nil, errors.New("无法确认任务说明编辑器的迁移身份")
			}
			state.instructions = append(state.instructions, markdownTaskRootInstructionEditor{entry.oldKey, entry.newKey, entry.after.path, old, taskIndex, tab})
		}
	}
	return state, nil
}

func (s *app) applyMarkdownTaskRootMigrationUI(plan *markdownTaskRootMigrationPlan, state *markdownTaskRootMigrationUI) {
	oldPath := s.materialsUnit.state.documentPath
	s.taskUnit.replaceTasks(state.tasks, s.taskUnit.state.selected)
	s.snapshot.Tasks = state.snapshotTasks
	s.materialsUnit.replaceDocuments(state.documents)
	s.materialsUnit.state.documentPath, s.materialsUnit.state.documentTabs = state.path, state.tabs
	s.materialsUnit.state.documentDrafts, s.materialsUnit.state.documentDirtyMap = state.drafts, state.dirty
	s.taskUnit.state.notePaths = state.notePaths
	if s.materialsUnit.state.markdown != nil {
		s.materialsUnit.state.markdown.path, s.materialsUnit.state.markdown.linkRoot = state.markdownPath, state.markdownRoot
	}
	if notes := s.taskUnit.state.taskNotesUI; notes != nil {
		for _, entry := range state.instructions {
			old := entry.state
			// The existing constructor regenerates closures over the new key.
			// Temporarily expose that editor's task/tab only to this constructor;
			// restore the current selection before leaving the window callback.
			selected, paths := s.taskUnit.state.selected, s.taskUnit.state.notePaths
			s.taskUnit.state.selected = entry.taskIndex
			s.taskUnit.state.notePaths[entry.tab] = entry.path
			delete(notes.markdownEditors, entry.oldKey)
			fresh := s.ensureInstructionMarkdownEditor(entry.newKey, "task", entry.tab, old.doc.Source())
			s.taskUnit.state.selected, s.taskUnit.state.notePaths = selected, paths
			binding := fresh.binding
			suspendInstructionMarkdownPreviews(old)
			*fresh = *old
			fresh.path, fresh.binding, fresh.closed = entry.path, binding, false
			// Native replacement callbacks captured the old host. Recreate only
			// view fields; retain the original Document, cursor and source history.
			fresh.richFields = nil
			fresh.structuredRichFields = nil
			fresh.selectedStructuredRich = nil
			fresh.editFields = nil
			fresh.richLink = nil
			fresh.lastFocusRequest = 0
			if cursor := fresh.doc.Snapshot().Cursor; cursor != nil {
				fresh.doc.RequestFocusCursor(cursor)
			}
			old.closed = true
			if notes.activeMarkdown == old {
				notes.activeMarkdown = fresh
			}
			if notes.activeKey == entry.oldKey {
				notes.activeKey = entry.newKey
			}
		}
		if key, ok := plan.moves[notes.activeKey]; ok {
			notes.activeKey = key
		}
	}
	if state.path != oldPath && (s.materialsUnit.state.documentKind == "image" || s.materialsUnit.state.documentKind == "pdf") {
		s.materialsUnit.closeNativeMedia()
		s.openNativeMedia(state.path, s.materialsUnit.state.documentKind)
	}
	if files := s.materialsUnit.state.materialsFileManagement; files != nil {
		// Watcher ownership is stopped/joined by the dispatch barrier. Let the
		// existing management-state initializer bind its new task/root.
		s.ensureMaterialsFileManagementState()
	}
}
