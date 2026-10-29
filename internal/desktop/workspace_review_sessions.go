package desktop

import (
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo/ui"
)

// workspaceReviewSession is process-local reading state. It is deliberately
// kept out of task manifests and user configuration.
type workspaceReviewSession struct {
	SplitRatio     float32
	RendererStates map[string]*workspaceDiffRendererState
	TaskReadings   map[string]map[string]workspaceReviewReadingState
}

type workspaceReviewReadingState struct {
	Path, OldPath       string
	Kind                domain.WorkspaceGitFileChangeKind
	PreviewMode         workspace.WorkspaceFilePreviewMode
	ChangedOnly         bool
	FileView            string
	ExpandedDirectories map[string]bool
	FileScrolls         map[string]ui.ScrollState
}

func workspaceReviewRepositoryKey(home, path string) string {
	return workspaceHealthPathKey(home) + "\x00" + workspaceHealthPathKey(path)
}

func workspaceReviewTaskKey(task task, item application.Workspace) string {
	return strings.Join([]string{
		task.id,
		task.path,
		task.createdAt,
		item.ServiceID,
		item.ModuleID,
		workspaceHealthPathKey(item.Path),
	}, "\x00")
}

func (s *app) ensureWorkspaceReviewSession(task task, item application.Workspace) (*workspaceReviewSession, string, string) {
	repositoryKey := workspaceReviewRepositoryKey(s.snapshot.Home, item.Path)
	taskKey := workspaceReviewTaskKey(task, item)
	if s.workspaceUnit.state.workspaceReviewSessions == nil {
		s.workspaceUnit.state.workspaceReviewSessions = make(map[string]*workspaceReviewSession)
	}
	session := s.workspaceUnit.state.workspaceReviewSessions[repositoryKey]
	if session == nil {
		session = &workspaceReviewSession{SplitRatio: 0.5}
		s.workspaceUnit.state.workspaceReviewSessions[repositoryKey] = session
	}
	if session.SplitRatio <= 0 {
		session.SplitRatio = 0.5
	}
	if session.RendererStates == nil {
		session.RendererStates = make(map[string]*workspaceDiffRendererState)
	}
	if session.TaskReadings == nil {
		session.TaskReadings = make(map[string]map[string]workspaceReviewReadingState)
	}
	if session.TaskReadings[taskKey] == nil {
		session.TaskReadings[taskKey] = make(map[string]workspaceReviewReadingState)
	}
	return session, repositoryKey, taskKey
}

func workspaceReviewComparisonKey(state *workspaceHistoryState) string {
	if state == nil {
		return ""
	}
	parts := []string{state.CompareMode}
	switch state.CompareMode {
	case workspaceReviewMainline:
		parts = append(parts, boolKey(state.IncludeWorkingTree))
	case workspaceReviewCommits:
		parts = append(parts, state.StartCommit, state.EndCommit, boolKey(state.SimulateOnMainline))
	}
	return strings.Join(parts, "\x00")
}

func boolKey(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func (s *app) saveWorkspaceReviewSelection(state *workspaceHistoryState) {
	if state == nil || state.Mode != "review" || state.RepositorySessionKey == "" || state.TaskSessionKey == "" {
		return
	}
	if state.FilesLoading && state.SelectedPath == "" {
		return
	}
	if s.workspaceUnit.state.workspaceReviewSessions == nil {
		return
	}
	session := s.workspaceUnit.state.workspaceReviewSessions[state.RepositorySessionKey]
	if session == nil {
		return
	}
	if session.TaskReadings == nil {
		session.TaskReadings = make(map[string]map[string]workspaceReviewReadingState)
	}
	readings := session.TaskReadings[state.TaskSessionKey]
	if readings == nil {
		readings = make(map[string]workspaceReviewReadingState)
		session.TaskReadings[state.TaskSessionKey] = readings
	}
	key := state.ActiveComparisonKey
	if key == "" {
		key = workspaceReviewComparisonKey(state)
	}
	readings[key] = workspaceReviewReadingState{
		Path: state.SelectedPath, OldPath: state.SelectedOldPath,
		Kind: state.SelectedKind, PreviewMode: state.PreviewMode,
		ChangedOnly: state.ChangedLinesOnly, FileView: state.FileView,
		ExpandedDirectories: cloneBoolMap(state.ExpandedDirectories),
		FileScrolls:         cloneFileScrollValueMap(state.FileScrolls),
	}
}

func cloneBoolMap(values map[string]bool) map[string]bool {
	if values == nil {
		return make(map[string]bool)
	}
	cloned := make(map[string]bool, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneFileScrollValueMap(values map[string]*ui.ScrollState) map[string]ui.ScrollState {
	cloned := make(map[string]ui.ScrollState, len(values))
	for key, value := range values {
		if value != nil {
			cloned[key] = *value
		}
	}
	return cloned
}

func cloneFileScrollMap(values map[string]ui.ScrollState) map[string]*ui.ScrollState {
	cloned := make(map[string]*ui.ScrollState, len(values))
	for key, value := range values {
		copy := value
		cloned[key] = &copy
	}
	return cloned
}

func (s *app) saveWorkspaceReviewSession(state *workspaceHistoryState) {
	if state == nil || state.Mode != "review" || state.RepositorySessionKey == "" {
		return
	}
	if s.workspaceUnit.state.workspaceReviewSessions == nil {
		return
	}
	session := s.workspaceUnit.state.workspaceReviewSessions[state.RepositorySessionKey]
	if session == nil {
		return
	}
	state.saveActiveWorkspaceDiffRenderer()
	if state.SplitRatio > 0 {
		session.SplitRatio = state.SplitRatio
	}
	if state.DiffRendererStates != nil {
		session.RendererStates = state.DiffRendererStates
	}
	s.saveWorkspaceReviewSelection(state)
}

func workspaceReviewSelection(files []workspace.CompareChange, path, oldPath string, kind domain.WorkspaceGitFileChangeKind) (workspace.CompareChange, bool) {
	if path == "" {
		return workspace.CompareChange{}, false
	}
	for _, file := range files {
		if file.Path == path && file.OldPath == oldPath && file.Kind == string(kind) {
			return file, true
		}
	}
	return workspace.CompareChange{}, false
}
