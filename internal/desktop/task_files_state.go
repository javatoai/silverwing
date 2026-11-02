package desktop

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"silverwing-go/internal/application"
	markdownui "silverwing-go/internal/ui/markdown"

	"github.com/egoist/mygo/ui"
	code "github.com/egoist/mygo/ui/editor"
)

// A view belongs to one task incarnation and one configured root. In particular,
// an ID reused after deletion must not inherit the previous task's responses.
type taskFilesIdentity struct {
	home, id, createdAt, taskPath, taskRoot string
	archived                                bool
}

func (id taskFilesIdentity) action(kind, relative string) application.Action {
	return application.Action{Kind: kind, TaskID: id.id, Path: relative, Payload: map[string]any{
		"taskFilesContext": application.TaskFilesContext{Home: id.home, TaskPath: id.taskPath,
			TaskCreatedAt: id.createdAt, TaskRoot: id.taskRoot, Archived: id.archived},
	}}
}

func (id taskFilesIdentity) documentID(relative string) string {
	return fmt.Sprintf("task-files:%x", sha256.Sum256([]byte(fmt.Sprintf("%q/%q/%q/%q/%q/%t:%q",
		id.home, id.id, id.createdAt, id.taskPath, id.taskRoot, id.archived, relative))))
}

type taskFilesDirectoryState struct {
	entries         []application.TaskFileEntry
	loaded, loading bool
	error           string
}

type taskFilesPosition struct {
	reader     markdownui.ReaderViewState
	code       code.ScrollPosition
	scroll     ui.ScrollState
	page, zoom int
}

type taskFilesView struct {
	identity                                      taskFilesIdentity
	active                                        bool
	epoch                                         uint64
	root                                          string
	directories                                   map[string]*taskFilesDirectoryState
	expanded                                      map[string]bool
	selected                                      string
	opened                                        string
	positions                                     map[string]*taskFilesPosition
	preview                                       *taskFilesPreview
	tree                                          FileTreeState
	treeWidth                                     float32
	canvas                                        ui.Handle
	revealPath                                    string
	query, searchQuery                            string
	searching, searchDone                         bool
	search                                        application.TaskFilesSearchResult
	searchError, operationError, operationMessage string
	searchScroll                                  ui.ScrollState
}

type taskFilesController struct {
	views   map[taskFilesIdentity]*taskFilesView
	current *taskFilesView
	jobs    taskFilesJobs
	stopped bool
}

func (s *app) taskFilesIdentity() taskFilesIdentity {
	t := s.taskUnit.currentTask()
	taskRoot := ""
	if cfg, err := s.readSnapshotAppConfig(); err == nil && cfg.TaskRoot != nil {
		taskRoot = *cfg.TaskRoot
	}
	return taskFilesIdentity{home: s.snapshot.Home, id: t.id, createdAt: t.createdAt,
		taskPath: t.path, taskRoot: taskRoot, archived: t.archived}
}

func (s *app) taskFilesPageActive() bool {
	return (s.page == "tasks" || s.page == "archive") && s.tab == taskFilesTab && !s.workspaceReviewPageVisible()
}

func (s *app) taskFilesCurrent(view *taskFilesView) bool {
	return view != nil && !s.taskFilesUnit.stopped && s.taskFilesPageActive() && view.active &&
		s.taskFilesUnit.current == view && view.identity == s.taskFilesIdentity()
}

func newTaskFilesView(identity taskFilesIdentity) *taskFilesView {
	return &taskFilesView{identity: identity, directories: map[string]*taskFilesDirectoryState{},
		expanded: map[string]bool{}, positions: map[string]*taskFilesPosition{}, treeWidth: fileTreeWidth}
}

func (view *taskFilesView) directory(relative string) *taskFilesDirectoryState {
	dir := view.directories[relative]
	if dir == nil {
		dir = &taskFilesDirectoryState{}
		view.directories[relative] = dir
	}
	return dir
}

func (view *taskFilesView) position(relative string) *taskFilesPosition {
	position := view.positions[relative]
	if position == nil {
		position = &taskFilesPosition{zoom: 100}
		view.positions[relative] = position
	}
	return position
}

func (view *taskFilesView) entry(relative string) (application.TaskFileEntry, bool) {
	parent := path.Dir(relative)
	if parent == "." {
		parent = ""
	}
	if dir := view.directories[parent]; dir != nil {
		for _, entry := range dir.entries {
			if entry.Path == relative {
				return entry, true
			}
		}
	}
	for _, entry := range view.search.Entries {
		if entry.Path == relative {
			return entry, true
		}
	}
	return application.TaskFileEntry{}, false
}

func (s *app) enterTaskFiles() { s.ensureTaskFilesCurrent() }

func (s *app) ensureTaskFilesCurrent() *taskFilesView {
	unit := &s.taskFilesUnit
	if unit.stopped || !s.taskFilesPageActive() {
		return nil
	}
	identity := s.taskFilesIdentity()
	if identity.id == "" || identity.taskPath == "" {
		s.leaveTaskFiles()
		return nil
	}
	if unit.current != nil && unit.current.identity != identity {
		s.leaveTaskFiles()
	}
	if unit.views == nil {
		unit.views = make(map[taskFilesIdentity]*taskFilesView)
	}
	view := unit.views[identity]
	if view == nil {
		view = newTaskFilesView(identity)
		unit.views[identity] = view
	}
	unit.current = view
	if !view.active {
		view.active = true
		view.epoch++
		// Returning refreshes just the root and the active preview. Previously
		// expanded directories are read individually as they become visible.
		for _, dir := range view.directories {
			dir.loaded, dir.loading = false, false
		}
		s.requestTaskFilesDirectory(view, "", true)
		if view.opened != "" {
			selected := view.selected
			s.openTaskFilePreview(view, view.opened, true)
			view.selected = selected
		}
	}
	return view
}

func (s *app) leaveTaskFiles() {
	unit := &s.taskFilesUnit
	view := unit.current
	if view == nil || !view.active {
		return
	}
	view.active = false
	view.epoch++
	unit.jobs.cancelView(view)
	view.searching = false
	for _, dir := range view.directories {
		dir.loading = false
	}
	view.retirePreview(&unit.jobs)
}

func (unit *taskFilesController) stopTaskFiles() {
	if unit.stopped {
		return
	}
	unit.stopped = true
	unit.jobs.stop()
	for _, view := range unit.views {
		view.active = false
		view.epoch++
		view.retirePreview(&unit.jobs)
	}
}

func (unit *taskFilesController) closeTaskFiles(ctx context.Context) error {
	unit.stopTaskFiles()
	return unit.jobs.close(ctx)
}

// Paths in UI state are canonical slash-separated relative paths. This check is
// defensive only; the backend remains authoritative for filesystem access.
func taskFilesRelative(raw string) (string, bool) {
	if raw == "" {
		return "", true
	}
	if strings.ContainsAny(raw, "\x00\r\n:") || strings.Contains(raw, `\`) || strings.HasPrefix(raw, "/") {
		return "", false
	}
	clean := path.Clean(raw)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	return clean, clean == raw
}

func taskFilesAbsolute(root, relative string) string {
	if root == "" {
		return ""
	}
	if _, ok := taskFilesRelative(relative); !ok {
		return ""
	}
	return filepath.Join(root, filepath.FromSlash(relative))
}
