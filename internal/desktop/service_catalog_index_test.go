package desktop

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

func TestServiceCatalogFlatOrderAndDuplicates(t *testing.T) {
	snapshot := application.Snapshot{
		Services: []application.Service{
			{ID: "second", Name: "Second"},
			{ID: "same", Name: "Same"},
			{ID: "last", Name: "Last"},
		},
	}
	index := newDesktopServiceCatalog(snapshot)
	if got := index.ordered; len(got) != 3 || got[0].ID != "second" || got[1].ID != "same" || got[2].ID != "last" {
		t.Fatalf("flat service order changed: %+v", got)
	}
	snapshot.Services = append(snapshot.Services, application.Service{ID: "same", Name: "duplicate"})
	index = newDesktopServiceCatalog(snapshot)
	if _, found := index.service("same"); found || len(index.ordered) != 2 {
		t.Fatal("duplicate service ID was exposed in the flat catalog")
	}
}

func TestServiceConfigIndexPreservesAmbiguityAndEditingOwnership(t *testing.T) {
	const source = `{"services":[{"id":"a","enabled":false},{"id":"b","enabled":true}]}`
	s := &app{settingsUnit: settingsController{state: settingsState{configJSON: source}}}
	if s.serviceEnabled("a") || !s.serviceEnabled("b") {
		t.Fatal("incorrect enabled state in flat service lookup")
	}
	var editable map[string]any
	if err := json.Unmarshal([]byte(source), &editable); err != nil {
		t.Fatal(err)
	}
	findServiceConfig(editable, "a")["enabled"] = true
	if s.serviceEnabled("a") {
		t.Fatal("editing an owned map mutated the borrowed read index")
	}
	s.settingsUnit.state.configJSON = `{"services":[{"id":"same","enabled":false},{"id":"same","enabled":true}]}`
	if s.readServiceConfig("same") != nil || !s.serviceEnabled("same") {
		t.Fatal("duplicate service ID lost existing nil/default semantics")
	}
	s.settingsUnit.state.configJSON = "{"
	if s.readServiceConfig("a") != nil || !s.serviceEnabled("a") {
		t.Fatal("invalid replacement exposed the old config")
	}
}

func TestServiceProjectionSnapshotPublicationAndEmptySearch(t *testing.T) {
	snapshot := application.Snapshot{Groups: []application.Group{{ID: "g", ServiceIDs: []string{"s"}}}, Services: []application.Service{{ID: "s", GroupID: "g", Name: "Original", RepositoryID: "r", Modules: []application.Module{{ID: "m", Name: "Module"}}}}, Repositories: []application.Repository{{ID: "r", Name: "Repository", RootPath: "/fixture/repository"}}}
	s := &app{snapshot: snapshot}
	first := s.serviceCatalog()
	var state serviceListBrowsingState
	if got := state.project(first, "", "", s.serviceEnabled); len(got) != 1 || state.lower != nil {
		t.Fatal("empty search constructed lowercase full text")
	}
	snapshot.Services[0].Name = "Changed"
	snapshot.Services[0].Modules[0].Name = "Changed module"
	snapshot.Repositories[0].RootPath = "/fixture/changed"
	s.snapshot = snapshot
	s.workspaceUnit.publishServiceCatalog(newDesktopServiceCatalog(snapshot))
	if first.services["s"].value.Modules[0].Name != "Module" {
		t.Fatal("publication mutated a retained catalog")
	}
	if got := state.project(s.serviceCatalog(), "", "CHANGED", s.serviceEnabled); len(got) != 1 || got[0].Name != "Changed" {
		t.Fatal("same-slice snapshot publication missed a changed value")
	}
	if name, path := s.repositoryDetails("r"); name != "Repository" || path != "/fixture/changed" {
		t.Fatal("repository index remained stale")
	}
	if got := state.project(s.serviceCatalog(), "", " __missing__ ", s.serviceEnabled); len(got) != 0 {
		t.Fatal("unexpected service query result")
	}
}

func TestServiceListOrdersDisabledServicesLast(t *testing.T) {
	const config = `{"services":[{"id":"d1","enabled":false},{"id":"a1"},{"id":"d2","enabled":false},{"id":"a2","enabled":true}]}`
	s := &app{settingsUnit: settingsController{state: settingsState{configJSON: config}}, snapshot: application.Snapshot{
		Services: []application.Service{
			{ID: "d1", Name: "Disabled first"}, {ID: "a1", Name: "Active first"},
			{ID: "d2", Name: "Match disabled second"}, {ID: "a2", Name: "Match active second"},
		},
	}}
	index := s.serviceCatalog()
	state := &s.workspaceUnit.state.serviceList
	check := func(query string, want ...string) {
		t.Helper()
		rows := state.project(index, s.settingsUnit.state.configJSON, query, s.serviceEnabled)
		var ids []string
		for _, service := range rows {
			ids = append(ids, service.ID)
		}
		if !slices.Equal(ids, want) {
			t.Fatalf("search %q ordered services %v, want %v", query, ids, want)
		}
	}
	check("", "a1", "a2", "d1", "d2")
	check("match", "a2", "d2")
	if state.enabledCount != 2 || state.positions["a2"] != 1 || state.positions["d1"] != 2 || index.ordered[0].ID != "d1" {
		t.Fatal("display order changed the original catalog or did not update row positions")
	}
	s.settingsUnit.state.configJSON = `{"services":[{"id":"d1","enabled":false},{"id":"a1"},{"id":"d2","enabled":true},{"id":"a2","enabled":false}]}`
	check("match", "d2", "a2")
	check("", "a1", "d2", "d1", "a2")
}

