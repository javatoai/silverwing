package desktop

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

func TestServiceCatalogGroupIdentityOrderFallbackAndDuplicates(t *testing.T) {
	snapshot := application.Snapshot{
		Groups:   []application.Group{{ID: "a", ServiceIDs: []string{"second", "same", "missing"}}, {ID: "b"}},
		Services: []application.Service{{ID: "same", GroupID: "a", Name: "A"}, {ID: "second", GroupID: "a", Name: "Second"}, {ID: "same", GroupID: "b", Name: "B"}, {ID: "last", GroupID: "b", Name: "Last"}},
	}
	index := newDesktopServiceCatalog(snapshot)
	if got := index.groupServices["a"]; len(got) != 2 || got[0].ID != "second" || got[1].Name != "A" {
		t.Fatalf("explicit ServiceIDs order changed: %+v", got)
	}
	if got := index.groupServices["b"]; len(got) != 2 || got[0].Name != "B" || got[1].ID != "last" {
		t.Fatalf("empty ServiceIDs fallback changed: %+v", got)
	}
	snapshot.Services = append(snapshot.Services, application.Service{ID: "same", GroupID: "a", Name: "duplicate"})
	index = newDesktopServiceCatalog(snapshot)
	if _, found := index.service("a", "same"); found {
		t.Fatal("duplicate scoped identity silently selected one service")
	}
	if service, found := index.service("b", "same"); !found || service.Name != "B" {
		t.Fatal("cross-group ID was treated as a scoped duplicate")
	}
}

func TestServiceConfigIndexPreservesAmbiguityAndEditingOwnership(t *testing.T) {
	const source = `{"groups":[{"id":"a","services":[{"id":"same","enabled":false}]},{"id":"b","services":[{"id":"same","enabled":true}]}]}`
	s := &app{settingsUnit: settingsController{state: settingsState{configJSON: source}}}
	if s.serviceEnabled("a", "same") || !s.serviceEnabled("b", "same") {
		t.Fatal("enabled state leaked across group/service identity")
	}
	if s.readServiceConfig("same") != nil {
		t.Fatal("unscoped ambiguous ID resolved")
	}
	var editable map[string]any
	if err := json.Unmarshal([]byte(source), &editable); err != nil {
		t.Fatal(err)
	}
	findServiceConfig(editable, "same", "a")["enabled"] = true
	if s.serviceEnabled("a", "same") {
		t.Fatal("editing an owned map mutated the borrowed read index")
	}
	s.settingsUnit.state.configJSON = `{"groups":[{"id":"a","services":[{"id":"same","enabled":false},{"id":"same","enabled":true}]}]}`
	if s.readServiceConfig("same", "a") != nil || !s.serviceEnabled("a", "same") {
		t.Fatal("same-group duplicate lost existing nil/default semantics")
	}
	s.settingsUnit.state.configJSON = "{"
	if s.readServiceConfig("same", "a") != nil || !s.serviceEnabled("a", "same") {
		t.Fatal("invalid replacement exposed the old config")
	}
}

func TestServiceProjectionSnapshotPublicationAndEmptySearch(t *testing.T) {
	snapshot := application.Snapshot{Groups: []application.Group{{ID: "g", ServiceIDs: []string{"s"}}}, Services: []application.Service{{ID: "s", GroupID: "g", Name: "Original", RepositoryID: "r", Modules: []application.Module{{ID: "m", Name: "Module"}}}}, Repositories: []application.Repository{{ID: "r", Name: "Repository", RootPath: "/fixture/repository"}}}
	s := &app{snapshot: snapshot}
	first := s.serviceCatalog()
	var state serviceListBrowsingState
	if got := state.project(first, "g", ""); len(got) != 1 || state.lower != nil {
		t.Fatal("empty search constructed lowercase full text")
	}
	snapshot.Services[0].Name = "Changed"
	snapshot.Services[0].Modules[0].Name = "Changed module"
	snapshot.Repositories[0].RootPath = "/fixture/changed"
	s.snapshot = snapshot
	s.workspaceUnit.publishServiceCatalog(newDesktopServiceCatalog(snapshot))
	if first.services[desktopServiceIdentity{"g", "s"}].value.Modules[0].Name != "Module" {
		t.Fatal("publication mutated a retained catalog")
	}
	if got := state.project(s.serviceCatalog(), "g", "CHANGED"); len(got) != 1 || got[0].Name != "Changed" {
		t.Fatal("same-slice snapshot publication missed a changed value")
	}
	if name, path := s.repositoryDetails("r"); name != "Repository" || path != "/fixture/changed" {
		t.Fatal("repository index remained stale")
	}
	if got := state.project(s.serviceCatalog(), "g", " __missing__ "); len(got) != 0 {
		t.Fatal("unexpected service query result")
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
	state.project(index, "g", "")
	b.Run("empty-query-warm", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			serviceIndexBenchmark = state.project(index, "g", "")
		}
	})
	state.project(index, "g", "__missing__")
	b.Run("same-query-warm", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			serviceIndexBenchmark = state.project(index, "g", "__missing__")
		}
	})
	b.Run("lookup-5000", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for _, service := range index.groupServices["g"] {
				_, _ = index.service("g", service.ID)
				_, _ = s.repositoryDetails(service.RepositoryID)
			}
		}
	})
}

func TestProjectTasksUsesScopedServiceNames(t *testing.T) {
	snapshot := application.Snapshot{Services: []application.Service{{GroupID: "a", ID: "same", Name: "A"}, {GroupID: "b", ID: "same", Name: "B"}}, Tasks: []application.Task{{ID: "task", GroupID: "a", Services: []string{"same"}}}}
	if got := projectTasks(snapshot); len(got) != 1 || !slices.Equal(got[0].services, []string{"A"}) {
		t.Fatalf("task service name crossed group scope: %+v", got)
	}
}
