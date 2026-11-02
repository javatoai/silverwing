package desktop

import (
	"encoding/json"
	"testing"

	"silverwing-go/internal/domain"
)

func setDesktopServiceProbe(t *testing.T, s *app, serviceID string, enabled bool) {
	t.Helper()
	var cfg domain.AppConfig
	if err := json.Unmarshal([]byte(s.snapshot.ConfigJSON), &cfg); err != nil {
		t.Fatal(err)
	}
	for i := range cfg.Groups[0].Services {
		if cfg.Groups[0].Services[i].ID == serviceID {
			cfg.Groups[0].Services[i].GenbuProbeEnabled = enabled
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
	row.loaded, row.configLoaded, row.configText = true, true, "memory-only-config"
	v.expanded, v.detail = row.key(), "config"
	cancelled := false
	s.taskServicesUnit.jobs = map[string]*taskServicesRequest{"old": {view: v, epoch: v.epoch, cancel: func() { cancelled = true }}}
	epoch := v.epoch
	setDesktopServiceProbe(t, s, row.serviceID, false)
	if s.taskServicesCurrent(v) {
		t.Fatal("disabled service kept a valid callback owner")
	}
	// Every old entry point must be inert even before the next render.
	row.error, row.configError = "untouched", "untouched"
	s.requestTaskServicePods(v, row)
	s.requestTaskServiceConfig(v, row)
	s.refreshTaskServicesEnvironment(v)
	if row.error != "untouched" || row.configError != "untouched" || row.loading || row.configLoading {
		t.Fatal("stale request started work")
	}
	fresh := s.ensureTaskServicesCurrent()
	if fresh == v || !cancelled || v.active || v.epoch <= epoch || row.configText != "" || len(s.taskServicesUnit.jobs) != 0 || fresh.expanded != "" || len(fresh.rows) != 2 {
		t.Fatal("disabled row retained request, config or detail")
	}
	if s.ensureTaskServicesCurrent() != fresh || s.taskServicesCurrent(v) {
		t.Fatal("old cache restored a disabled service")
	}
	setDesktopServiceProbe(t, s, row.serviceID, true)
	restored := s.ensureTaskServicesCurrent()
	if len(restored.rows) != 4 || !s.taskServicesCurrent(restored) || row.configText != "" || restored.expanded != "" {
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
