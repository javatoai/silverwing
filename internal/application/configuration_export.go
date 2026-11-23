package application

import (
	"errors"
	"strings"
)

func (a *App) exportConfiguration(action Action) (ActionResult, error) {
	target := actionPath(action)
	if strings.TrimSpace(target) == "" {
		return ActionResult{}, errors.New("请选择配置备份的保存位置")
	}
	path, err := a.Config.ExportTo(target)
	return ActionResult{Message: "配置备份已保存", Path: path}, err
}
