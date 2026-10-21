package desktop

import (
	"encoding/json"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/workspace"
)

func TestBranchReuseConflictSharedConsumers(t *testing.T) {
	for _, host := range []string{"create", "append"} {
		t.Run(host, func(t *testing.T) {
			conflict := workspace.BranchReuseConflict{
				Key:       workspace.BranchReuseKey{RepositoryID: "example-repo", Branch: "feature/example", StateFingerprint: "synthetic-fingerprint"},
				ServiceID: "example-service", ServiceName: "示例服务", ModuleName: "示例模块", LocalExists: true,
				RemoteRefs: []string{"origin/feature/example", "backup/feature/example"}, OccupiedWorktreePaths: []string{"fixture/shared-worktree"},
				LockedWorktreePaths: []string{"fixture/locked-worktree"}, RequiresForceAttach: true,
			}
			before, _ := json.Marshal(conflict)
			creationState := &taskCreationState{
				token: "branch-reuse-test", phase: "confirm",
				preview: &taskCreationPreview{conflicts: []workspace.BranchReuseConflict{conflict}},
			}
			s := &app{inputs: map[string]*string{}, taskCreation: creationState}
			appendState := &taskAppendUIState{conflicts: []workspace.BranchReuseConflict{conflict}}
			tester := ui.NewTester(func(c *ui.Context) {
				p := setTheme(c, false)
				ui.Column(c).Fill().Children(func() {
					if host == "create" {
						s.renderTaskCreationBranchReuseDialog(c, p, creationState)
					} else {
						s.renderTaskAppendConflicts(c, p, appendState)
					}
				})
			}, 720, 600)
			want := []string{"示例服务 · 示例模块", "分支：feature/example", "本地分支已存在", "远程分支：origin/feature/example, backup/feature/example", "已被 Worktree 检出：fixture/shared-worktree", "Worktree 已锁定，需先在 Git 中 unlock：fixture/locked-worktree", "锁定的 Worktree 不能自动清理或强制复用；请先解锁并重新预检。"}
			if host == "append" {
				want = append(want, "仓库：example-repo", "状态指纹：synthetic-fingerprint")
			}
			for _, text := range want {
				if !tester.HasText(text) {
					t.Fatalf("shared conflict card lost preflight evidence: %q", text)
				}
			}
			if host == "create" {
				if !tester.HasText("确认复用已有分支") || !tester.HasText("创建后会在新任务工作区中复用这些本地或远程分支；提交时仍会再次校验分支状态。") {
					t.Fatal("create-specific confirmation semantics changed")
				}
				if tester.HasText("仓库：example-repo") || tester.HasText("状态指纹：synthetic-fingerprint") {
					t.Fatal("creation confirmation exposed internal repository identity")
				}
				if err := tester.Click("我了解已检出的分支会在多个 Worktree 间共享"); err != nil {
					t.Fatal(err)
				}
				if !creationState.preview.sharedAck || appendState.sharedAck {
					t.Fatal("acknowledgement escaped the owning form")
				}
			} else if !tester.HasText("复用已检出的分支会使多个 Worktree 共享同一分支。") || tester.HasText("我了解已检出的分支会在多个 Worktree 间共享") {
				t.Fatal("append-specific acknowledgement ownership changed")
			}
			after, _ := json.Marshal(conflict)
			if string(before) != string(after) {
				t.Fatal("presentation mutated conflict evidence")
			}
			locked, occupied := taskAppendConflictFlags(appendState.conflicts)
			if !locked || !occupied {
				t.Fatal("shared display changed the original submission gate flags")
			}
		})
	}
}

func TestBranchReuseConflictFallbackAndOptionalFields(t *testing.T) {
	conflict := workspace.BranchReuseConflict{Key: workspace.BranchReuseKey{RepositoryID: "fallback-repo", Branch: "feature/fallback", StateFingerprint: "fallback-state"}, ServiceID: "fallback-service"}
	tester := ui.NewTester(func(c *ui.Context) { renderBranchReuseConflict(c, setTheme(c, true), conflict) }, 640, 300)
	if !tester.HasText("fallback-service · fallback-repo") || !tester.HasText("分支：feature/fallback") || tester.HasText("本地分支已存在") || tester.HasText("远程分支：") || tester.HasText("Worktree 已锁定") {
		t.Fatal("fallback names or optional evidence changed")
	}
}
