package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

type settingsSystemBackend struct {
	actions       chan application.Action
	snapshotCalls atomic.Int32
	clipboard     string
	clipboardErr  error
	result        application.ActionResult
	dispatchErr   error
}

func (backend *settingsSystemBackend) Snapshot(context.Context) (application.Snapshot, error) {
	backend.snapshotCalls.Add(1)
	return application.Snapshot{}, errors.New("unexpected snapshot request")
}

func (backend *settingsSystemBackend) Dispatch(_ context.Context, action application.Action) (application.ActionResult, error) {
	backend.actions <- action
	return backend.result, backend.dispatchErr
}

func (backend *settingsSystemBackend) WriteRichClipboard(content application.RichClipboardContent) error {
	if backend.clipboardErr != nil {
		return backend.clipboardErr
	}
	backend.clipboard = content.Text
	return nil
}

func (backend *settingsSystemBackend) ReadRichClipboard() (application.RichClipboardContent, error) {
	return application.RichClipboardContent{Text: backend.clipboard}, nil
}

func TestSettingsAppDataActionsUseCurrentHome(test *testing.T) {
	root := test.TempDir()
	backend := &settingsSystemBackend{actions: make(chan application.Action, 4)}
	state := &app{backend: backend, configJSON: `{"taskRoot":"D:/tasks","repositoryRoot":"D:/repositories"}`}
	state.snapshot.Home = filepath.Join(root, "SilverWing 应用数据")
	test.Cleanup(func() {
		deadline, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := state.closeSystemOpenJobs(deadline); err != nil {
			test.Error(err)
		}
	})
	tester := settingsSystemPageTester(state, 640, false, func(context *ui.Context, theme palette) {
		state.settingsAppData(context, theme)
	})
	if err := tester.Click("复制路径"); err != nil {
		test.Fatal(err)
	}
	if backend.clipboard != state.snapshot.Home || state.lastMessage != "应用数据目录路径已复制" {
		test.Fatalf("application path copy changed its bytes or feedback: %q, %q", backend.clipboard, state.lastMessage)
	}
	state.snapshot.Home = filepath.Join(root, "另一个实例 数据目录")
	tester.Frame()
	if err := tester.Click("打开目录"); err != nil {
		test.Fatal(err)
	}
	select {
	case action := <-backend.actions:
		if action.Kind != "system.open" || action.Path != state.snapshot.Home || action.TaskID != "" ||
			action.ServiceID != "" || action.ModuleID != "" || action.Payload["desktopSystemOpenDirectory"] != true {
			test.Fatalf("application data directory did not use the existing unscoped directory action: %+v", action)
		}
	case <-time.After(time.Second):
		test.Fatal("opening the application data directory did not reach the fake backend")
	}
	if backend.snapshotCalls.Load() != 0 || len(tester.OpenedURLs()) != 0 {
		test.Fatal("directory action unexpectedly requested a snapshot or opened an external URL")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		test.Fatalf("application data controls created files or directories: %v, %v", entries, err)
	}
}

func TestSettingsSystemCopyFailureAndRecovery(test *testing.T) {
	for _, scenario := range []struct {
		name, button, text, feedback string
		render                       func(*app, *ui.Context, palette)
	}{
		{"repository", "复制地址", silverwingRepositoryURL, "GitHub 仓库地址已复制", (*app).settingsAbout},
		{"updates", "复制链接", silverwingReleasesURL, "更新链接已复制", (*app).settingsUpdates},
		{"app-data", "复制路径", `C:\用户 数据\Silver Wing`, "应用数据目录路径已复制", (*app).settingsAppData},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			backend := &settingsSystemBackend{actions: make(chan application.Action, 1), clipboard: "original", clipboardErr: errors.New("clipboard unavailable")}
			state := &app{backend: backend, lastMessage: "previous success"}
			state.snapshot.Home = scenario.text
			tester := settingsSystemPageTester(state, 640, false, func(context *ui.Context, theme palette) {
				scenario.render(state, context, theme)
			})
			if err := tester.Click(scenario.button); err != nil {
				test.Fatal(err)
			}
			if backend.clipboard != "original" || state.lastError != "复制失败：clipboard unavailable" || state.lastMessage != "" {
				test.Fatalf("copy failure did not retain clipboard bytes and show the error: %q, %q, %q", backend.clipboard, state.lastError, state.lastMessage)
			}
			backend.clipboardErr = nil
			if err := tester.Click(scenario.button); err != nil {
				test.Fatal(err)
			}
			if backend.clipboard != scenario.text || state.lastMessage != scenario.feedback || state.lastError != "" {
				test.Fatalf("copy retry failed: %q, %q, %q", backend.clipboard, state.lastError, state.lastMessage)
			}
			if len(backend.actions) != 0 || len(tester.OpenedURLs()) != 0 {
				test.Fatal("copy action dispatched a backend action or opened an external URL")
			}
		})
	}
}

