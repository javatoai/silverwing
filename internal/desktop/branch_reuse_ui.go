package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/workspace"
)

// renderBranchReuseConflict presents the preflight evidence. Each form owns
// its acknowledgement and submission gates.
func renderBranchReuseConflict(c *ui.Context, p palette, conflict workspace.BranchReuseConflict) {
	renderBranchReuseConflictDetails(c, p, conflict, true)
}

func renderBranchReuseConflictSummary(c *ui.Context, p palette, conflict workspace.BranchReuseConflict) {
	renderBranchReuseConflictDetails(c, p, conflict, false)
}

func renderBranchReuseConflictDetails(c *ui.Context, p palette, conflict workspace.BranchReuseConflict, includeRepositoryIdentity bool) {
	serviceName := firstNonEmpty(conflict.ServiceName, "未命名服务")
	moduleName := firstNonEmpty(conflict.ModuleName, "模块")
	if includeRepositoryIdentity {
		serviceName = firstNonEmpty(conflict.ServiceName, conflict.ServiceID)
		moduleName = firstNonEmpty(conflict.ModuleName, conflict.Key.RepositoryID)
	}
	ui.Column(c).FillWidth().Padding(8).Gap(3).Background(p.surface).Radius(7).Children(func() {
		label(c, serviceName+" · "+moduleName, 11, p.text).FontWeight(600)
		label(c, "分支："+conflict.Key.Branch, 11, p.text)
		if includeRepositoryIdentity {
			label(c, "仓库："+conflict.Key.RepositoryID, 10, p.muted).Selectable()
			label(c, "状态指纹："+conflict.Key.StateFingerprint, 10, p.muted).Selectable()
		}
		if conflict.LocalExists {
			label(c, "本地分支已存在", 10, p.muted)
		}
		if len(conflict.RemoteRefs) > 0 {
			label(c, "远程分支："+strings.Join(conflict.RemoteRefs, ", "), 10, p.muted)
		}
		for _, path := range conflict.OccupiedWorktreePaths {
			label(c, "已被 Worktree 检出："+path, 10, p.amber).Selectable()
		}
		for _, path := range conflict.LockedWorktreePaths {
			label(c, "Worktree 已锁定，需先在 Git 中 unlock："+path, 10, p.amber).Selectable()
		}
	})
}
