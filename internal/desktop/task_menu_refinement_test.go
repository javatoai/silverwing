package desktop

import (
	"context"

	"encoding/json"

	"path/filepath"

	"slices"
	"strings"
	"testing"
	"time"

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

func TestTaskMenuMigratesActionsAndKeepsTarget(t *testing.T) {
	for _, action := range []string{"复制任务资料路径", "在 Codex 中打开", "打开任务资料目录", "关联任务资料", "重试关联任务资料", "配置任务资料"} {
		t.Run(action, func(t *testing.T) {
			s, backend := taskMenuFixture(t)
			if strings.Contains(action, "关联任务资料") || action == "配置任务资料" {
				s.taskUnit.state.tasks[1].materialsPath, s.taskUnit.state.tasks[1].materialsStatus = "", ""
				s.settingsUnit.state.configJSON = `{"requirementMaterialsRoot":"D:/materials","requirementMaterialsSubdirectory":"docs","defaultWorkspaceToolIds":["codex"]}`
				if action == "重试关联任务资料" {
					s.taskUnit.state.tasks[1].materialsStatus = "FAILED"
				}
				if action == "配置任务资料" {
					s.settingsUnit.state.configJSON = `{}`
				}
			}
			target := s.taskUnit.state.tasks[1]
			tester := taskNavigationTester(s, 340, 560)
			navigationChooseTaskMenu(t, tester, target.title, action)
			if s.taskUnit.state.selected != 0 || s.tab != 1 || s.materialsUnit.state.documentContent != "保留草稿" || !s.materialsUnit.state.documentDirty ||
				s.materialsUnit.state.documentDrafts["draft.md"] != "保留草稿" || len(s.taskNavigationFor().value.Recent) != 0 {
				t.Fatal("migrated action changed selection, draft, detail tab or visits")
			}
			switch action {
			case "配置任务资料":
				if s.page != "settings" || s.settingsUnit.state.settingsPage != "paths" {
					t.Fatal("materials configuration did not open its settings page")
				}
			case "复制任务资料路径":
				if backend.clipboard != target.materialsPath {
					t.Fatal("copied the current task instead of the context target")
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
				index, s.page, s.taskUnit.state.selected, lifecycle = 3, "archive", 3, "恢复归档"
			} else if mode != "ready" {
				s.taskUnit.state.tasks[index].materialsStatus, s.taskUnit.state.tasks[index].materialsPath = "", ""
				s.taskUnit.state.tasks[index].requirementURL = "https://project.feishu.cn/demo/userstory/detail/123"
				materials = []string{"配置任务资料"}
				if mode != "missing-config" {
					s.settingsUnit.state.configJSON = `{"requirementMaterialsRoot":"D:/materials","requirementMaterialsSubdirectory":"docs","defaultWorkspaceToolIds":["codex"]}`
					materials = []string{"关联任务资料"}
					if mode == "failed" {
						s.taskUnit.state.tasks[index].materialsStatus = "FAILED"
						materials = []string{"重试关联任务资料"}
					}
				}
			}
			tester := taskNavigationTester(s, 340, 560)
			if err := tester.RightClick(s.taskUnit.state.tasks[index].title); err != nil {
				t.Fatal(err)
			}
			want := []string{"在 Codex 中打开", "-"}
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
			if err := tester.RightClick(s.taskUnit.state.tasks[1].title); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "materials":
				s.taskUnit.state.tasks[1].materialsPath += "-changed"
			case "configuration":
				s.settingsUnit.state.configJSON = `{}`
			case "created":
				s.taskUnit.state.tasks[1].createdAt += "-replacement"
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
		s.setTaskPinned(s.taskUnit.state.tasks[index], true)
		tester := taskNavigationTester(s, 240, 560)
		name := "已置顶：" + s.taskUnit.state.tasks[index].title
		if bounds, ok := tester.Find(name); !ok || bounds.W != 16 || bounds.H != 16 {
			t.Fatalf("missing fixed-size pinned status: %+v, %v", bounds, ok)
		}
		s.taskNavigationFor().value.Recent = []string{taskNavigationKey(s.taskUnit.state.tasks[0])}
		tester.Frame()
		if !tester.HasText(name) {
			t.Fatal("legacy recent data hid the pinned task or its status")
		}
		navigationChooseTaskMenu(t, tester, s.taskUnit.state.tasks[index].title, "取消置顶")
		if tester.HasText(name) {
			t.Fatal("unpin did not immediately remove the status icon")
		}
	}
}

func taskMenuFixture(t *testing.T) (*app, *taskMenuBackend) {
	t.Helper()
	s := taskNavigationFixture()
	s.taskUnit.state.taskContextUpdate = func(update func()) { update() }
	b := &taskMenuBackend{calls: make(chan application.Action, 4)}
	s.backend = b
	root := t.TempDir()
	s.snapshot.Home = root
	defaults := domain.DefaultAppConfig()
	raw, err := json.Marshal(defaults)
	if err != nil {
		t.Fatal(err)
	}
	s.settingsUnit.state.configJSON = string(raw)
	for i := range s.taskUnit.state.tasks {
		s.taskUnit.state.tasks[i].path = filepath.Join(root, s.taskUnit.state.tasks[i].id)
		s.taskUnit.state.tasks[i].materialsPath = filepath.Join(root, "materials", s.taskUnit.state.tasks[i].id)
		s.taskUnit.state.tasks[i].materialsStatus = "READY"
	}
	s.tab = 1
	s.materialsUnit.state.documentPath, s.materialsUnit.state.documentContent, s.materialsUnit.state.documentDirty = "draft.md", "保留草稿", true
	s.materialsUnit.state.documentDrafts[s.materialsUnit.state.documentPath] = s.materialsUnit.state.documentContent
	return s, b
}
