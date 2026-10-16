package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func (a *App) Dispatch(ctx context.Context, action Action) (ActionResult, error) {
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	if action.Kind == workspace.CompareSessionCloseAction {
		sessionID := textPayload(action, "sessionId")
		if strings.TrimSpace(sessionID) == "" {
			return ActionResult{}, errors.New("Git 差异会话标识不能为空")
		}
		a.workspaceGitDiffSessions().Retire(sessionID)
		return ActionResult{Message: "Git 差异会话已关闭"}, nil
	}
	if strings.HasPrefix(action.Kind, "migration.") || action.Kind == "config.view" || action.Kind == "config.backups" || action.Kind == "config.delete-backups" || action.Kind == "config.import-preview" || action.Kind == "config.restore-preview" || action.Kind == "config.restore-backup" {
		return a.configurationAction(ctx, action)
	}
	switch action.Kind {
	case "config.task-root.save":
		return a.saveTaskRootSwitch(ctx, action)
	case "config.materials-root.save":
		return a.saveMaterialsRoot(action)
	case "task.refresh":
		return a.refreshTasks(ctx)
	case "config.save":
		return a.saveConfiguration(action)
	case "config.fields.save":
		return a.saveConfigurationFields(action)
	case "config.export":
		return a.exportConfiguration(action)
	case "config.import":
		config, err := a.Config.ImportFrom(actionPath(action))
		return ActionResult{Message: "配置已导入", Data: config}, err
	case "repository.add":
		config, err := a.Config.Load()
		if err != nil {
			return ActionResult{}, err
		}
		repository, err := workspace.NewRepositoryInspector(a.git(config)).InspectContext(ctx, actionPath(action))
		if err != nil {
			return ActionResult{}, err
		}
		_, err = a.Config.Update(func(c domain.AppConfig) (domain.AppConfig, error) {
			for _, r := range c.Repositories {
				if r.ID == repository.ID || strings.EqualFold(filepath.Clean(r.GitCommonDirectory), filepath.Clean(repository.GitCommonDirectory)) {
					return c, errors.New("该仓库已添加")
				}
			}
			c.Repositories = append(c.Repositories, repository)
			return c, nil
		})
		return ActionResult{Message: "仓库已添加", Data: repository}, err
	case "repository.choices":
		return a.repositoryChoices(ctx, action)
	case "repository.addresses":
		return a.repositoryAddresses(ctx, action)
	case "service.add-repositories":
		return a.addRepositories(ctx, action)
	case "repository.remove":
		id := textPayload(action, "repositoryId")
		if id == "" {
			id = action.ServiceID
		}
		_, err := a.Config.Update(func(c domain.AppConfig) (domain.AppConfig, error) {
			for _, g := range c.Groups {
				for _, s := range g.Services {
					if s.RepositoryID == id {
						return c, errors.New("仓库仍被服务引用，请先调整服务")
					}
				}
			}
			found := false
			out := make([]domain.RepositoryConfig, 0)
			for _, r := range c.Repositories {
				if r.ID == id {
					found = true
				} else {
					out = append(out, r)
				}
			}
			if !found {
				return c, errors.New("仓库不存在")
			}
			c.Repositories = out
			return c, nil
		})
		return ActionResult{Message: "仓库配置已移除"}, err
	case "service.save":
		return a.serviceConfig(action)
	case "service.remove":
		return a.removeConfiguredService(action)
	case "service.module-save", "service.module-remove":
		return a.moduleConfig(action)
	case "tool.json", "tool.time", "tool.diff", "tool.ports", "tool.port-stop":
		return a.developerToolAction(ctx, action)
	case "codex.run":
		return a.openCodex(ctx, action)
	}
	if strings.HasPrefix(action.Kind, "document.") {
		return a.documentAction(ctx, action)
	}
	if strings.HasPrefix(action.Kind, "instructions.") {
		return a.instructionAction(ctx, action)
	}
	if strings.HasPrefix(action.Kind, "requirements.") || strings.HasPrefix(action.Kind, "materials.") || strings.HasPrefix(action.Kind, "meegle.") {
		return a.integrationAction(ctx, action)
	}
	if strings.HasPrefix(action.Kind, "skills.") || strings.HasPrefix(action.Kind, "codex.extensions") || strings.HasPrefix(action.Kind, "codex.ai/") || strings.HasPrefix(action.Kind, "cli.") || strings.HasPrefix(action.Kind, "logs.") || strings.HasPrefix(action.Kind, "git.environment") || strings.HasPrefix(action.Kind, "development-tools/") || action.Kind == "terminal.resolve" || action.Kind == "diagnostics.export" || action.Kind == "proxy.test" || action.Kind == "git.version" || action.Kind == "update.check" {
		return a.platformAction(ctx, action)
	}
	if strings.HasPrefix(action.Kind, "tag.") || strings.HasPrefix(action.Kind, "agent.") {
		return a.deliveryAction(ctx, action)
	}
	if strings.HasPrefix(action.Kind, "system.") {
		return a.systemAction(ctx, action)
	}
	if strings.HasPrefix(action.Kind, "task.") || strings.HasPrefix(action.Kind, "workspace.") {
		return a.workspaceAction(ctx, action)
	}
	return ActionResult{}, fmt.Errorf("功能 %s 正在迁移，当前版本尚未实现", action.Kind)
}

