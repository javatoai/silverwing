package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type productionTestAnswer struct {
	result application.ActionResult
	err    error
}
type productionTestBackend struct {
	calls   chan application.Action
	replies chan productionTestAnswer
}

func (b *productionTestBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, nil
}
func (b *productionTestBackend) Dispatch(ctx context.Context, a application.Action) (application.ActionResult, error) {
	select {
	case b.calls <- a:
	case <-ctx.Done():
		return application.ActionResult{}, ctx.Err()
	}
	select {
	case answer := <-b.replies:
		return answer.result, answer.err
	case <-ctx.Done():
		return application.ActionResult{}, ctx.Err()
	}
}

func productionFixture(t *testing.T) (*app, *productionView, production.Batch) {
	t.Helper()
	c := domain.DefaultAppConfig()
	c.Groups[0].Name = "国内组"
	c.Groups[0].Services = []domain.GroupServiceConfig{{ID: "service", RepositoryID: "repo", DisplayName: "示例服务", Enabled: true, MasterBranch: "origin/master", TestTagBaselineRef: "origin/release/test", DevelopmentTool: domain.DevelopmentToolVisualStudioCode, Modules: []domain.ServiceModuleConfig{}}}
	c.Groups[0].Services[0].Bootstrap = domain.BootstrapConfig{CopyRules: []domain.BootstrapCopyRule{}, Commands: []domain.BootstrapCommand{}}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var decoded domain.AppConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("production fixture must use readable configuration: %v", err)
	}
	s := newApp(nil, Options{Page: "production"})
	s.loading = false
	s.configJSON = string(data)
	s.snapshot = application.Snapshot{Home: t.TempDir(), Services: []application.Service{{ID: "service", GroupID: domain.DefaultGroupID, Name: "示例服务", RepositoryID: "repo", MasterBranch: "origin/master"}}, Groups: []application.Group{{ID: domain.DefaultGroupID, Name: "国内组"}}, Repositories: []application.Repository{{ID: "repo", Name: "synthetic-repo", RootPath: "C:/synthetic/repo-one"}}}
	selection := production.Selection{GroupID: domain.DefaultGroupID, ServiceID: "service"}
	v := &productionView{Selection: selection, Version: "release/2.3.44", Branch: "feature/synthetic"}
	target := production.Target{Selection: selection, ServiceName: "示例服务", RepositoryID: "repo", Repository: "C:/synthetic/repo-one", Remote: "origin", MainBranch: "master", IDE: string(domain.DevelopmentToolVisualStudioCode)}
	v.Preview = &production.Preview{Target: target, ProductionTag: "2.3.43", ProductionSHA: strings.Repeat("a", 40), MainSHA: strings.Repeat("a", 40), ReleaseSHA: strings.Repeat("c", 40), ProductionInMain: true, SuggestedVersion: "2.3.44", ProductionStatus: domain.GenbuTagProbeStatus{Production: domain.GenbuSuccess}, Branches: []string{"master", "feature/synthetic", "release/2.3.44"}, Tags: []string{"2.3.43"}}
	b := production.Batch{ID: strings.Repeat("b", 32), Target: target, Version: "2.3.44", ReleaseBranch: "release/2.3.44", Phase: "READY", InitialSHA: strings.Repeat("a", 40), ReleaseSHA: strings.Repeat("c", 40), Branches: []production.MergedBranch{{Branch: "feature/synthetic", SourceSHA: strings.Repeat("d", 40)}}, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	v.BatchID = b.ID
	state := s.ensureProduction()
	state.selected = productionKey(selection)
	state.views[state.selected] = v
	state.choices = []productionServiceChoice{productionChoice(s.snapshot.Services[0], s.snapshot)}
	state.batches = []production.Batch{b}
	state.loadedHome = s.snapshot.Home
	state.configScope = productionConfigScope(s.configJSON)
	return s, v, b
}

