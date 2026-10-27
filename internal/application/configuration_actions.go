package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

type ConfigImportPreview struct {
	Path          string   `json:"path"`
	ChangedFields []string `json:"changedFields"`
	Changes       []string `json:"changes"`
	Fingerprint   string   `json:"fingerprint"`
}

func (a *App) configurationAction(ctx context.Context, action Action) (ActionResult, error) {
	return a.configurationActionWithProgress(ctx, action, nil)
}

func (a *App) configurationActionWithProgress(ctx context.Context, action Action, onProgress func(workspace.TaskRootMigrationProgress)) (ActionResult, error) {
	switch action.Kind {
	case "config.view":
		data := a.Config.FileSnapshot()
		text := value(data.Content)
		if data.ReadError != nil {
			text = *data.ReadError
		}
		return ActionResult{Message: "配置文件原始内容已读取", Path: data.Path, Text: text, Data: data}, nil
	case "config.backups":
		data, err := a.Config.BackupsContext(ctx)
		return ActionResult{Message: "配置备份已读取", Data: data}, err
	case "config.delete-backups":
		if !boolPayload(action, "confirmed") {
			return ActionResult{}, errors.New("请先确认删除所选备份")
		}
		var ids []string
		if err := decodeField(action, "ids", &ids); err != nil {
			return ActionResult{}, errors.New("备份选择无效，请重新选择")
		}
		data, err := a.Config.DeleteBackupsContext(ctx, ids)
		return ActionResult{Message: fmt.Sprintf("已删除 %d 个配置备份", len(data.DeletedIDs)), Data: data}, err
	case "config.import-preview", "config.restore-preview":
		path := actionPath(action)
		var config domain.AppConfig
		var err error
		if action.Kind == "config.import-preview" {
			config, err = a.Config.PreviewImportContext(ctx, path)
		} else {
			config, err = a.Config.PreviewRestoreContext(ctx, path)
		}
		if err != nil {
			return ActionResult{}, err
		}
		current, err := a.Config.LoadForPreview(ctx)
		if err != nil {
			var unsupported *storage.UnsupportedConfigVersionError
			var readError *os.PathError
			// Only strict content/structure errors allow a replacement warning.
			// All filesystem reads retain their original PathError and cause.
			if errors.As(err, &unsupported) || errors.As(err, &readError) || errors.Is(err, storage.ErrLockBusy) || errors.Is(err, os.ErrPermission) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return ActionResult{}, err
			}
		}
		if cancelled := ctx.Err(); cancelled != nil {
			return ActionResult{}, cancelled
		}
		path, pathErr := filepath.Abs(path)
		if pathErr != nil {
			return ActionResult{}, pathErr
		}
		preview := ConfigImportPreview{Path: filepath.Clean(path), ChangedFields: []string{}, Changes: []string{}}
		if err != nil {
			preview.Changes = []string{"当前配置已损坏；确认后将先归档完整分片，再以导入配置替换"}
		} else {
			preview.ChangedFields = changedConfigFields(current, config)
			preview.Changes = configImportChanges(current, config)
		}

		fingerprint, err := storage.ConfigurationArchiveFingerprintContext(ctx, path)
		if err != nil {
			return ActionResult{}, err
		}
		preview.Fingerprint = fingerprint
		return ActionResult{Message: "配置预览已读取，请确认变更", Path: path, Data: preview}, nil
	case "config.restore-backup":
		config, err := a.Config.Restore(actionPath(action))
		return ActionResult{Message: "配置备份已恢复", Data: config}, err
	}
	config, err := a.Config.Load()
	if err != nil {
		return ActionResult{}, err
	}
	service := workspace.NewTaskApplicationService(a.Manifests, a.git(config), a.Paths)
	service.RootMigration.Drafts = a.Documents
	path := actionPath(action)
	if path == "" {
		path = textPayload(action, "targetRoot")
	}
	switch action.Kind {
	case "migration.preview":
		data, err := service.PreviewTaskRootMigration(config, path)
		return ActionResult{Message: "任务根目录迁移预览已读取", Path: path, Data: data}, err
	case "migration.apply":
		if !boolPayload(action, "confirmed") {
			return ActionResult{}, errors.New("请先确认任务根目录迁移预览")
		}
		var expected workspace.TaskRootMigrationPreview
		if action.Payload["plan"] != nil {
			if err := decodeField(action, "plan", &expected); err != nil {
				return ActionResult{}, err
			}
			current, e := service.PreviewTaskRootMigration(config, path)
			if e != nil {
				return ActionResult{}, e
			}
			if !reflect.DeepEqual(current, expected) {
				return ActionResult{}, errors.New("迁移范围已变化，请重新预览")
			}
		}
		data, err := service.MigrateTaskRoot(ctx, config, path, onProgress)
		message := "任务根目录已迁移"
		if len(data.CleanupFailures) > 0 {
			message = "任务根目录已切换，旧目录有待清理项"
		}
		return ActionResult{Message: message, Path: path, Data: data}, err
	case "migration.recover":
		data, err := service.RecoverInterruptedTaskRootMigration(config)
		return ActionResult{Message: "中断迁移恢复已返回，请查看结果", Data: data}, err
	}
	return ActionResult{}, errors.New("未知配置操作")
}

