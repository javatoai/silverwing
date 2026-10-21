package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func (a *App) workspaceGitDiffSessions() *workspace.WorkspaceGitDiffSessionManager {
	a.diffSessionsMu.Lock()
	defer a.diffSessionsMu.Unlock()
	if a.diffSessions == nil {
		a.diffSessions = workspace.NewWorkspaceGitDiffSessionManager()
	}
	return a.diffSessions
}

// CloseWorkspaceDiffSessions is called by the desktop shutdown lifecycle. It
// cancels in-flight Git reads and waits for session-owned temporary trees to be
// released before the process exits.
func (a *App) CloseWorkspaceDiffSessions(ctx context.Context) error {
	if a == nil {
		return nil
	}
	return a.workspaceGitDiffSessions().Close(ctx)
}

func workspaceGitDiffSessionIdentity(taskID, taskPath string, manifest domain.TaskManifest, config domain.AppConfig) (owner, configIdentity string, err error) {
	ownerBytes, err := json.Marshal(struct {
		TaskID            string
		TaskPath          string
		TaskDirectoryName string
		TaskCreatedAt     string
		GroupID           string
	}{
		TaskID: taskID, TaskPath: taskPath, TaskDirectoryName: manifest.TaskDirectoryName,
		TaskCreatedAt: manifest.CreatedAt, GroupID: manifest.GroupID,
	})
	if err != nil {
		return "", "", err
	}
	configBytes, err := json.Marshal(config)
	if err != nil {
		return "", "", err
	}
	configHash := sha256.Sum256(configBytes)
	return string(ownerBytes), hex.EncodeToString(configHash[:]), nil
}
