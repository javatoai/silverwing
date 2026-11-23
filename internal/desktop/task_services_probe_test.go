package desktop

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"

	"github.com/egoist/mygo"
)

func setDesktopServiceProbe(t *testing.T, s *app, serviceID string, enabled bool) {
	t.Helper()
	var cfg domain.AppConfig
	if err := json.Unmarshal([]byte(s.snapshot.ConfigJSON), &cfg); err != nil {
		t.Fatal(err)
	}
	for i := range cfg.Services {
		if cfg.Services[i].ID == serviceID {
			cfg.Services[i].GenbuProbeEnabled = enabled
		}
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s.snapshot.ConfigJSON = string(data)
}

func TestTaskServicesDisableCancelsRequestsAndRejectsCachedOwner(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	v := s.ensureTaskServicesCurrent()
	row := v.rows[0]
	row.loaded = true
	s.openTaskServiceDialog(v, row, taskServiceConfigDetail)
	dialog := v.dialog
	dialog.config.loaded, dialog.config.text = true, "memory-only-config"
	cancelled := false
	s.taskServicesUnit.jobs = map[string]*taskServicesRequest{"old": {view: v, epoch: v.epoch, cancel: func() { cancelled = true }}}
	epoch := v.epoch
	setDesktopServiceProbe(t, s, row.serviceID, false)
	if s.taskServicesCurrent(v) {
		t.Fatal("disabled service kept a valid callback owner")
	}
	// Every old entry point must be inert even before the next render.
	row.error, dialog.config.error = "untouched", "untouched"
	s.requestTaskServicePods(v, row)
	s.requestTaskServiceConfig(v, dialog)
	s.openTaskServiceDialog(v, row, taskServicePodsDetail)
	s.refreshTaskServicesEnvironment(v)
	if row.error != "untouched" || dialog.config.error != "untouched" || row.loading || dialog.config.loading || v.dialog != dialog {
		t.Fatal("stale request started work")
	}
	fresh := s.ensureTaskServicesCurrent()
	if fresh == v || !cancelled || v.active || v.epoch <= epoch || dialog.config.text != "" || len(s.taskServicesUnit.jobs) != 0 || fresh.dialog != nil || len(fresh.rows) != 2 {
		t.Fatal("disabled row retained request, config or detail")
	}
	if s.ensureTaskServicesCurrent() != fresh || s.taskServicesCurrent(v) {
		t.Fatal("old cache restored a disabled service")
	}
	setDesktopServiceProbe(t, s, row.serviceID, true)
	restored := s.ensureTaskServicesCurrent()
	if len(restored.rows) != 4 || !s.taskServicesCurrent(restored) || dialog.config.text != "" || restored.dialog != nil {
		t.Fatal("reenabled service did not regain a fresh active owner")
	}
}

func TestTaskServicesAllDisabledAndNoServicesHaveDistinctEmptyStates(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	setDesktopServiceProbe(t, s, "s1", false)
	setDesktopServiceProbe(t, s, "s2", false)
	v := s.ensureTaskServicesCurrent()
	if v == nil || !v.hasServices || len(v.rows) != 0 {
		t.Fatal("all-disabled task retained probe rows")
	}
	s.refreshTaskServicesEnvironment(v)
	if len(s.taskServicesUnit.jobs) != 0 {
		t.Fatal("all-disabled task queued refresh")
	}
	s.taskUnit.state.tasks[0].workspaces, s.taskUnit.state.tasks[0].serviceIDs = nil, nil
	empty := s.ensureTaskServicesCurrent()
	if empty == v || empty.hasServices || len(empty.rows) != 0 {
		t.Fatal("empty cache displayed the all-disabled state")
	}
}

func TestTaskServicesDisabledProbeProjection(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	setDesktopServiceProbe(t, s, "s1", true)
	setDesktopServiceProbe(t, s, "s2", false)
	v, _ := s.taskServicesProjection()
	if v == nil || len(v.rows) != 2 || v.rows[0].serviceID != "s1" || v.rows[0].environment != "uat" || v.rows[1].environment != "prod" {
		t.Fatalf("disabled or duplicated service retained: %+v", v)
	}
}

func TestTaskServicesDropsUnpublishedPreviewAfterLeaving(t *testing.T) {
	s := taskServicesDesktopFixture(t)
	v, row := activateTaskServicesFixture(t, s, "tasks", "uat")
	s.win = &mygo.Window{}
	started := make(chan struct{})
	s.backend = &asyncLifecycleBackend{dispatch: func(ctx context.Context, _ application.Action) (application.ActionResult, error) {
		close(started)
		<-ctx.Done()
		return application.ActionResult{Data: "issued-preview-token"}, nil
	}}
	dropped := make(chan any, 2)
	published := make(chan struct{}, 1)
	s.runTaskServiceJobWithDrop(v, "preview-test", s.taskServiceAction(v, row, "task.services.config-preview"), func(application.ActionResult, error) {
		published <- struct{}{}
	}, func(result application.ActionResult) {
		dropped <- result.Data
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("preview request did not start")
	}
	s.leaveTaskServices()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.closeTaskServices(ctx); err != nil {
		t.Fatal(err)
	}
	if len(dropped) != 1 || <-dropped != "issued-preview-token" || len(published) != 0 {
		t.Fatal("leaving must discard the unpublished preview exactly once")
	}
}