func TestProductionSameNameServicesShowAndSearchRepositoryIdentity(t *testing.T) {
	s, _, _ := productionFixture(t)
	other := application.Service{ID: "service", GroupID: "other", Name: "示例服务", RepositoryID: "repo-two", MasterBranch: "origin/master"}
	s.snapshot.Groups = append(s.snapshot.Groups, application.Group{ID: "other", Name: "海外组"})
	s.snapshot.Repositories = append(s.snapshot.Repositories, application.Repository{ID: "repo-two", Name: "synthetic-overseas", RootPath: "C:/synthetic/overseas-repo"})
	s.production.choices = append(s.production.choices, productionChoice(other, s.snapshot))
	selection := production.Selection{GroupID: other.GroupID, ServiceID: other.ID}
	s.production.views[productionKey(selection)] = &productionView{Selection: selection}
	for _, dark := range []bool{false, true} {
		tester := ui.NewTester(func(c *ui.Context) {
			p := setTheme(c, dark)
			ui.Row(c).Fill().AlignItems(ui.Stretch).Background(p.bg).Children(func() { s.sidebar(c, p, true); s.renderProductionPage(c, p) })
		}, 960, 900)
		for _, text := range []string{"国内组 · 示例服务", "海外组 · 示例服务"} {
			if !tester.HasText(text) {
				t.Fatalf("same-name service identity not visible: %s", text)
			}
		}
		for _, text := range []string{"国内组 · synthetic-repo", "origin/master · C:/synthetic/repo-one"} {
			if tester.HasText(text) {
				t.Fatalf("verbose service metadata is still visible: %s", text)
			}
		}
		if directory := os.Getenv("SILVERWING_PRODUCTION_EVIDENCE_DIR"); directory != "" {
			if err := os.MkdirAll(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			file, err := os.Create(filepath.Join(directory, fmt.Sprintf("production-960-dark-%t-duplicate.png", dark)))
			if err != nil {
				t.Fatal(err)
			}
			encodeErr, closeErr := png.Encode(file, tester.Image()), file.Close()
			if encodeErr != nil || closeErr != nil {
				t.Fatalf("capture: %v / %v", encodeErr, closeErr)
			}
		}
	}
	for _, query := range []string{"海外组", "synthetic-overseas", "overseas-repo", "repo-two"} {
		*s.input("production-service-query", "") = query
		tester := ui.NewTester(func(c *ui.Context) { s.renderProductionPage(c, setTheme(c, false)) }, 960, 900)
		if !tester.HasText("海外组 · 示例服务") || tester.HasText("国内组 · 示例服务") {
			t.Fatalf("repository/group search %q did not isolate target", query)
		}
	}
}

func TestProductionGitConfirmationsIdentifyRepository(t *testing.T) {
	s, v, b := productionFixture(t)
	s.win = &mygo.Window{}
	p := *v.Preview
	p.ReleaseSHA = b.ReleaseSHA
	p.BranchHeads = map[string]string{v.Branch: strings.Repeat("d", 40)}
	b.TagPlan = &production.TagPlan{Token: "synthetic-token", Tag: "2.3.44", Commit: b.ReleaseSHA, MainSHA: v.Preview.MainSHA, Preview: *v.Preview}
	b.Intent = &production.Intent{Merge: &production.Merge{TargetBranch: "master", BeforeSHA: p.MainSHA}}
	for _, tc := range []struct {
		name string
		open func()
	}{
		{"create", func() { s.confirmProductionCreate(v) }},
		{"feature", func() { s.confirmProductionFeature(v, p, v.Branch) }},
		{"sync", func() { s.confirmProductionSync(v, p) }},
		{"pending", func() { s.confirmProductionPending(v, b, p) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.open()
			if s.confirmationDialog == nil {
				t.Fatal("missing confirmation")
			}
			for _, identity := range []string{b.Target.Repository, b.Target.RepositoryID, b.Target.GroupID, "origin/master"} {
				if !strings.Contains(s.confirmationDialog.Detail, identity) {
					t.Fatalf("missing confirmed repository identity %s", identity)
				}
			}
			s.cancelConfirmationDialog()
		})
	}
}

func TestProductionRemovedServiceBatchRemainsSelectableAfterReload(t *testing.T) {
	s, _, b := productionFixture(t)
	state := s.production
	state.choices, state.selected, state.views = nil, "__history__", map[string]*productionView{"__history__": {}}
	state.addHistoricalChoices(application.Snapshot{})
	v := s.productionView()
	if len(state.choices) != 1 || !state.choices[0].Historical || v == nil || v.Selection != b.Target.Selection || state.choices[0].RepositoryPath != b.Target.Repository {
		t.Fatal("removed service stranded its batch")
	}
	state.addHistoricalChoices(application.Snapshot{})
	if len(state.choices) != 1 {
		t.Fatal("history service duplicated")
	}
	tester := productionIterationTester(s, 960, false)
	if err := tester.Click("更多生产发布操作"); err != nil {
		t.Fatal(err)
	}
	if !tester.HasText("示例服务（历史）") || !tester.HasText("结束批次") || !tester.HasText(b.ReleaseBranch) {
		t.Fatal("removed service history/end action inaccessible")
	}
}

func TestProductionEndedBatchKeepsUnconfirmedTagVisible(t *testing.T) {
	s, _, b := productionFixture(t)
	b.Ended, b.Phase = true, "ENDED"
	b.Intent = &production.Intent{Kind: "tag", Step: "tag-push", Tag: "2.3.44", TagCommit: b.ReleaseSHA, TagObject: strings.Repeat("e", 40)}
	s.production.batches[0] = b
	for _, dark := range []bool{false, true} {
		tester := ui.NewTester(func(c *ui.Context) {
			p := setTheme(c, dark)
			ui.Row(c).Fill().AlignItems(ui.Stretch).Background(p.bg).Children(func() { s.sidebar(c, p, true); s.renderProductionPage(c, p) })
		}, 960, 900)
		if !tester.HasText("未确认 Tag：2.3.44") || !tester.HasText("核对远端结果") {
			t.Fatal("ending hid an ambiguous Tag push")
		}
		if directory := os.Getenv("SILVERWING_PRODUCTION_EVIDENCE_DIR"); directory != "" {
			if err := os.MkdirAll(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			file, err := os.Create(filepath.Join(directory, fmt.Sprintf("production-960-dark-%t-ended.png", dark)))
			if err != nil {
				t.Fatal(err)
			}
			encodeErr, closeErr := png.Encode(file, tester.Image()), file.Close()
			if encodeErr != nil || closeErr != nil {
				t.Fatalf("capture: %v/%v", encodeErr, closeErr)
			}
		}
	}
}

func TestProductionNavigationUsesIndependentGlobalTagGate(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		s := &app{configJSON: fmt.Sprintf(`{"tagEnabled":%t}`, enabled)}
		found := false
		for _, item := range s.sidebarNavigationItems() {
			if item.page == "production" {
				found = true
			}
		}
		if found != enabled {
			t.Fatalf("production navigation=%t, enabled=%t", found, enabled)
		}
	}
	if !validPage("production") || validPage("tags") {
		t.Fatal("production route changed test Tag routing")
	}
}

func TestProductionLayoutThemesAndWidths(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			for _, conflict := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d-dark-%t-conflict-%t", width, dark, conflict), func(t *testing.T) {
					s, _, b := productionFixture(t)
					if conflict {
						b.Phase = "CONFLICT"
						b.Intent = &production.Intent{Kind: "feature", Step: "feature", Merge: &production.Merge{TargetBranch: b.ReleaseBranch, SourceSHA: strings.Repeat("d", 40), Worktree: "C:/synthetic/production-worktrees/batch/merge", Conflicts: []string{"src/long-directory/示例文件.go"}}}
						s.production.batches[0] = b
					}
					tester := ui.NewTester(func(c *ui.Context) {
						p := setTheme(c, dark)
						ui.MenuScope(c, s.popupMenuScope())
						ui.Row(c).Fill().AlignItems(ui.Stretch).Background(p.bg).Children(func() { s.sidebar(c, p, true); s.renderProductionPage(c, p) })
						ui.ThemedMenus(c, s.popupMenuScope())
					}, width, 900)
					control := "合并到 release"
					if conflict {
						control = "校验并继续"
					}
					if !tester.HasText("生产发布") || !tester.HasText(control) {
						t.Fatalf("missing production controls: %s", control)
					}
					for _, name := range []string{"搜索生产发布服务", control, "构建 Tag"} {
						// Independent cards may put the last action below the initial
						// viewport. Verify it after the same scroll available to users.
						productionRevealControl(t, tester, name)
						rect, ok := tester.Find(name)
						if !ok || rect.W <= 0 || rect.H <= 0 || rect.X < 0 || rect.X+rect.W > float32(width)+1 || rect.Y+rect.H > 900 {
							t.Fatalf("%s overflow: %+v", name, rect)
						}
					}
					if directory := os.Getenv("SILVERWING_PRODUCTION_EVIDENCE_DIR"); directory != "" {
						if err := os.MkdirAll(directory, 0o700); err != nil {
							t.Fatal(err)
						}
						path := filepath.Join(directory, fmt.Sprintf("production-%d-dark-%t-conflict-%t.png", width, dark, conflict))
						file, err := os.Create(path)
						if err != nil {
							t.Fatal(err)
						}
						err = png.Encode(file, tester.Image())
						closeErr := file.Close()
						if err != nil || closeErr != nil {
							t.Fatalf("capture: %v / %v", err, closeErr)
						}
					}
				})
			}
		}
	}
}

