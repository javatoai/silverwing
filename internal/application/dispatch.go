package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

func (a *App) Dispatch(ctx context.Context, action Action) (ActionResult, error) {
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	kind := action.Kind
	if strings.HasPrefix(kind, "migration.") || kind == "config.view" || kind == "config.backups" || kind == "config.delete-backups" || kind == "config.import-preview" || kind == "config.restore-preview" || kind == "config.restore-backup" {
		return a.configurationAction(ctx, action)
	}
	switch kind {
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
		c, e := a.Config.ImportFrom(actionPath(action))
		return ActionResult{Message: "配置已导入", Data: c}, e
	case "tool.json", "tool.time", "tool.diff", "tool.ports", "tool.port-stop":
		return a.developerToolAction(ctx, action)
	case "codex.run":
		return a.openCodex(ctx, action)
	}
	if strings.HasPrefix(kind, "document.") {
		return a.documentAction(ctx, action)
	}
	if strings.HasPrefix(kind, "instructions.") {
		return a.instructionAction(ctx, action)
	}
	if strings.HasPrefix(kind, "requirements.") || strings.HasPrefix(kind, "meegle.") {
		return a.integrationAction(ctx, action)
	}
	if strings.HasPrefix(kind, "skills.") || strings.HasPrefix(kind, "codex.extensions") || strings.HasPrefix(kind, "codex.ai/") || strings.HasPrefix(kind, "cli.") || strings.HasPrefix(kind, "logs.") || kind == "terminal.resolve" || kind == "diagnostics.export" || kind == "proxy.test" || kind == "git.version" || kind == "update.check" {
		return a.platformAction(ctx, action)
	}
	if strings.HasPrefix(kind, "system.") {
		return a.systemAction(ctx, action)
	}
	if strings.HasPrefix(kind, "task.") || kind == "workspace.tools-options" {
		return a.workspaceAction(ctx, action, nil)
	}
	return ActionResult{}, fmt.Errorf("产品版不支持操作：%s", kind)
}
func numberPayload(action Action, key string) (int, error) {
	v := action.Payload[key]
	if v == nil || v == "" {
		return 0, nil
	}
	switch n := v.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 1 || n > 65535 || n != math.Trunc(n) {
			return 0, errors.New("请输入整数")
		}
		return int(n), nil
	case int:
		return n, nil
	case json.Number:
		if !digitsOnly(string(n)) {
			return 0, errors.New("端口号必须是 1–65535 的整数")
		}
		i, err := strconv.Atoi(string(n))
		if err != nil {
			return 0, errors.New("端口号必须是 1–65535 的整数")
		}
		return i, nil
	case string:
		if !digitsOnly(n) {
			return 0, errors.New("端口号必须是 1–65535 的整数")
		}
		i, err := strconv.Atoi(n)
		if err != nil {
			return 0, errors.New("端口号必须是 1–65535 的整数")
		}
		return i, nil
	}
	return 0, errors.New("参数必须为整数：" + key)
}

func digitsOnly(value string) bool {
	if value == "" {
		return false
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}
