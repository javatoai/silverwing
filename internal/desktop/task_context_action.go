package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/egoist/mygo"
	"silverwing-go/internal/application"
)

var errTaskContextChanged = errors.New("任务或应用上下文已变化，请重新打开任务菜单。")

func taskMenuContextAction(action application.Action, scope taskMenuScope) application.Action {
	payload := make(map[string]any, len(action.Payload)+1)
	for key, value := range action.Payload {
		payload[key] = value
	}
	payload[applicationTaskContextPayloadKey] = map[string]any{
		"taskId": scope.taskID, "taskPath": scope.taskPath, "taskCreatedAt": scope.taskCreatedAt,
		"home": scope.home, "taskRoot": scope.taskRoot, "page": scope.page, "archived": scope.archived,
		"configFingerprint": taskContextConfigFingerprint(scope.configJSON), "groupId": scope.groupID, "branch": scope.branch,
		"materialsPath": scope.materialsPath, "materialsStatus": scope.materialsStatus,
		"materialsFingerprint": taskContextMaterialsFingerprint(scope.requirementURL, scope.requirementID,
			scope.materialsPath, scope.materialsStatus, scope.materialsFailureReason),
	}
	action.Payload = payload
	return action
}

func taskContextConfigFingerprint(configJSON string) string {
	sum := sha256.Sum256([]byte(configJSON))
	return hex.EncodeToString(sum[:])
}

func taskContextMaterialsFingerprint(requirementURL, requirementID, materialsPath, materialsStatus, failureReason string) string {
	encoded, _ := json.Marshal([]string{requirementURL, requirementID, materialsPath, materialsStatus, failureReason})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// taskContextUpdater is captured on the UI thread before a worker starts.
// Tests can inject a synchronous dispatcher without needing a native window.
func taskContextUpdater(window *mygo.Window, headless func(func())) func(func()) {
	if window != nil && !window.IsDestroyed() {
		return window.Update
	}
	return headless
}

// A lifecycle action can make its own captured UI scope stale (archive,
// restore, or delete). Reconcile that result only when the refreshed backend
// snapshot proves the intended lifecycle transition for the same incarnation
// and the application configuration still belongs to the captured context.
func (s *app) taskLifecycleResultBelongsToContext(action application.Action, expected taskMenuScope, snapshot application.Snapshot) bool {
	if action.TaskID != expected.taskID || action.Path != expected.taskPath ||
		s.page != expected.page || s.snapshot.Home != expected.home || s.settingsUnit.state.configJSON != expected.configJSON ||
		s.configString("taskRoot") != expected.taskRoot || snapshot.Home != expected.home ||
		taskContextConfigFingerprint(snapshot.ConfigJSON) != taskContextConfigFingerprint(expected.configJSON) {
		return false
	}
	desiredArchived := false
	switch action.Kind {
	case "task.archive":
		desiredArchived = true
	case "task.restore", "task.delete":
	default:
		return false
	}

	current := s.taskUnit.taskByID(expected.taskID)
	if current.id != "" {
		actual, valid := s.taskMenuScopeFor(current)
		if !valid {
			return false
		}
		if actual.archived != expected.archived &&
			((action.Kind != "task.archive" && action.Kind != "task.restore") || actual.archived != desiredArchived) {
			return false
		}
		actual.archived = expected.archived
		if actual != expected {
			return false
		}
	} else if action.Kind != "task.delete" {
		return false
	}

	for _, refreshed := range projectTasks(snapshot) {
		if refreshed.id != expected.taskID {
			continue
		}
		if action.Kind == "task.delete" || refreshed.path != expected.taskPath || refreshed.createdAt != expected.taskCreatedAt ||
			refreshed.groupID != expected.groupID || refreshed.branch != expected.branch ||
			taskContextMaterialsFingerprint(refreshed.requirementURL, refreshed.requirementID, refreshed.materialsPath,
				refreshed.materialsStatus, refreshed.materialsFailureReason) != taskContextMaterialsFingerprint(expected.requirementURL,
				expected.requirementID, expected.materialsPath, expected.materialsStatus, expected.materialsFailureReason) ||
			refreshed.archived != desiredArchived {
			return false
		}
		return true
	}
	return action.Kind == "task.delete"
}

// waitForTaskMenuScope runs current on the owning UI thread and remains
// cancellation-aware if the window begins shutting down while the callback is
// queued. The buffered result lets an already-queued callback finish harmlessly.
func waitForTaskMenuScope(ctx context.Context, update func(func()), current func() bool, destroyed func() bool) error {
	if update == nil || current == nil {
		return errTaskContextChanged
	}
	if destroyed != nil && destroyed() {
		return errTaskContextChanged
	}
	result := make(chan bool, 1)
	update(func() { result <- current() })
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case valid := <-result:
			if !valid {
				return errTaskContextChanged
			}
			return nil
		case <-ticker.C:
			if destroyed != nil && destroyed() {
				return errTaskContextChanged
			}
		}
	}
}

const applicationTaskContextPayloadKey = "desktopTaskContext"