func TestProductionCreateLayout(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s, v, _ := productionFixture(t)
				s.production.batches = nil
				v.BatchID = ""
				tester := ui.NewTester(func(c *ui.Context) {
					p := setTheme(c, dark)
					ui.MenuScope(c, s.popupMenuScope())
					ui.Row(c).Fill().AlignItems(ui.Stretch).Background(p.bg).Children(func() { s.sidebar(c, p, true); s.renderProductionPage(c, p) })
					ui.ThemedMenus(c, s.popupMenuScope())
				}, width, 900)
				for _, name := range []string{"release 分支名", "创建 release"} {
					rect, ok := tester.Find(name)
					if !ok || rect.W <= 0 || rect.X+rect.W > float32(width)+1 {
						t.Fatalf("creation control %s overflow: %+v", name, rect)
					}
				}
				if directory := os.Getenv("SILVERWING_PRODUCTION_EVIDENCE_DIR"); directory != "" {
					if err := os.MkdirAll(directory, 0o700); err != nil {
						t.Fatal(err)
					}
					file, err := os.Create(filepath.Join(directory, fmt.Sprintf("production-%d-dark-%t-create.png", width, dark)))
					if err != nil {
						t.Fatal(err)
					}
					err = png.Encode(file, tester.Image())
					closeErr := file.Close()
					if err != nil || closeErr != nil {
						t.Fatalf("capture: %v/%v", err, closeErr)
					}
				}
			})
		}
	}
}