func configImportChanges(before, after domain.AppConfig) []string {
	changes := []string{}
	add := func(changed bool, message string) {
		if changed {
			changes = append(changes, message)
		}
	}
	state := func(enabled bool) string {
		if enabled {
			return "开启"
		}
		return "关闭"
	}
	add(value(before.TaskRoot) != value(after.TaskRoot), fmt.Sprintf("任务路径：%s → %s", value(before.TaskRoot), value(after.TaskRoot)))
	add(value(before.RequirementMaterialsRoot) != value(after.RequirementMaterialsRoot), fmt.Sprintf("任务资料根路径：%s → %s", value(before.RequirementMaterialsRoot), value(after.RequirementMaterialsRoot)))
	add(value(before.RequirementMaterialsSubdirectory) != value(after.RequirementMaterialsSubdirectory), fmt.Sprintf("任务资料子目录：%s → %s", value(before.RequirementMaterialsSubdirectory), value(after.RequirementMaterialsSubdirectory)))
	add(len(before.Groups) != len(after.Groups), fmt.Sprintf("任务组：%d → %d", len(before.Groups), len(after.Groups)))
	add(before.ToolVisibility != after.ToolVisibility, "实用工具显示设置将更新")
	add(!reflect.DeepEqual(before.MeegleProjects, after.MeegleProjects), fmt.Sprintf("飞书项目：%d → %d", len(before.MeegleProjects), len(after.MeegleProjects)))
	add(before.AIRequirementNamingEnabled != after.AIRequirementNamingEnabled, fmt.Sprintf("AI 需求命名：%s → %s", state(before.AIRequirementNamingEnabled), state(after.AIRequirementNamingEnabled)))
	add(before.AIRequirementNamingPrewarmEnabled != after.AIRequirementNamingPrewarmEnabled, "AI 命名自动预热设置将更新")
	add(before.AIRequirementNamingModel != after.AIRequirementNamingModel, fmt.Sprintf("AI 命名模型：%s → %s", before.AIRequirementNamingModel, after.AIRequirementNamingModel))

	add(len(changes) == 0 && !reflect.DeepEqual(before, after), "配置内容存在其他变化")
	return changes
}

func changedConfigFields(before, after domain.AppConfig) []string {
	toMap := func(config domain.AppConfig) map[string]json.RawMessage {
		data, _ := json.Marshal(config)
		out := map[string]json.RawMessage{}
		_ = json.Unmarshal(data, &out)
		return out
	}
	left, right := toMap(before), toMap(after)
	changed := []string{}
	for key, value := range right {
		if string(value) != string(left[key]) {
			changed = append(changed, key)
		}
	}
	sort.Strings(changed)
	return changed
}
