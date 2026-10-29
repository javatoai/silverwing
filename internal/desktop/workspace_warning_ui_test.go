package desktop

import (
	"context"
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

type workspaceWarningBackend struct{ actions chan application.Action }

func (*workspaceWarningBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, nil
}

func (b *workspaceWarningBackend) Dispatch(_ context.Context, action application.Action) (application.ActionResult, error) {
	b.actions <- action
	return application.ActionResult{}, nil
}

func workspaceWarningFixture(t *testing.T, warnings []string) (*app, *workspaceWarningBackend, task, application.Workspace) {
	t.Helper()
	root := t.TempDir()
	w := application.Workspace{ID: "workspace-a", ServiceID: "service-a", ModuleID: "module-a", Name: "Example service", Path: filepath.Join(root, "workspace-a"), Branch: "feature/example", State: "READY_WITH_WARNINGS", Warnings: warnings,
		Record: domain.ServiceWorkspace{ServiceName: "Example service", ModuleName: "API", Health: domain.WorkspaceReadyWithWarnings}}
	taskItem := task{id: "task-example", title: "Example task", path: filepath.Join(root, "task"), createdAt: "synthetic-created", workspaces: []application.Workspace{w}}
	backend := &workspaceWarningBackend{actions: make(chan application.Action, 8)}
	s := newApp(backend, Options{CapturePath: "isolated-workspace-warning"})
	s.page, s.loading, s.taskUnit.state.tasks, s.win = "tasks", false, []task{taskItem}, &mygo.Window{}
	s.snapshot.Home = filepath.Join(root, "home")
	s.settingsUnit.state.configJSON = `{}`
	s.workspaceUnit.state.workspaceGit = &workspaceGitState{health: map[string]domain.WorkspaceGitHealth{
		workspaceHealthPathKey(w.Path): {State: domain.WorkspaceGitReady, PushState: domain.LocalPushPushed},
	}}
	return s, backend, taskItem, w
}

func workspaceWarningTester(s *app, taskItem task, w application.Workspace, width int, dark bool) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, dark)
		ui.Column(c).Fill().Padding(12).Children(func() { s.workspaceCard(c, p, taskItem, w, 0) })
		s.renderConfirmationDialog(c, p)
	}, width, 500)
}

func TestWorkspaceWarningLayoutReservesActionsAndShowsFullErrors(t *testing.T) {
	for _, width := range []int{360, 1000} {
		for _, dark := range []bool{false, true} {
			for _, test := range []struct{ name, text string }{
				{"short", "初始化工具暂不可用。"},
				{"lines", "复制初始化文件失败。\n启动初始化工具失败。"},
				{"long", "执行初始化分析：启动 analysis-tool: exec: \"analysis-tool\": executable file not found in %PATH%。\n模块仍可使用；请检查初始化命令的可执行文件路径后重试。"},
			} {
				t.Run(fmt.Sprintf("%d-dark-%t-%s", width, dark, test.name), func(t *testing.T) {
					s, _, taskItem, w := workspaceWarningFixture(t, []string{test.text})
					tester := workspaceWarningTester(s, taskItem, w, width, dark)
					heading, headingOK := tester.Find("工作区提醒")
					detail, detailOK := tester.Find(test.text)
					retry, retryOK := tester.Find("重新执行 Bootstrap")
					close, closeOK := tester.Find("清除警告（确认已知晓）")
					if !headingOK || !detailOK || !retryOK || !closeOK || tester.HasText("工作区有提醒") {
						t.Fatal("warning hierarchy, complete raw text or actions missing")
					}
					if detail.X+detail.W > retry.X-7 || retry.X+retry.W > close.X || close.X+close.W > float32(width)-12 ||
						math.Abs(float64(retry.Y-close.Y)) > .5 || retry.W != 34 || close.W != 34 || detail.W <= 0 || detail.Y < heading.Y+heading.H {
						t.Fatalf("warning text squeezed/overlapped actions: heading=%+v detail=%+v retry=%+v close=%+v", heading, detail, retry, close)
					}
					if strings.Contains(test.text, "\n") && detail.H < 25 {
						t.Fatal("multiline original warning did not wrap")
					}
					if directory := os.Getenv("SILVERWING_WORKSPACE_WARNING_EVIDENCE_DIR"); directory != "" && test.name == "long" {
						if err := os.MkdirAll(directory, 0o700); err != nil {
							t.Fatal(err)
						}
						file, err := os.Create(filepath.Join(directory, fmt.Sprintf("warning-%d-dark-%t.png", width, dark)))
						if err != nil {
							t.Fatal(err)
						}
						encodeErr, closeErr := png.Encode(file, tester.Image()), file.Close()
						if encodeErr != nil || closeErr != nil {
							t.Fatalf("capture: %v %v", encodeErr, closeErr)
						}
					}
				})
			}
		}
	}
}

