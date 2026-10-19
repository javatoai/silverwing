package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

type taskMenuBackend struct {
	calls     chan application.Action
	clipboard string
}

func (b *taskMenuBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, nil
}

func (b *taskMenuBackend) Dispatch(_ context.Context, action application.Action) (application.ActionResult, error) {
	b.calls <- action
	return application.ActionResult{}, nil
}

func (b *taskMenuBackend) WriteRichClipboard(content application.RichClipboardContent) error {
	b.clipboard = content.Text
	return nil
}

func (b *taskMenuBackend) ReadRichClipboard() (application.RichClipboardContent, error) {
	return application.RichClipboardContent{Text: b.clipboard}, nil
}

func taskMenuFixture(t *testing.T) (*app, *taskMenuBackend) {
	t.Helper()
	s := taskNavigationFixture()
	s.taskContextUpdate = func(update func()) { update() }
	b := &taskMenuBackend{calls: make(chan application.Action, 4)}
	s.backend = b
	root := t.TempDir()
	s.snapshot.Home = root
	config, err := json.Marshal(domain.DefaultAppConfig())
	if err != nil {
		t.Fatal(err)
	}
	s.configJSON = string(config)
	for i := range s.tasks {
		s.tasks[i].path = filepath.Join(root, s.tasks[i].id)
		s.tasks[i].materialsPath = filepath.Join(root, "materials", s.tasks[i].id)
		s.tasks[i].materialsStatus = "READY"
	}
	base, branch := "origin/main", "feature/ui"
	s.tasks[1].branch = branch
	s.tasks[1].workspaces = []application.Workspace{{
		ServiceID: "service", ModuleID: "api", Name: "ui-service", Path: filepath.Join(s.tasks[1].path, "api"), Branch: branch, State: "READY",
		Record: domain.ServiceWorkspace{GroupServiceID: "service", ModuleID: "api", ServiceName: "ui-service", ModuleName: "api",
			WorktreePath: filepath.Join(s.tasks[1].path, "api"), Branch: branch, Health: domain.WorkspaceReady, BaseRef: &base, TargetBranch: &branch},
	}}
	s.tab = 1
	s.documentPath, s.documentContent, s.documentDirty = "draft.md", "保留草稿", true
	s.documentDrafts[s.documentPath] = s.documentContent
	return s, b
}

func TestTaskMenuMigratesActionsAndKeepsTarget(t *testing.T) {
	for _, action := range []string{"复制任务资料路径", "在 Codex 中打开", "打开任务资料目录", "批量合并主分支", "关联任务资料", "重试关联任务资料", "配置任务资料"} {
		t.Run(action, func(t *testing.T) {
			s, backend := taskMenuFixture(t)
			if strings.Contains(action, "关联任务资料") || action == "配置任务资料" {
				s.tasks[1].materialsPath, s.tasks[1].materialsStatus = "", ""
				s.configJSON = `{"requirementMaterialsRoot":"D:/materials","requirementMaterialsSubdirectory":"docs"}`
				if action == "重试关联任务资料" {
					s.tasks[1].materialsStatus = "FAILED"
				}
				if action == "配置任务资料" {
					s.configJSON = `{}`
				}
			}
			target := s.tasks[1]
			tester := taskNavigationTester(s, 340, 560)
			navigationChooseTaskMenu(t, tester, target.title, action)
			if s.selected != 0 || s.tab != 1 || s.documentContent != "保留草稿" || !s.documentDirty ||
				s.documentDrafts["draft.md"] != "保留草稿" || len(s.taskNavigationFor().value.Recent) != 0 {
				t.Fatal("migrated action changed selection, draft, detail tab or visits")
			}
			switch action {
			case "配置任务资料":
				if s.page != "settings" || s.settingsPage != "paths" {
					t.Fatal("materials configuration did not open its settings page")
				}
			case "复制任务资料路径":
				if backend.clipboard != target.materialsPath {
					t.Fatal("copied the current task instead of the context target")
				}
			case "批量合并主分支":
				state := s.workspaceMergeBatch
				if state == nil || state.TaskID != target.id || len(state.Items) != 1 || !state.Items[0].Selected {
					t.Fatal("non-current task could not open its merge confirmation")
				}
				confirmation := ui.NewTester(func(c *ui.Context) { s.renderWorkspaceMergeBatchDialog(c, setTheme(c, false)) }, 900, 600)
				if !confirmation.HasText(target.title) {
					t.Fatal("merge confirmation does not identify the target task")
				}
				s.submitWorkspaceMergeBatch(state)
				select {
				case got := <-backend.calls:
					if got.TaskID != target.id || got.Path != target.path || got.Kind != "workspace.merge-batch" {
						t.Fatalf("wrong merge target: %+v", got)
					}
				case <-time.After(time.Second):
					t.Fatal("merge was not dispatched")
				}
			default:
				select {
				case got := <-backend.calls:
					wantKind, wantPath := "codex.run", target.path
					if action == "打开任务资料目录" {
						wantKind, wantPath = "system.open", target.materialsPath
					} else if strings.Contains(action, "关联任务资料") {
						wantKind, wantPath = "task.materials-retry", ""
					}
					if got.Kind != wantKind || got.TaskID != target.id || got.Path != wantPath {
						t.Fatalf("wrong context target: %+v", got)
					}
				case <-time.After(time.Second):
					t.Fatal("context action was not dispatched")
				}
			}
		})
	}
}

