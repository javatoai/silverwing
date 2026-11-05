package releasenotice

import (
	"errors"
	"silverwing-go/internal/domain"
	"strings"
)

// Project only this task's service/branch pairs. Several modules on the same
// branch need one row; different branches of one service remain separate.
func BranchRows(services []domain.ServiceWorkspace) ([]Row, error) {
	rows := []Row{}
	seen := map[string]bool{}
	for _, service := range services {
		name, branch := strings.TrimSpace(service.ServiceName), strings.TrimSpace(service.Branch)
		if name == "" || branch == "" {
			return nil, errors.New("任务存在未确定服务名称或分支的工作区，请刷新任务后重试")
		}
		key := name + "\x00" + branch
		if !seen[key] {
			rows = append(rows, Row{Service: name, Branch: branch})
			seen[key] = true
		}
	}
	if len(rows) == 0 {
		return nil, errors.New("任务没有可发送的服务分支")
	}
	return rows, nil
}
