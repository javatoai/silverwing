package application

import (
	"context"
	"silverwing-go/internal/domain"
)

// Backend is the in-process boundary used by the native desktop. It does not
// require a local HTTP server or another runtime.
type Backend interface {
	Snapshot(context.Context) (Snapshot, error)
	Dispatch(context.Context, Action) (ActionResult, error)
}

// WorkspaceCommandBackend delivers process output in the same Go process.
// Callbacks run on worker goroutines; the desktop queues them onto its UI thread.

type Snapshot struct {
	Home  string
	Tasks []Task

	Groups []Group
	Skills []Skill

	ConfigJSON string
	// Nil means the recovery state could not be read; it is not absence.
	TaskRootMigrationPending *bool
	Theme                    string
	Error                    string
}

type Task struct {
	Materials                                                MaterialsPermissions
	ID, Title, Project, Status, Updated, Path, MaterialsPath string
	CreatedAt                                                string
	RequirementURL, RequirementTitle, GroupID                string
	RequirementID, MaterialsStatus, MaterialsFailureReason   string
	WorkspaceToolLaunches                                    []domain.WorkspaceToolLaunch

	Archived bool
}

type Group struct {
	ID, Name string
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

// Task, document, requirement, configuration, Codex, Skills and utility actions are shared by the native desktop.