func TestServiceMoveSwapsNeighborsWithinTheirStatusGroup(t *testing.T) {
	backend := &transientFormBackend{actions: make(chan application.Action, 1)}
	s := newApp(backend, Options{Page: "repositories"})
	s.win = &mygo.Window{}
	s.settingsUnit.state.configJSON = `{"services":[{"id":"a1","enabled":true},{"id":"d1","enabled":false},{"id":"a2"},{"id":"d2","enabled":false}]}`
	t.Cleanup(func() {
		if s.cancelOperation != nil {
			s.cancelOperation()
		}
	})
	s.moveService("a2", "d1")
	if s.busy {
		t.Fatal("a cross-group move started saving the configuration")
	}
	s.moveService("a1", "a2")
	select {
	case action := <-backend.actions:
		update, ok := action.Payload["update"].(application.ConfigFieldsUpdate)
		if action.Kind != "config.fields.save" || !ok {
			t.Fatalf("unexpected reorder action: %+v", action)
		}
		var services []struct{ ID string }
		if err := json.Unmarshal(update.Fields["services"], &services); err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, service := range services {
			ids = append(ids, service.ID)
		}
		if want := []string{"a2", "d1", "a1", "d2"}; !slices.Equal(ids, want) {
			t.Fatalf("reordering across an intervening disabled row saved %v, want %v", ids, want)
		}
	case <-time.After(time.Second):
		t.Fatal("same-group reorder did not dispatch a save")
	}
}

func serviceListPerformanceFixture(root string, n int) *app {
	s := newApp(nil, Options{CapturePath: "isolated-service-list"})
	s.loading = false
	s.snapshot.Groups = []application.Group{{ID: "g", Name: "Group"}}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("service-%05d", i)
		s.snapshot.Services = append(s.snapshot.Services, application.Service{GroupID: "g", ID: id, Name: "Row " + id, RepositoryID: id})
		s.snapshot.Repositories = append(s.snapshot.Repositories, application.Repository{ID: id, RootPath: filepath.Join(root, id)})
	}
	s.workspaceUnit.publishServiceCatalog(newDesktopServiceCatalog(s.snapshot))
	return s
}

func TestServiceVirtualListFixedViewportBuildsBoundedRows(t *testing.T) {
	for _, n := range []int{10, 1000, 5000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			s := serviceListPerformanceFixture(t.TempDir(), n)
			tester := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Children(func() { s.serviceList(c, setTheme(c, false)) }) }, 800, 400)
			rows := 0
			for _, text := range tester.Texts() {
				if strings.HasPrefix(text, "Row service-") {
					rows++
				}
			}
			if rows == 0 || rows > 40 {
				t.Fatalf("viewport built %d row labels for %d services", rows, n)
			}
			if s.workspaceUnit.state.serviceList.lower != nil {
				t.Fatal("empty search did full-catalog lowercase work")
			}
			first, last := s.workspaceUnit.state.serviceList.list.Visible()
			if last-first > 20 {
				t.Fatal("visible row window scaled with total count")
			}
		})
	}
}

var serviceIndexBenchmark []application.Service

func BenchmarkServiceCatalogRead(b *testing.B) {
	s := serviceListPerformanceFixture(b.TempDir(), 5000)
	index := s.serviceCatalog()
	state := &s.workspaceUnit.state.serviceList
	state.project(index, "", "", s.serviceEnabled)
	b.Run("empty-query-warm", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			serviceIndexBenchmark = state.project(index, "", "", s.serviceEnabled)
		}
	})
	state.project(index, "", "__missing__", s.serviceEnabled)
	b.Run("same-query-warm", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			serviceIndexBenchmark = state.project(index, "", "__missing__", s.serviceEnabled)
		}
	})
	b.Run("lookup-5000", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for _, service := range index.ordered {
				_, _ = index.service(service.ID)
				_, _ = s.repositoryDetails(service.RepositoryID)
			}
		}
	})
}

func TestProjectTasksUsesFlatServiceNames(t *testing.T) {
	snapshot := application.Snapshot{
		Services: []application.Service{{ID: "first", Name: "First"}, {ID: "second", Name: "Second"}},
		Tasks:    []application.Task{{ID: "task", Services: []string{"second", "first"}}},
	}
	if got := projectTasks(snapshot); len(got) != 1 || !slices.Equal(got[0].services, []string{"Second", "First"}) {
		t.Fatalf("task lost flat service names: %+v", got)
	}
}