func TestTaskMenuOrderAndMaterialsVariants(t *testing.T) {
	for _, mode := range []string{"ready", "missing-config", "associate", "failed", "archived"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := taskMenuFixture(t)
			index := 1
			materials := []string{"打开任务资料目录", "复制任务资料路径"}
			lifecycle := "归档任务"
			if mode == "archived" {
				index, s.page, s.selected, lifecycle = 3, "archive", 3, "恢复归档"
			} else if mode != "ready" {
				s.tasks[index].materialsStatus, s.tasks[index].materialsPath = "", ""
				s.tasks[index].requirementURL = "https://project.feishu.cn/demo/userstory/detail/123"
				materials = []string{"配置任务资料"}
				if mode != "missing-config" {
					s.configJSON = `{"requirementMaterialsRoot":"D:/materials","requirementMaterialsSubdirectory":"docs"}`
					materials = []string{"关联任务资料"}
					if mode == "failed" {
						s.tasks[index].materialsStatus = "FAILED"
						materials = []string{"重试关联任务资料"}
					}
				}
			}
			tester := taskNavigationTester(s, 340, 560)
			if err := tester.RightClick(s.tasks[index].title); err != nil {
				t.Fatal(err)
			}
			want := []string{"在 Codex 中打开", "-", "批量合并主分支", "-"}
			want = append(want, materials...)
			want = append(want, "-", "置顶任务", "-", lifecycle, "-", "删除任务")
			if got := tester.Menu(); !slices.Equal(got, want) {
				t.Fatalf("menu=%q want=%q", got, want)
			}
		})
	}
}

func TestTaskMenuRejectsChangedMaterialsAndConfiguration(t *testing.T) {
	for _, change := range []string{"materials", "configuration", "created"} {
		t.Run(change, func(t *testing.T) {
			s, backend := taskMenuFixture(t)
			tester := taskNavigationTester(s, 340, 560)
			if err := tester.RightClick(s.tasks[1].title); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "materials":
				s.tasks[1].materialsPath += "-changed"
			case "configuration":
				s.configJSON = `{}`
			case "created":
				s.tasks[1].createdAt += "-replacement"
			}
			_ = tester.ChooseMenuItem("复制任务资料路径")
			if backend.clipboard != "" {
				t.Fatal("stale menu copied a changed target")
			}
		})
	}
}

