package application

import (
	"fmt"
	"path/filepath"
	"time"
)

// exportConfiguration defaults to the same recoverable backup location as
// the desktop source. Explicit destinations remain available to API callers.
func (a *App) exportConfiguration(action Action) (ActionResult, error) {
	target := configurationExportPath(actionPath(action), a.Paths.Backups, time.Now())
	path, err := a.Config.ExportTo(target)
	return ActionResult{Message: "配置已导出", Path: path}, err
}

func configurationExportPath(explicitPath, backups string, now time.Time) string {
	if explicitPath != "" {
		return explicitPath
	}
	return filepath.Join(backups, fmt.Sprintf("silverwing-config-%d.zip", now.UnixMilli()))
}
