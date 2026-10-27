package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type quietNamingFake struct {
	calls   atomic.Int32
	started chan application.Action
	release chan struct{}
	fail    bool
}

func (*quietNamingFake) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, errors.New("unexpected snapshot")
}
func (b *quietNamingFake) Dispatch(ctx context.Context, a application.Action) (application.ActionResult, error) {
	if a.Kind != "requirements.ai-name" {
		return application.ActionResult{}, errors.New("unexpected action")
	}
	b.calls.Add(1)
	b.started <- a
	select {
	case <-ctx.Done():
		return application.ActionResult{}, ctx.Err()
	case <-b.release:
	}
	if b.fail {
		return application.ActionResult{}, errors.New("fixture background failure")
	}
	return application.ActionResult{Data: domain.RequirementAiNamingSuggestion{FolderName: "静默命名"}}, nil
}
func TestRequirementNamingPreparationStaysSilentAndDoesNotRestartOnRender(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "failed"}[fail], func(t *testing.T) {
			t.Setenv("USERPROFILE", t.TempDir())
			backend := &quietNamingFake{started: make(chan application.Action, 2), release: make(chan struct{}), fail: fail}
			var once sync.Once
			release := func() { once.Do(func() { close(backend.release) }) }
			item := application.Requirement{ID: "1001", ProjectKey: "fixture", Project: "fixture", Type: "userstory", Title: "静默需求", URL: "https://project.feishu.cn/fixture/userstory/detail/1001"}
			config, _ := json.Marshal(domain.DefaultAppConfig())
			s := &app{page: "requirements", backend: backend, configJSON: string(config), requirements: []application.Requirement{item}, requirementsLoaded: true, requirementsRequested: true, lastError: "manual-visible", lastMessage: "manual-status"}
			s.snapshot.Home = t.TempDir()
			t.Cleanup(func() {
				release()
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := s.closeRequirementNamingPreparation(ctx); err != nil {
					t.Error(err)
				}
			})
			s.startRequirementNamingPreparation([]application.Requirement{item, item})
			select {
			case action := <-backend.started:
				if action.Payload["background"] != true {
					t.Fatal("prewarm did not use background request")
				}
			case <-time.After(time.Second):
				t.Fatal("background did not start")
			}
			tt := ui.NewTester(func(c *ui.Context) {
				s.observeRequirementNamingPreparation()
				s.requirementList(c, setTheme(c, false), true)
			}, 480, 400)
			assertQuiet := func() {
				t.Helper()
				for _, text := range tt.Texts() {
					for _, bad := range []string{"预热", "命名已准备", "fixture background failure", "缓存命名"} {
						if strings.Contains(text, bad) {
							t.Fatalf("background UI leaked %q", text)
						}
					}
				}
				if s.lastError != "manual-visible" || s.lastMessage != "manual-status" {
					t.Fatal("background replaced explicit manual feedback")
				}
			}
			for i := 0; i < 6; i++ {
				tt.Frame()
				assertQuiet()
			}
			if backend.calls.Load() != 1 || s.requirementNaming.queue.snapshot().Total != 1 {
				t.Fatal("render restarted or duplicated preparation")
			}
			release()
			deadline := time.Now().Add(time.Second)
			for s.requirementNaming.queue.snapshot().Running && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			snapshot := s.requirementNaming.queue.snapshot()
			if snapshot.Running || (!fail && snapshot.Completed != 1) || (fail && len(snapshot.Failures) != 1) {
				t.Fatalf("fake outcome %+v", snapshot)
			}
			for i := 0; i < 6; i++ {
				tt.Frame()
				assertQuiet()
			}
			if backend.calls.Load() != 1 {
				t.Fatal("completed/failed background restarted during render")
			}
		})
	}
}
