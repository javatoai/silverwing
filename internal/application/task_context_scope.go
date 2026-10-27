package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/taskscope"
)

const desktopTaskContextPayloadKey = "desktopTaskContext"

var errDesktopTaskContextChanged = errors.New("任务或配置已变化，请重新打开任务菜单。")

// desktopTaskContext is a private, per-action snapshot from the task menu. It
// is deliberately transient: callers without this payload keep the ordinary
// application action contract.
type desktopTaskContext struct {
	TaskID, TaskPath, TaskCreatedAt string
	Home, TaskRoot, Page            string
	Archived                        bool
	ConfigFingerprint               string
	GroupID, Branch                 string
	MaterialsPath, MaterialsStatus  string
	MaterialsFingerprint            string
}

func desktopTaskContextForAction(action Action) (desktopTaskContext, bool, error) {
	if action.Payload == nil {
		return desktopTaskContext{}, false, nil
	}
	raw, present := action.Payload[desktopTaskContextPayloadKey]
	if !present {
		return desktopTaskContext{}, false, nil
	}
	fields, ok := raw.(map[string]any)
	if !ok {
		return desktopTaskContext{}, true, errDesktopTaskContextChanged
	}
	var expected desktopTaskContext
	if err := decodePayload(fields, &expected); err != nil || expected.TaskID == "" || expected.TaskPath == "" ||
		expected.TaskCreatedAt == "" || expected.Home == "" || expected.TaskRoot == "" || expected.ConfigFingerprint == "" ||
		expected.MaterialsFingerprint == "" ||
		(expected.Page != "tasks" && expected.Page != "archive" && expected.Page != "materials") {
		return desktopTaskContext{}, true, errDesktopTaskContextChanged
	}
	return expected, true, nil
}

func (a *App) validateDesktopTaskContext(action Action, config domain.AppConfig, manifest domain.TaskManifest, taskPath string) error {
	if action.Kind == "task.delete" {
		if raw, supplied := action.Payload["taskDeleteCreatedAt"]; supplied {
			createdAt, ok := raw.(string)
			if !ok || createdAt == "" || manifest.CreatedAt != createdAt {
				return errDesktopTaskContextChanged
			}
		}
	}
	expected, present, err := desktopTaskContextForAction(action)
	if err != nil || !present {
		return err
	}
	if expected.Page == "materials" && action.Kind != "task.archive" {
		return errDesktopTaskContextChanged
	}
	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return errDesktopTaskContextChanged
	}
	materialsManifest := manifest
	materialsPath := visibleMaterialsRoot(value(manifest.RequirementMaterials.WriteRoot))
	if err := validAnnualTaskMaterials(manifest, config); err != nil {
		materialsPath = ""
		reason := err.Error()
		materialsManifest.RequirementMaterials.Status = domain.MaterialsFailed
		materialsManifest.RequirementMaterials.FailureReason = &reason
	}
	materialsManifest.RequirementMaterials.WriteRoot = &materialsPath
	if expected.TaskID != action.TaskID || expected.Home != a.Paths.Home || !sameTaskContextPath(expected.TaskPath, taskPath) ||
		config.TaskRoot == nil || !sameTaskContextPath(expected.TaskRoot, *config.TaskRoot) ||
		taskContextConfigFingerprint(string(encoded)) != expected.ConfigFingerprint ||
		manifest.TaskDirectoryName != expected.TaskID || manifest.CreatedAt != expected.TaskCreatedAt ||
		manifest.GroupID != expected.GroupID || manifest.FeatureBranch != expected.Branch ||
		materialsPath != expected.MaterialsPath ||
		!sameTaskContextText(projectedTaskMaterialsStatus(materialsManifest), expected.MaterialsStatus) ||
		taskMaterialsFingerprint(materialsManifest) != expected.MaterialsFingerprint ||
		(manifest.LifecycleStatus == domain.TaskArchived) != expected.Archived ||
		(expected.Page == "archive") != expected.Archived || (expected.Page == "materials" && expected.Archived) {
		return errDesktopTaskContextChanged
	}

	switch action.Kind {
	case "codex.run":
		if !slices.Contains(config.DefaultWorkspaceToolIDs, "codex") || action.ServiceID != "" || action.ModuleID != "" || action.Path == "" || !sameTaskContextPath(action.Path, taskPath) {
			return errDesktopTaskContextChanged
		}
	case "task.tools-launch":
		var ids []string
		if decodeField(action, "toolIds", &ids) != nil || len(ids) != 1 || ids[0] != "cursor" ||
			!slices.Contains(config.DefaultWorkspaceToolIDs, ids[0]) || action.ServiceID != "" || action.ModuleID != "" ||
			action.Path == "" || !sameTaskContextPath(action.Path, taskPath) {
			return errDesktopTaskContextChanged
		}
	case "task.materials-retry":
		if action.ServiceID != "" || action.ModuleID != "" {
			return errDesktopTaskContextChanged
		}
	case "task.archive":
		if expected.Archived || (expected.Page != "tasks" && expected.Page != "materials") ||
			action.ServiceID != "" || action.ModuleID != "" || action.Path == "" || !sameTaskContextPath(action.Path, taskPath) {
			return errDesktopTaskContextChanged
		}
	case "task.restore":
		if !expected.Archived || expected.Page != "archive" || action.ServiceID != "" || action.ModuleID != "" ||
			action.Path == "" || !sameTaskContextPath(action.Path, taskPath) {
			return errDesktopTaskContextChanged
		}
	case "task.delete":
		if action.ServiceID != "" || action.ModuleID != "" || action.Path == "" || !sameTaskContextPath(action.Path, taskPath) {
			return errDesktopTaskContextChanged
		}
	case "system.open":
		directory, _ := action.Payload["desktopSystemOpenDirectory"].(bool)
		if !directory || action.ServiceID != "" || action.ModuleID != "" ||
			!strings.EqualFold(strings.TrimSpace(expected.MaterialsStatus), string(domain.MaterialsReady)) ||
			!sameTaskContextPath(actionPath(action), expected.MaterialsPath) {
			return errDesktopTaskContextChanged
		}
	default:
		return errDesktopTaskContextChanged
	}
	return nil
}

