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
	Path                    string   `json:"path"`
	ChangedFields           []string `json:"changedFields"`
	Changes                 []string `json:"changes"`
	InvalidDevelopmentTools []string `json:"invalidDevelopmentTools"`
	Fingerprint             string   `json:"fingerprint"`
}

func (a *App) configurationAction(ctx context.Context, action Action) (ActionResult, error) {
	return a.configurationActionWithProgress(ctx, action, nil)
}

func (a *App) configurationActionWithProgress(ctx context.Context, action Action, onProgress func(workspace.TaskRootMigrationProgress)) (ActionResult, error) {
	switch action.Kind {
	case "config.import-preview":
		path := actionPath(action)
		config, fingerprint, err := a.Config.PreviewImportSnapshotContext(ctx, path)
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
		preview := ConfigImportPreview{Path: filepath.Clean(path), ChangedFields: []string{}, Changes: []string{}, InvalidDevelopmentTools: []string{}}
		if err != nil {
			preview.Changes = []string{"当前配置已损坏，确认后会用选中的备份替换当前配置。"}
		} else {
			preview.ChangedFields = changedConfigFields(current, config)
			preview.Changes = configImportChanges(current, config)
		}
		for _, tool := range config.DevelopmentTools {
			if err := ctx.Err(); err != nil {
				return ActionResult{}, err
			}
			if _, e := os.Stat(tool.Path); e != nil {
				preview.InvalidDevelopmentTools = append(preview.InvalidDevelopmentTools, string(tool.Type))
			}
		}
		preview.Fingerprint = fingerprint
		return ActionResult{Message: "配置预览已读取，请确认变更", Path: path, Data: preview}, nil
	}
	config, err := a.Config.Load()
	if err != nil {
		return ActionResult{}, err
	}
	service := a.taskService(config)
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
	add(value(before.RequirementMaterialsSubdirectory) != value(after.RequirementMaterialsSubdirectory), fmt.Sprintf("任务角色名从 %s 改为 %s", value(before.RequirementMaterialsSubdirectory), value(after.RequirementMaterialsSubdirectory)))
	add(len(before.ServiceCatalog()) != len(after.ServiceCatalog()), fmt.Sprintf("服务：%d → %d", len(before.ServiceCatalog()), len(after.ServiceCatalog())))
	add(len(before.Repositories) != len(after.Repositories), fmt.Sprintf("仓库：%d → %d", len(before.Repositories), len(after.Repositories)))
	add(!reflect.DeepEqual(before.DevelopmentTools, after.DevelopmentTools), "开发工具配置将更新")
	add(before.ToolVisibility != after.ToolVisibility, "研发工具显示设置将更新")
	add(!reflect.DeepEqual(before.MeegleProjects, after.MeegleProjects), fmt.Sprintf("飞书项目：%d → %d", len(before.MeegleProjects), len(after.MeegleProjects)))
	add(before.AIRequirementNamingEnabled != after.AIRequirementNamingEnabled, fmt.Sprintf("AI 需求命名：%s → %s", state(before.AIRequirementNamingEnabled), state(after.AIRequirementNamingEnabled)))
	add(before.AIRequirementNamingPrewarmEnabled != after.AIRequirementNamingPrewarmEnabled, "AI 命名自动预热设置将更新")
	add(before.AIRequirementNamingModel != after.AIRequirementNamingModel, fmt.Sprintf("AI 命名模型：%s → %s", before.AIRequirementNamingModel, after.AIRequirementNamingModel))
	add(before.TagEnabled != after.TagEnabled, fmt.Sprintf("全局测试Tag：%s → %s", state(before.TagEnabled), state(after.TagEnabled)))
	add(before.TagHistoryMaxGroups != after.TagHistoryMaxGroups, fmt.Sprintf("Tag历史保留组数：%d → %d", before.TagHistoryMaxGroups, after.TagHistoryMaxGroups))
	add(before.AllowTaskTagTargetEditing != after.AllowTaskTagTargetEditing, fmt.Sprintf("任务测试目标分支修改：%s → %s", state(before.AllowTaskTagTargetEditing), state(after.AllowTaskTagTargetEditing)))
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
