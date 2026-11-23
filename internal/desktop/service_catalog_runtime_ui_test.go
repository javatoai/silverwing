package desktop

import (
	"encoding/json"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/domain"
)

func catalogRuntimeListFixture(t *testing.T) (*app, *ui.Tester) {
	t.Helper()
	s := serviceListPerformanceFixture(t.TempDir(), 4)
	s.page = "repositories"
	seedPlatformConnectionsForDesktopTest(s, true, false)
	config := domain.DefaultAppConfig()
	for _, service := range s.snapshot.Services {
		config.Services = append(config.Services, domain.ServiceConfig{
			ID: service.ID, DisplayName: service.Name, RepositoryID: service.RepositoryID,
			Enabled: true, GenbuProbeEnabled: true, GenbuServiceName: service.ID,
		})
	}
	config.Services[1].Enabled = false
	config.Services[2].GenbuServiceName = ""
	config.Services[3].GenbuProbeEnabled = false
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	s.snapshot.ConfigJSON, s.settingsUnit.state.configJSON = string(data), string(data)
	tester := ui.NewTester(func(c *ui.Context) {
		s.beginOverflowMenuFrame()
		defer s.endOverflowMenuFrame()
		ui.Column(c).Fill().Children(func() {
			s.replicaRepositories(c, setTheme(c, false))
		})
	}, 1250, 650)
	tester.Frame()
	return s, tester
}

func TestServiceCatalogRuntimeActionsAdaptToWindow(t *testing.T) {
	s, tester := catalogRuntimeListFixture(t)
	name := s.snapshot.Services[0].Name
	const wide = 1250
	const narrow = 850
	if s.catalogRuntimeEnvironment() != "uat" || !tester.HasText("运行环境") || !tester.HasText("仓库配置") {
		t.Fatalf("missing the catalog environment or repository action: %v", tester.Texts())
	}
	if tester.HasText("移除") {
		t.Fatal("remove action still appears directly on the row")
	}
	root := ui.Rect{W: wide, H: 650}
	menuName := "查看更多 " + name
	more := taskServicesBoundsWithin(t, tester, root, menuName)
	var previous ui.Rect
	for _, action := range []string{name + " UAT Pod 明细", name + " UAT 查看配置", name + " UAT 修改配置", "配置仓库 " + name, menuName} {
		bounds := taskServicesBoundsWithin(t, tester, root, action)
		if bounds.H != serviceCatalogActionHeight || bounds.Y < more.Y-.5 || bounds.Y > more.Y+.5 || bounds.X < previous.X+previous.W {
			t.Fatalf("%q did not align with the action group: %+v vs %+v", action, bounds, more)
		}
		previous = bounds
	}
	if err := tester.Click("PROD 环境"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click(menuName); err != nil {
		t.Fatal(err)
	}
	if !tester.HasText("查看配置审批") || !tester.HasText("移除") {
		t.Fatal("wide menu did not expose the approval list and remove action")
	}
	tester.Key(0, ui.KeyEscape)
	tester.SetSize(narrow, 650)
	tester.Frame()
	if _, found := tester.Find(name + " PROD Pod 明细"); found {
		t.Fatal("narrow list did not collapse the inline actions")
	}
	root.W = narrow
	trigger := taskServicesBoundsWithin(t, tester, root, menuName)
	repository := taskServicesBoundsWithin(t, tester, root, "配置仓库 "+name)
	if repository.X+repository.W > trigger.X+.5 || trigger.H != repository.H || trigger.Y < repository.Y-.5 || trigger.Y > repository.Y+.5 {
		t.Fatalf("operations did not fit on the same row: %+v vs %+v", trigger, repository)
	}
	if err := tester.Click(menuName); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"Pod 明细", "查看配置", "修改配置", "查看配置审批", "移除"} {
		if !tester.HasText(action) {
			t.Errorf("narrow runtime menu omitted %q", action)
		}
	}
}

func TestServiceCatalogRuntimeAvailabilityAndReorder(t *testing.T) {
	s, tester := catalogRuntimeListFixture(t)
	for i, reason := range []string{"", "此服务已停用", "请在仓库配置填写 Genbu 服务名"} {
		if got := catalogRuntimeDisabledReason(s.readServiceConfig(s.snapshot.Services[i].ID)); got != reason {
			t.Errorf("availability for row %d: %q, want %q", i, got, reason)
		}
	}
	tester.SetSize(850, 650)
	tester.Frame()
	menuName := "查看更多 " + s.snapshot.Services[1].Name
	if err := tester.Click(menuName); err != nil {
		t.Fatal(err)
	}
	if !tester.HasText("查看配置审批") || !tester.HasText("移除") {
		t.Fatal("a disabled service lost its remove menu")
	}
	// Clicking a disabled runtime action must leave the menu open.
	if bounds, ok := tester.Find("查看配置审批"); ok {
		tester.ClickAt(bounds.X+bounds.W/2, bounds.Y+bounds.H/2)
	}
	if !tester.HasText("移除") {
		t.Fatal("a disabled runtime action was activated")
	}
	s.workspaceUnit.state.serviceReorderMode = true
	tester.Frame()
	if tester.HasText("运行环境") || tester.HasText("Pod 明细") || tester.HasText("查看更多") || !tester.HasText("下移") {
		t.Fatal("reorder mode did not switch to the existing reorder controls")
	}
	s.workspaceUnit.state.serviceReorderMode = false
	seedPlatformConnectionsForDesktopTest(s, false, false)
	tester.Frame()
	if tester.HasText("运行环境") || tester.HasText("Pod 明细") || !tester.HasText("仓库配置") {
		t.Fatal("Genbu switch did not control the runtime entry points")
	}
	if err := tester.Click(menuName); err != nil {
		t.Fatal(err)
	}
	if tester.HasText("查看配置审批") || !tester.HasText("移除") {
		t.Fatal("disabling Genbu changed the local remove action")
	}
}

func TestServiceCatalogHidesRuntimeActionsWithoutGenbuProbe(t *testing.T) {
	s, tester := catalogRuntimeListFixture(t)
	name := s.snapshot.Services[3].Name
	actions := []string{"Pod 明细", "查看配置", "修改配置", "查看配置审批"}
	for _, width := range []int{1250, 850} {
		tester.SetSize(width, 650)
		tester.Frame()
		for _, action := range actions {
			if _, found := tester.Find(name + " UAT " + action); found {
				t.Errorf("service without probe exposed %q at width %d", action, width)
			}
		}
		if _, found := tester.Find("配置仓库 " + name); !found {
			t.Fatalf("service without probe lost repository configuration at width %d", width)
		}
		if err := tester.Click("查看更多 " + name); err != nil {
			t.Fatal(err)
		}
		if !tester.HasText("移除") || tester.HasText("查看配置审批") {
			t.Fatalf("service without probe had the wrong menu at width %d: %v", width, tester.Texts())
		}
		if width < serviceCatalogDirectActionsMinWidth {
			for _, action := range actions {
				if tester.HasText(action) {
					t.Errorf("service without probe exposed %q in its narrow menu", action)
				}
			}
		}
		tester.Key(0, ui.KeyEscape)
	}
}
