package application

import (
	"context"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

// Backend is the in-process boundary used by the native desktop. It does not
// require a local HTTP server or another runtime.
type Backend interface {
	Snapshot(context.Context) (Snapshot, error)
	Dispatch(context.Context, Action) (ActionResult, error)
}

// WorkspaceCommandBackend delivers process output in the same Go process.
// Callbacks run on worker goroutines; the desktop queues them onto its UI thread.
type WorkspaceCommandBackend interface {
	DispatchWorkspaceCommand(context.Context, Action, func(process.StreamEvent)) (ActionResult, error)
}

type Snapshot struct {
	Home         string
	Tasks        []Task
	Repositories []Repository
	Services     []Service
	Groups       []Group
	Skills       []Skill

	ConfigJSON string
	// Nil means the recovery state could not be read; it is not absence.
	TaskRootMigrationPending *bool
	Theme                    string
	Error                    string
}

type Task struct {
	ID, Title, Project, Status, Updated, Branch, Path, MaterialsPath string
	CreatedAt                                                        string
	RequirementURL, RequirementTitle, GroupID                        string
	RequirementID, MaterialsStatus, MaterialsFailureReason           string
	WorkspaceToolLaunches                                            []domain.WorkspaceToolLaunch
	Services                                                         []string
	Workspaces                                                       []Workspace
	Archived                                                         bool
}
type Workspace struct {
	ID, ServiceID, ModuleID, Name, Path, Branch, PushRemote, BaseRef, Strategy, State string
	Dirty                                                                             bool
	Warnings                                                                          []string
	Record                                                                            domain.ServiceWorkspace
}
type Repository struct{ ID, Name, RootPath, OriginURL, CurrentBranch, DefaultBranch string }
type Service struct {
	ID, Name, RepositoryID, MasterBranch, Strategy, GroupID string
	Modules                                                 []Module
}
type Module struct {
	ID, Name, MasterBranch, Strategy string
}
type Group struct {
	ID, Name   string
	ServiceIDs []string
}
type Skill struct{ Name, Description, Path, Source string }

type Document struct {
	Path     string     `json:"path"`
	Name     string     `json:"name"`
	Kind     string     `json:"kind"`
	Size     int64      `json:"size"`
	Modified string     `json:"modified"`
	Children []Document `json:"children,omitempty"`
}
type Requirement struct {
	ID           string                      `json:"id"`
	Title        string                      `json:"title"`
	Project      string                      `json:"project"`
	URL          string                      `json:"url"`
	Status       string                      `json:"status"`
	Updated      string                      `json:"updated"`
	Participants []string                    `json:"participants"`
	ProjectKey   string                      `json:"projectKey"`
	Type         string                      `json:"type"`
	TypeLabel    string                      `json:"typeLabel"`
	WorkItem     domain.ParticipatedWorkItem `json:"workItem"`
}

// Action identifiers retain one operation per source workflow. Payload is JSON
// compatible so the CLI and desktop can share precisely the same implementation.
type Action struct {
	Kind      string         `json:"kind"`
	TaskID    string         `json:"taskId,omitempty"`
	ServiceID string         `json:"serviceId,omitempty"`
	ModuleID  string         `json:"moduleId,omitempty"`
	Path      string         `json:"path,omitempty"`
	Text      string         `json:"text,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
}
type ActionResult struct {
	Message string `json:"message"`
	Text    string `json:"text,omitempty"`
	Path    string `json:"path,omitempty"`
	Data    any    `json:"data,omitempty"`
}

// Initial action names: task.create, task.archive, task.restore, task.refresh,
// repository.add, repository.remove, service.save, service.remove, config.save, config.fields.save,
// config.export, config.import-preview, config.import, workspace.status,
// workspace.preview, workspace.previews, workspace.add, workspace.fetch,
// workspace.commit, workspace.commit-push, workspace.push, workspace.batch, workspace.merge, workspace.merge-batch,
// workspace.repair, workspace.remove, workspace.command, document.list,
// document.read, document.save, document.mkdir, document.create, document.rename,
// document.trash, document.copy, document.open, document.reveal,
// instructions.read, instructions.save, requirements.refresh, requirements.list,
// requirements.read, requirements.comments, requirements.comment, materials.sync,
// skills.refresh, skills.install, skills.remove, codex.extensions, codex.run,
// tool.json, tool.time, tool.diff, tool.ports, tool.port-stop,
// system.open, system.reveal, system.logs, system.terminal, system.ide, diagnostics.export.