func numberPayload(action Action, key string) (int, error) {
	v := action.Payload[key]
	if v == nil || v == "" {
		return 0, nil
	}
	switch n := v.(type) {
	case float64:
		if n < 1 || n > 65535 || n != float64(int(n)) {
			return 0, errors.New("请输入整数")
		}
		return int(n), nil
	case int:
		return n, nil
	case string:
		for _, digit := range n {
			if digit < '0' || digit > '9' {
				return 0, errors.New("端口号必须是 1–65535 的整数")
			}
		}
		number, err := strconv.Atoi(n)
		if err != nil {
			return 0, errors.New("端口号必须是 1–65535 的整数")
		}
		return number, nil
	}
	return 0, errors.New("请输入有效整数")
}

func (a *App) serviceConfig(action Action) (ActionResult, error) {
	id := action.ServiceID
	if id == "" {
		id = textPayload(action, "serviceId")
	}
	groupID := textPayload(action, "groupId")
	if groupID == "" {
		groupID = domain.DefaultGroupID
	}
	_, err := a.Config.Update(func(c domain.AppConfig) (domain.AppConfig, error) {
		for gi := range c.Groups {
			if c.Groups[gi].ID != groupID {
				continue
			}
			index := -1
			for i, s := range c.Groups[gi].Services {
				if s.ID == id {
					index = i
					break
				}
			}
			if action.Kind == "service.remove" {
				if index < 0 {
					return c, errors.New("服务不存在")
				}
				c.Groups[gi].Services = append(c.Groups[gi].Services[:index], c.Groups[gi].Services[index+1:]...)
				return c, nil
			}
			if id == "" {
				return c, errors.New("服务 ID 不能为空")
			}
			masterBranch := textPayload(action, "masterBranch")
			if masterBranch == "" {
				masterBranch = "origin/master"
			}
			strategy := domain.WorkspaceStandardWorktree
			if raw := textPayload(action, "strategy"); raw != "" {
				strategy = domain.WorkspaceStrategy(raw)
			}
			module := domain.ServiceModuleConfig{ID: "default", Name: "default", Strategy: strategy, TagEnabled: true, TagMode: domain.TagBuildMergeToTargetBranch, TagMessagePrefix: "Tag", CustomCommands: []domain.WorkspaceCommandConfig{}}
			service := domain.GroupServiceConfig{ID: id, Enabled: true, DisplayName: textPayload(action, "name"), GenbuServiceName: textPayload(action, "name"), RepositoryID: textPayload(action, "repositoryId"), MasterBranch: masterBranch, TestTagBaselineRef: "origin/release/test", DevelopmentTool: c.DefaultDevelopmentTool, Modules: []domain.ServiceModuleConfig{module}, Bootstrap: domain.BootstrapConfig{CopyRules: []domain.BootstrapCopyRule{}, Commands: []domain.BootstrapCommand{}}}
			if index >= 0 {
				service = c.Groups[gi].Services[index]
				if name := textPayload(action, "name"); name != "" {
					service.DisplayName = name
				}
				if ref := textPayload(action, "masterBranch"); ref != "" {
					service.MasterBranch = ref
				}
				if repo := textPayload(action, "repositoryId"); repo != "" {
					service.RepositoryID = repo
				}
			}
			if _, ok := action.Payload["testTagBaselineRef"]; ok {
				service.TestTagBaselineRef = textPayload(action, "testTagBaselineRef")
			}
			if raw, ok := action.Payload["service"]; ok {
				data, e := json.Marshal(raw)
				if e != nil {
					return c, e
				}
				if e = json.Unmarshal(data, &service); e != nil {
					return c, e
				}
			}
			if index < 0 {
				c.Groups[gi].Services = append(c.Groups[gi].Services, service)
			} else {
				c.Groups[gi].Services[index] = service
			}
			return c, nil
		}
		return c, errors.New("组不存在")
	})
	return ActionResult{Message: "服务配置已更新"}, err
}
