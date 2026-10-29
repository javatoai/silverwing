package desktop

import (
	"context"
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/platform"
)

type settingsLogsClearBackend struct{ actions chan application.Action }

func (b *settingsLogsClearBackend) Dispatch(_ context.Context, action application.Action) (application.ActionResult, error) {
	b.actions <- action
	return application.ActionResult{}, nil
}
func (*settingsLogsClearBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, nil
}

func settingsLogsClearFixture(t *testing.T) (*app, *settingsLogsClearBackend, application.Action) {
	t.Helper()
	backend := &settingsLogsClearBackend{actions: make(chan application.Action, 8)}
	s := newApp(backend, Options{CapturePath: "isolated-test"})
	s.page, s.settingsUnit.state.settingsPage, s.settingsUnit.state.settingsTab = "settings", "logs", "日志"
	s.snapshot.Home = t.TempDir()
	s.settingsUnit.state.settingsLogEntries = []map[string]any{{"timestamp": "2026-10-15 19:00:00", "event": "fixture.failure", "message": "Original fixture error"}}
	action := application.Action{Kind: "logs.clear", Payload: map[string]any{"expectedHome": s.snapshot.Home, "confirmed": true, "settingsLogsAction": true}}
	return s, backend, action
}

func settingsLogsWaitAction(t *testing.T, backend *settingsLogsClearBackend) application.Action {
	t.Helper()
	select {
	case action := <-backend.actions:
		return action
	case <-time.After(2 * time.Second):
		t.Fatal("logs action was not dispatched")
	}
	return application.Action{}
}

func TestSettingsLogsClearCancelBusyAndScope(t *testing.T) {
	s, backend, action := settingsLogsClearFixture(t)
	before := append([]map[string]any(nil), s.settingsUnit.state.settingsLogEntries...)
	s.confirmSettingsLogsClear(action, 1)
	if !reflect.DeepEqual(before, s.settingsUnit.state.settingsLogEntries) || s.busy {
		t.Fatal("cancel changed log contents")
	}
	s.confirmSettingsLogsClear(action, 0)
	if started := settingsLogsWaitAction(t, backend); started.Kind != "logs.clear" {
		t.Fatal("wrong operation", started)
	}
	s.confirmSettingsLogsClear(action, 0)
	select {
	case duplicate := <-backend.actions:
		t.Fatal("duplicate clear", duplicate)
	default:
	}
	if !s.busy {
		t.Fatal("clear does not own ordinary mutation busy gate")
	}
	s.busy = false
	s.snapshot.Home = t.TempDir()
	if !s.finishSettingsLogsAction(action, application.ActionResult{Data: platform.ApplicationLogClearResult{Changed: true, ClearedFiles: 2}}, nil) || !reflect.DeepEqual(before, s.settingsUnit.state.settingsLogEntries) {
		t.Fatal("late clear changed another home")
	}
	s.confirmSettingsLogsClear(action, 0)
	select {
	case stale := <-backend.actions:
		t.Fatal("old confirmation dispatched in new home", stale)
	default:
	}
}

func TestSettingsLogsClearRefreshesActualEntriesAndPartialFailure(t *testing.T) {
	s, backend, action := settingsLogsClearFixture(t)
	before := append([]map[string]any(nil), s.settingsUnit.state.settingsLogEntries...)
	s.finishSettingsLogsAction(action, application.ActionResult{}, errors.New("fixture permission failure"))
	if !reflect.DeepEqual(before, s.settingsUnit.state.settingsLogEntries) || s.lastError != "fixture permission failure" {
		t.Fatal("failed clear lost original content")
	}
	s.finishSettingsLogsAction(action, application.ActionResult{Data: platform.ApplicationLogClearResult{FailedFiles: 2, Error: "日志正被占用"}}, nil)
	select {
	case unexpected := <-backend.actions:
		t.Fatal("unchanged failure triggered refresh", unexpected)
	default:
	}
	s.finishSettingsLogsAction(action, application.ActionResult{Data: platform.ApplicationLogClearResult{Changed: true, ClearedFiles: 1, FailedFiles: 1, Error: "fixture sync failure"}}, nil)
	refresh := settingsLogsWaitAction(t, backend)
	if refresh.Kind != "logs.list" || !reflect.DeepEqual(before, s.settingsUnit.state.settingsLogEntries) {
		t.Fatal("clear response erased live contents instead of refreshing")
	}
	s.busy = false
	newEntries := []map[string]any{{"event": "fresh", "message": "Written after clear"}}
	s.finishSettingsLogsAction(refresh, application.ActionResult{Data: newEntries}, nil)
	if len(s.settingsUnit.state.settingsLogEntries) != 1 || mapString(s.settingsUnit.state.settingsLogEntries[0], "event") != "fresh" || !strings.Contains(s.lastError, "fixture sync failure") {
		t.Fatal("fresh logs or partial failure was lost")
	}
	s.finishSettingsLogsAction(action, application.ActionResult{Data: platform.ApplicationLogClearResult{ClearedFiles: 0}}, nil)
	refresh = settingsLogsWaitAction(t, backend)
	s.busy = false
	s.finishSettingsLogsAction(refresh, application.ActionResult{Data: []map[string]any{}}, nil)
	if len(s.settingsUnit.state.settingsLogEntries) != 0 || s.lastMessage != "应用日志已清空。" {
		t.Fatal("successful empty clear did not show actual empty results")
	}
	s.settingsUnit.state.settingsPage = "about"
	s.finishSettingsLogsAction(refresh, application.ActionResult{Data: newEntries}, nil)
	if len(s.settingsUnit.state.settingsLogEntries) != 0 {
		t.Fatal("late refresh replaced another page")
	}
}

func TestSettingsLogsClearToolbarLayout(t *testing.T) {
	for _, width := range []int{960, 360} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s, backend, action := settingsLogsClearFixture(t)
				tester := ui.NewTester(func(c *ui.Context) {
					p := setTheme(c, dark)
					ui.Column(c).Fill().Padding(12).Children(func() { s.settingsLogs(c, p) })
				}, width, 480)
				clear, ok := tester.Find("清空全部日志")
				if !ok || clear.X < 0 || clear.X+clear.W > float32(width) || clear.H < 30 {
					t.Fatalf("clear button clipped: %+v", clear)
				}
				if directory := os.Getenv("SILVERWING_LOGS_EVIDENCE_DIR"); directory != "" {
					if err := os.MkdirAll(directory, 0o700); err != nil {
						t.Fatal(err)
					}
					file, err := os.Create(filepath.Join(directory, fmt.Sprintf("logs-%d-dark-%t.png", width, dark)))
					if err != nil {
						t.Fatal(err)
					}
					encodeErr := png.Encode(file, tester.Image())
					closeErr := file.Close()
					if encodeErr != nil || closeErr != nil {
						t.Fatalf("capture: %v %v", encodeErr, closeErr)
					}
				}
				s.busy, s.operationKind = true, "logs.clear"
				tester.Frame()
				if err := tester.Click("正在清空…"); err != nil {
					t.Fatal(err)
				}
				s.confirmSettingsLogsClear(action, 0)
				select {
				case duplicate := <-backend.actions:
					t.Fatal("busy clear click repeated", duplicate)
				default:
				}
				s.busy = false
				s.settingsUnit.state.settingsLogEntries = nil
				tester.Frame()
				if !tester.HasText("暂无错误记录。") {
					t.Fatal("empty logs did not render")
				}
			})
		}
	}
}