func TestPinnedTaskStatusIcon(t *testing.T) {
	for _, archived := range []bool{false, true} {
		s := taskNavigationFixture()
		index := 1
		if archived {
			index, s.page = 3, "archive"
		}
		s.setTaskPinned(s.tasks[index], true)
		tester := taskNavigationTester(s, 240, 560)
		name := "已置顶：" + s.tasks[index].title
		if bounds, ok := tester.Find(name); !ok || bounds.W != 16 || bounds.H != 16 {
			t.Fatalf("missing fixed-size pinned status: %+v, %v", bounds, ok)
		}
		s.taskNavigationFor().value.Recent = []string{taskNavigationKey(s.tasks[index])}
		s.taskIndexBrowsingFor(archived).recentOnly = true
		tester.Frame()
		if !tester.HasText(name) {
			t.Fatal("recent view lost the pinned status")
		}
		navigationChooseTaskMenu(t, tester, s.tasks[index].title, "取消置顶")
		if tester.HasText(name) {
			t.Fatal("unpin did not immediately remove the status icon")
		}
	}
}

func TestWorkspaceCardOmitsPathRowAndKeepsOptionalCopy(t *testing.T) {
	f := newWorkspaceCopyTooltipFixture(t, false)
	tester := f.tester(1000, false, 0, false)
	for _, text := range tester.Texts() {
		if strings.Contains(text, f.w.Path) {
			t.Fatalf("workspace path row is still visible: %q", text)
		}
	}
	f.s.configJSON = `{"tagEnabled":false,"showWorkspacePathActionGroup":true}`
	tester.Frame()
	if err := tester.Click("复制工作区路径"); err != nil {
		t.Fatal(err)
	}
	if tester.Clipboard() != f.w.Path {
		t.Fatal("optional workspace path action no longer copies the full path")
	}
}

func TestTaskMenuRefinementVisuals(t *testing.T) {
	for _, width := range []int{160, 240, 340} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s, _ := taskMenuFixture(t)
				s.selected = 1
				s.tasks[0].title = "支付平台跨项目联调与异常场景修复的较长任务名称"
				s.setTaskPinned(s.tasks[0], true)
				s.setTaskPinned(s.tasks[1], true)
				tester := ui.NewTester(func(c *ui.Context) {
					p := setTheme(c, dark)
					ui.MenuScope(c, "task-refinement-visual")
					ui.Row(c).Fill().Children(func() {
						ui.Column(c).Width(float32(width)).Shrink(0).Children(func() { s.taskIndex(c, p) })
						ui.Scroll(c).Grow(1).MinWidth(0).Padding(16).Children(func() { s.taskDetails(c, p) })
					})
					ui.ThemedMenus(c, "task-refinement-visual")
				}, 1100, 620)
				for _, target := range []task{s.tasks[0], s.tasks[1]} {
					bounds, ok := tester.Find("已置顶：" + target.title)
					if !ok || bounds.W != 16 || bounds.H != 16 || bounds.X+bounds.W > float32(width) {
						t.Fatalf("pinned indicator compressed or clipped: %+v", bounds)
					}
				}
				for _, removed := range []string{"任务更多操作", s.tasks[1].workspaces[0].Path} {
					if tester.HasText(removed) {
						t.Fatalf("removed toolbar or path row remains: %q", removed)
					}
				}
				captureReleaseNotice(t, tester, fmt.Sprintf("task-layout-%d-dark-%t", width, dark))
				if err := tester.RightClick(s.tasks[1].title); err != nil {
					t.Fatal(err)
				}
				for _, label := range []string{"在 Codex 中打开", "批量合并主分支", "复制任务资料路径", "取消置顶", "删除任务"} {
					bounds, found := tester.Find(label)
					if !found || bounds.X < 0 || bounds.X+bounds.W > 1100 || bounds.Y < 0 || bounds.Y+bounds.H > 620 {
						t.Fatalf("context menu is clipped: %s %+v", label, bounds)
					}
				}
				captureReleaseNotice(t, tester, fmt.Sprintf("task-layout-menu-%d-dark-%t", width, dark))
				tester.Key(0, ui.KeyEscape)
				if tester.HasText("在 Codex 中打开") || s.selected != 1 {
					t.Fatal("Escape failed to dismiss the task menu")
				}
			})
		}
	}
}
