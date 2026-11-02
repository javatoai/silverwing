package application

import (
	"context"
	"encoding/json"
	"errors"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/media"
)

type TaskFileEntry = documents.TaskFileEntry
type TaskFilesDirectory = documents.TaskFilesDirectory
type TaskFilesSearchResult = documents.TaskFilesSearchResult
type TaskFilesReadResult = documents.TaskFilesReadResult
type TaskFilesIssue = documents.TaskFilesIssue

// TaskFilesContext is transient read ownership, not a persisted permission.
// Unrelated settings/source changes deliberately do not invalidate it.
type TaskFilesContext struct {
	Home          string `json:"home"`
	TaskPath      string `json:"taskPath"`
	TaskCreatedAt string `json:"taskCreatedAt"`
	TaskRoot      string `json:"taskRoot"`
	Archived      bool   `json:"archived"`
}

var errTaskFilesContext = errors.New("任务文件所属任务、目录或状态已变化，请重新打开任务文件")

func taskFilesContext(action Action) (TaskFilesContext, error) {
	var expected TaskFilesContext
	raw, ok := action.Payload["taskFilesContext"]
	if !ok || action.TaskID == "" {
		return expected, errTaskFilesContext
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return expected, errTaskFilesContext
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return expected, errTaskFilesContext
	}
	for _, key := range []string{"home", "taskPath", "taskCreatedAt", "taskRoot", "archived"} {
		if _, ok := fields[key]; !ok {
			return expected, errTaskFilesContext
		}
	}
	if json.Unmarshal(data, &expected) != nil || expected.Home == "" || expected.TaskPath == "" || expected.TaskCreatedAt == "" || expected.TaskRoot == "" {
		return expected, errTaskFilesContext
	}
	// JSON null is not a lifecycle boolean, even for an active task.
	if string(fields["archived"]) != "true" && string(fields["archived"]) != "false" {
		return expected, errTaskFilesContext
	}
	return expected, nil
}

func (a *App) taskFilesIdentity(ctx context.Context, action Action, expected TaskFilesContext) (domain.TaskManifest, string, error) {
	if err := ctx.Err(); err != nil {
		return domain.TaskManifest{}, "", err
	}
	// The preview reader never initializes a missing config, repairs it or opens
	// materials/draft services. Only the configured task root participates.
	cfg, err := a.Config.LoadForPreview(ctx)
	if err != nil {
		return domain.TaskManifest{}, "", err
	}
	if cfg.TaskRoot == nil || !sameTaskContextPath(expected.Home, a.Paths.Home) || !sameTaskContextPath(expected.TaskRoot, *cfg.TaskRoot) {
		return domain.TaskManifest{}, "", errTaskFilesContext
	}
	manifest, path, err := a.taskInConfig(action, cfg)
	if err != nil {
		return domain.TaskManifest{}, "", err
	}
	if manifest.TaskDirectoryName != action.TaskID || manifest.CreatedAt != expected.TaskCreatedAt || !sameTaskContextPath(path, expected.TaskPath) || (manifest.LifecycleStatus == domain.TaskArchived) != expected.Archived {
		return domain.TaskManifest{}, "", errTaskFilesContext
	}
	return manifest, path, nil
}

type taskFilesLauncher func(context.Context, string, string, bool) (ActionResult, error)

func taskFilesNativeLaunch(ctx context.Context, kind, path string, directory bool) (ActionResult, error) {
	if kind == "task.files.open" && !directory {
		return runSystemFileOpening(ctx, "system.open", path, nativeSystemFileOpening{})
	}
	return ActionResult{Path: path}, openNative(path, kind == "task.files.reveal")
}

func (a *App) taskFilesAction(ctx context.Context, action Action) (ActionResult, error) {
	return a.taskFilesActionWithLauncher(ctx, action, taskFilesNativeLaunch)
}

func (a *App) taskFilesActionWithLauncher(ctx context.Context, action Action, launch taskFilesLauncher) (ActionResult, error) {
	switch action.Kind {
	case "task.files.list", "task.files.read", "task.files.search", "task.files.open", "task.files.reveal":
	default:
		return ActionResult{}, errors.New("任务文件只支持读取、查找或显式系统打开")
	}
	expected, err := taskFilesContext(action)
	if err != nil {
		return ActionResult{}, err
	}
	_, root, err := a.taskFilesIdentity(ctx, action, expected)
	if err != nil {
		return ActionResult{}, err
	}
	scope, err := documents.OpenTaskFilesScope(root)
	if err != nil {
		return ActionResult{}, err
	}
	defer scope.Close()
	check := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, latest, err := a.taskFilesIdentity(ctx, action, expected)
		if err != nil {
			return err
		}
		if !sameTaskContextPath(root, latest) {
			return errTaskFilesContext
		}
		return scope.Check()
	}
	var result ActionResult
	switch action.Kind {
	case "task.files.list":
		data, readErr := scope.List(ctx, actionPath(action))
		if readErr != nil {
			return ActionResult{}, readErr
		}
		result = ActionResult{Path: root, Data: data}
	case "task.files.read":
		mediaSnapshot := false
		if raw, provided := action.Payload["media"]; provided {
			var valid bool
			mediaSnapshot, valid = raw.(bool)
			if !valid {
				return ActionResult{}, errors.New("任务媒体快照标记必须是布尔值")
			}
		}
		var data TaskFilesReadResult
		var readErr error
		if mediaSnapshot {
			// Limits follow the server-derived file kind, never a payload option.
			var maxBytes int64
			switch documents.Kind(actionPath(action)) {
			case "image":
				maxBytes = media.DefaultMaxImageBytes
			case "pdf":
				maxBytes = media.DefaultMaxPDFBytes
			default:
				return ActionResult{}, errors.New("媒体字节快照只支持图片或 PDF")
			}
			data, readErr = scope.ReadMedia(ctx, actionPath(action), maxBytes)
		} else {
			data, readErr = scope.Read(ctx, actionPath(action))
		}
		if readErr != nil {
			return ActionResult{}, readErr
		}
		result = ActionResult{Path: data.Content.Path, Text: data.Content.Text, Data: data}
	case "task.files.search":
		data, readErr := scope.Search(ctx, textPayload(action, "query"), documents.TaskFilesSearchLimit)
		if readErr != nil {
			return ActionResult{}, readErr
		}
		result = ActionResult{Path: root, Data: data}
	case "task.files.open", "task.files.reveal":
		err = scope.WithPath(ctx, actionPath(action), func(path string, directory bool) error {
			if err := check(); err != nil {
				return err
			}
			var err error
			result, err = launch(ctx, action.Kind, path, directory)
			return err
		})
		if err != nil {
			return ActionResult{}, err
		}
	}
	if err := check(); err != nil {
		return ActionResult{}, err
	}
	return result, nil
}