func sameTaskContextPath(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	left, leftErr := filepath.Abs(filepath.Clean(strings.TrimSpace(a)))
	right, rightErr := filepath.Abs(filepath.Clean(strings.TrimSpace(b)))
	if leftErr != nil || rightErr != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func sameTaskContextText(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

func projectedTaskMaterialsStatus(manifest domain.TaskManifest) string {
	status := string(manifest.RequirementMaterials.Status)
	if status != "" {
		return status
	}
	if value(manifest.RequirementMaterials.WriteRoot) != "" {
		return string(domain.MaterialsReady)
	}
	return string(domain.MaterialsNotRequested)
}

func taskContextConfigFingerprint(configJSON string) string {
	sum := sha256.Sum256([]byte(configJSON))
	return hex.EncodeToString(sum[:])
}

func taskMaterialsFingerprint(manifest domain.TaskManifest) string {
	encoded, _ := json.Marshal([]string{
		manifest.RequirementLink,
		value(manifest.RequirementID),
		visibleMaterialsRoot(value(manifest.RequirementMaterials.WriteRoot)),
		projectedTaskMaterialsStatus(manifest),
		value(manifest.RequirementMaterials.FailureReason),
	})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func (a *App) validateTaskContextFromCurrentSource(action Action) error {
	if _, present, err := desktopTaskContextForAction(action); err != nil || !present {
		return err
	}
	config, err := a.Config.Load()
	if err != nil {
		return err
	}
	manifest, path, err := a.taskInConfig(action, config)
	if err != nil {
		return err
	}
	return a.validateDesktopTaskContext(action, config, manifest, path)
}

// desktopTaskMutationContext carries a transient UI snapshot into the
// service's task lock. The app-level validation is still useful for a quick
// rejection, but this second check owns the operation that follows it.
func (a *App) desktopTaskMutationContext(ctx context.Context, action Action) context.Context {
	_, scoped, scopeErr := desktopTaskContextForAction(action)
	_, hasDeleteCreatedAt := action.Payload["taskDeleteCreatedAt"]
	if scopeErr != nil {
		return taskscope.WithValidation(ctx, func(context.Context, domain.AppConfig, string, domain.TaskManifest) error { return scopeErr })
	}
	if !scoped && !(action.Kind == "task.delete" && hasDeleteCreatedAt) {
		return ctx
	}
	return taskscope.WithValidation(ctx, func(ctx context.Context, _ domain.AppConfig, taskPath string, manifest domain.TaskManifest) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		config, err := a.Config.Load()
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := a.validateDesktopTaskContext(action, config, manifest, taskPath); err != nil {
			return err
		}
		return ctx.Err()
	})
}
