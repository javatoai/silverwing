package application

import (
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func (a *App) recoverStartupTaskRootMigration(config domain.AppConfig) []string {
	service := workspace.NewTaskApplicationService(a.Manifests, a.git(config), a.Paths)
	service.RootMigration.Drafts = a.Documents
	failures, err := service.RecoverInterruptedTaskRootMigration(config)

	diagnostics := make([]string, 0, len(failures)+1)
	for _, failure := range failures {
		diagnostics = append(diagnostics, "任务根目录迁移恢复清理失败："+failure)
	}
	if err != nil {
		diagnostics = append(diagnostics, "任务根目录迁移恢复失败："+err.Error())
	}
	return diagnostics
}