func TestWorkspaceWarningActionsRemainScopedAndConfirmed(t *testing.T) {
	for _, operation := range []string{"retry", "close"} {
		t.Run(operation, func(t *testing.T) {
			s, backend, taskItem, w := workspaceWarningFixture(t, []string{"Synthetic initialization failure"})
			tester := workspaceWarningTester(s, taskItem, w, 700, false)
			kind, button := "workspace.bootstrap-clear", "清除警告（确认已知晓）"
			if operation == "retry" {
				kind, button = "workspace.bootstrap-rerun", "重新执行 Bootstrap"
			}
			if err := tester.Click(button); err != nil {
				t.Fatal(err)
			}
			if operation == "retry" {
				select {
				case action := <-backend.actions:
					t.Fatal("retry bypassed confirmation", action.Kind)
				default:
				}
				if err := tester.Click("取消"); err != nil {
					t.Fatal(err)
				}
				if len(w.Warnings) != 1 || s.busy {
					t.Fatal("cancel changed warnings or started an operation")
				}
				if err := tester.Click(button); err != nil {
					t.Fatal(err)
				}
				if err := tester.Click("继续"); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case action := <-backend.actions:
				if action.Kind != kind || action.TaskID != taskItem.id || action.ServiceID != w.ServiceID || action.ModuleID != w.ModuleID || action.Path != w.Path ||
					!reflect.DeepEqual(action.Payload["selectionKeys"], []string{w.Path}) || operation == "retry" && action.Payload["confirmed"] != true {
					t.Fatalf("warning operation targeted another module: %+v", action)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("warning operation did not dispatch")
			}
			_ = tester.Click(button)
			tester.Frame()
			select {
			case action := <-backend.actions:
				t.Fatal("warning action dispatched twice while busy", action.Kind)
			default:
			}
		})
	}
}

func TestWorkspaceWarningEmptyHasNoLayoutSpace(t *testing.T) {
	s, _, taskItem, w := workspaceWarningFixture(t, nil)
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Gap(8).Children(func() {
			s.workspaceWarning(c, setTheme(c, false), taskItem, w, w.ID)
			label(c, "after-warning", 12, colors(false).text)
		})
	}, 360, 150)
	after, ok := tester.Find("after-warning")
	if !ok || after.Y != 0 || tester.HasText("工作区提醒") || tester.HasText("重新执行 Bootstrap") {
		t.Fatalf("empty warning left a strip or spacing: %+v", after)
	}
}

func TestWorkspaceWarningKeepsFailureAndCreationHeadings(t *testing.T) {
	for _, test := range []struct{ state, heading string }{
		{"FAILED", "工作区创建失败"}, {"MISSING", "工作区创建失败"}, {"CREATING", "工作区创建中"},
	} {
		s, _, taskItem, w := workspaceWarningFixture(t, []string{"Synthetic initialization detail"})
		w.State = test.state
		tester := workspaceWarningTester(s, taskItem, w, 700, false)
		if !tester.HasText(test.heading) || !tester.HasText(w.Warnings[0]) || tester.HasText("工作区有提醒") {
			t.Fatalf("%s warning lost its original state meaning", test.state)
		}
	}
}