func TestProductionBackgroundCompletionPreservesPageTaskDraftAndSelection(t *testing.T) {
	s, v, b := productionFixture(t)
	backend := &productionTestBackend{calls: make(chan application.Action, 2), replies: make(chan productionTestAnswer, 2)}
	window := &refreshTestWindow{updates: make(chan func(), 8)}
	s.backend, s.production.window = backend, window
	s.tasks = []task{{id: "current", title: "current task"}}
	s.notes[0] = "unsaved draft"
	s.productionRequest(application.Action{Kind: "production.refresh-build", Payload: map[string]any{"batchId": b.ID}}, "")
	select {
	case <-backend.calls:
	case <-time.After(time.Second):
		t.Fatal("no worker")
	}
	s.productionRequest(application.Action{Kind: "production.refresh-build"}, "")
	if len(backend.calls) != 0 {
		t.Fatal("duplicate worker admitted")
	}
	s.page = "tasks"
	v.BatchID = "another-selected-batch"
	b.Message = "background result"
	backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batch: &b}}}
	select {
	case update := <-window.updates:
		update()
	case <-time.After(time.Second):
		t.Fatal("no UI completion")
	}
	if s.page != "tasks" || s.currentTask().id != "current" || s.notes[0] != "unsaved draft" || v.BatchID != "another-selected-batch" {
		t.Fatal("background job changed page/task/draft/batch selection")
	}
	if v.Busy || s.production.batches[0].Message != "background result" {
		t.Fatal("background result not applied")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.closeProductionJobs(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestProductionLateReadAfterInputChangeAndCloseCancellation(t *testing.T) {
	s, v, _ := productionFixture(t)
	backend := &productionTestBackend{calls: make(chan application.Action, 2), replies: make(chan productionTestAnswer, 2)}
	window := &refreshTestWindow{updates: make(chan func(), 8)}
	s.backend, s.production.window = backend, window
	v.Preview = nil
	s.productionInspect("")
	<-backend.calls
	v.ManualTag = "changed"
	backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Preview: &production.Preview{ProductionTag: "old"}}}}
	select {
	case update := <-window.updates:
		update()
	case <-time.After(time.Second):
		t.Fatal("no completion")
	}
	if v.Preview != nil {
		t.Fatal("late baseline replaced current input")
	}
	s.productionInspect("")
	<-backend.calls
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.closeProductionJobs(ctx); err != nil {
		t.Fatal(err)
	}
	s.production.mu.Lock()
	remaining := len(s.production.workers)
	s.production.mu.Unlock()
	if remaining != 0 {
		t.Fatal("production worker survived close")
	}
}