func TestSettingsAppDataMissingHomeDisablesActions(test *testing.T) {
	for _, home := range []string{"", " \t "} {
		test.Run(home, func(test *testing.T) {
			backend := &settingsSystemBackend{actions: make(chan application.Action, 1), clipboard: "original"}
			state := &app{backend: backend}
			state.snapshot.Home = home
			tester := settingsSystemPageTester(state, 360, false, func(context *ui.Context, theme palette) {
				state.settingsAppData(context, theme)
			})
			if !tester.HasText("应用数据目录尚未加载") {
				test.Fatal("missing application data directory has no explanation")
			}
			for _, action := range []string{"打开目录", "复制路径"} {
				if err := tester.Click(action); err != nil {
					test.Fatal(err)
				}
				tester.Key(0, ui.KeyTab)
				if tester.Focused(action) {
					test.Fatalf("disabled %q is still a keyboard tab stop", action)
				}
			}
			if backend.clipboard != "original" || len(backend.actions) != 0 || state.systemOpenJobs != nil || state.lastMessage != "" {
				test.Fatal("missing application data directory still permits an operation")
			}
		})
	}
}

func TestSettingsAppDataOpenFeedbackUsesExistingPipeline(test *testing.T) {
	for _, scenario := range []struct {
		name, failure string
	}{
		{"success", ""},
		{"missing-directory", "应用数据目录不存在"},
		{"not-directory", "路径不是目录"},
		{"launcher-failed", "系统无法打开目录"},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			backend := &settingsSystemBackend{actions: make(chan application.Action, 1), result: application.ActionResult{Message: "应用数据目录已打开"}}
			if scenario.failure != "" {
				backend.dispatchErr = errors.New(scenario.failure)
			}
			state := &app{backend: backend}
			state.snapshot.Home = filepath.Join(test.TempDir(), "应用 数据")
			request, err := state.beginSystemOpenJob(application.Action{Kind: "system.open", Path: state.snapshot.Home,
				Payload: map[string]any{"desktopSystemOpenDirectory": true}})
			if err != nil || request == nil {
				test.Fatalf("directory job was not accepted: %v", err)
			}
			test.Cleanup(func() { state.completeSystemOpenJob(request) })
			if request.key != "directory" {
				test.Fatalf("application data action used %q rather than directory progress", request.key)
			}
			result, dispatchErr := executeSystemOpenJob(request)
			state.finishSystemOpenJob(request, result, dispatchErr)
			state.completeSystemOpenJob(request)
			if scenario.failure == "" {
				if state.systemOpenJobs.message != "应用数据目录已打开" || state.systemOpenJobs.error != "" {
					test.Fatal("directory success was not delivered through the system-open state")
				}
			} else if state.systemOpenJobs.error != scenario.failure {
				test.Fatalf("directory failure was lost: %q", state.systemOpenJobs.error)
			}
			ui.NewTester(func(context *ui.Context) {
				setTheme(context, false)
				state.renderOperationNotifications(context)
			}, 640, 400)
			feedback := "success\x00应用数据目录已打开"
			if scenario.failure != "" {
				feedback = "error\x00" + scenario.failure
			}
			if state.operationNotifications.shown["system-open"] != feedback {
				test.Fatalf("directory feedback did not reach the existing toast channel: %q", state.operationNotifications.shown["system-open"])
			}
		})
	}
}
