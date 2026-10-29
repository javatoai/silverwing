package desktop

import (
	"context"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

type meeglePopoverTestBackend struct {
	actions       chan application.Action
	completed     chan struct{}
	identityCalls int
}

func (b *meeglePopoverTestBackend) Dispatch(_ context.Context, action application.Action) (application.ActionResult, error) {
	b.actions <- action
	return application.ActionResult{}, nil
}
func (b *meeglePopoverTestBackend) Snapshot(context.Context) (application.Snapshot, error) {
	if b.completed != nil {
		b.completed <- struct{}{}
	}
	return application.Snapshot{}, nil
}
func (b *meeglePopoverTestBackend) RequirementIdentity(context.Context) (application.RequirementIdentitySnapshot, error) {
	b.identityCalls++
	return application.RequirementIdentitySnapshot{State: application.RequirementIdentityAuthenticated, Identity: "reqid-v1:test-account"}, nil
}

func newMeeglePopoverTestApp(t *testing.T) (*app, *meeglePopoverTestBackend) {
	t.Helper()
	backend := &meeglePopoverTestBackend{actions: make(chan application.Action, 8), completed: make(chan struct{}, 8)}
	s := newApp(backend, Options{CapturePath: "isolated-test"})
	s.page, s.settingsUnit.state.settingsPage = "settings", "cli-meegle"
	s.snapshot.Home = t.TempDir()
	s.settingsUnit.state.configJSON = `{"meegleProjects":[{"project_key":"existing","simple_name":"Existing"}]}`
	s.snapshot.ConfigJSON = s.settingsUnit.state.configJSON
	s.settingsUnit.state.settingsCLIStatus = map[string]map[string]any{"meegle": {"installed": true, "authenticated": true, "host": "fixture"}}
	identity, _ := s.currentMeegleProjectCatalogIdentity()
	s.settingsUnit.state.meegleProjectCatalog = &meegleProjectCatalogState{
		loaded: true, identity: identity, context: s.currentRequirementIdentityContext(), verifiedIdentity: "reqid-v1:test-account",
		projects: []meegleCatalogProject{{Name: "Existing", ProjectKey: "existing", SimpleName: "Existing"}, {Name: "Alpha Project", ProjectKey: "alpha-key", SimpleName: "alpha"}, {Name: "Beta Project", ProjectKey: "beta-key", SimpleName: "beta"}},
	}
	return s, backend
}

func waitMeeglePopoverSave(t *testing.T, backend *meeglePopoverTestBackend) application.Action {
	t.Helper()
	var action application.Action
	select {
	case action = <-backend.actions:
	case <-time.After(3 * time.Second):
		t.Fatal("selection did not dispatch a save")
	}
	select {
	case <-backend.completed:
	case <-time.After(3 * time.Second):
		t.Fatal("save did not complete")
	}
	return action
}

func TestMeegleProjectPopoverSelectsDirectlyAndRetainsFailures(t *testing.T) {
	s, backend := newMeeglePopoverTestApp(t)
	tester := ui.NewTester(func(c *ui.Context) { s.settingsMeegleProjects(c, setTheme(c, false)) }, 900, 650)
	if err := tester.Click("添加项目空间"); err != nil {
		t.Fatal(err)
	}
	if !s.settingsUnit.state.meegleProjectCatalog.pickerOpen || !tester.Focused("搜索 Meegle 项目空间") || tester.HasText("添加 Meegle 项目空间") {
		t.Fatal("add button did not directly open and focus the searchable popover")
	}
	if _, ok := tester.Find("Existing · Existing · existing"); ok {
		t.Fatal("configured project was offered again")
	}
	anchor, _ := tester.Find("添加项目空间")
	search, _ := tester.Find("搜索 Meegle 项目空间")
	if search.Y < anchor.Y+anchor.H || search.X < anchor.X-2 {
		t.Fatalf("popover is not anchored to the add button: %+v / %+v", anchor, search)
	}
	tester.Key(0, ui.KeyDown)
	if s.settingsUnit.state.meegleProjectCatalog.pickerHighlight != 1 {
		t.Fatal("Down did not move the highlighted candidate")
	}
	tester.Key(0, ui.KeyUp)
	if s.settingsUnit.state.meegleProjectCatalog.pickerHighlight != 0 {
		t.Fatal("Up did not move the highlighted candidate")
	}
	if directory := os.Getenv("SILVERWING_LIGHTBOX_EVIDENCE_DIR"); directory != "" {
		file, err := os.Create(filepath.Join(directory, "meegle-project-popover.png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(file, tester.Image())
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("save fixture: %v / %v", err, closeErr)
		}
	}
	tester.Type("beta-key")
	if tester.HasText("Alpha Project") || !tester.HasText("Beta Project") {
		t.Fatal("Key filter did not narrow candidates")
	}
	tester.Key(0, ui.KeyEnter)
	action := waitMeeglePopoverSave(t, backend)
	if action.Kind != "config.fields.save" || payloadString(action.Payload, "meegleProjectPickerSaveProjectKey") != "beta-key" {
		t.Fatalf("Enter did not directly save selected project: %+v", action)
	}
	if s.meegleProjectKeyAlreadyConfigured("beta-key") || s.settingsUnit.state.meegleProjectCatalog.pickerPendingSave == 0 {
		t.Fatal("UI claimed success before the save completed")
	}
	tester.Key(0, ui.KeyEnter)
	select {
	case duplicate := <-backend.actions:
		t.Fatalf("duplicate save: %+v", duplicate)
	default:
	}
	s.busy = false
	s.finishMeegleProjectPickerSave(action, application.ActionResult{}, errors.New("isolated failure"), nil)
	tester.Frame()
	if !s.settingsUnit.state.meegleProjectCatalog.pickerOpen || !tester.HasText("isolated failure") || s.meegleProjectKeyAlreadyConfigured("beta-key") {
		t.Fatal("failed addition did not retain retry state")
	}
	tester.Key(0, ui.KeyEnter)
	action = waitMeeglePopoverSave(t, backend)
	s.busy = false
	s.finishMeegleProjectPickerSave(action, application.ActionResult{}, nil, nil)
	tester.Frame()
	if s.settingsUnit.state.meegleProjectCatalog.pickerOpen || !s.meegleProjectKeyAlreadyConfigured("beta-key") || !tester.Focused("添加项目空间") {
		t.Fatalf("successful save state: open=%t configured=%t focus=%t error=%q", s.settingsUnit.state.meegleProjectCatalog.pickerOpen, s.meegleProjectKeyAlreadyConfigured("beta-key"), tester.Focused("添加项目空间"), s.settingsUnit.state.meegleProjectCatalog.pickerSaveError)
	}
	if err := tester.Click("添加项目空间"); err != nil {
		t.Fatal(err)
	}
	tester.Key(0, ui.KeyEscape)
	if s.settingsUnit.state.meegleProjectCatalog.pickerOpen || !tester.Focused("添加项目空间") {
		t.Fatal("Escape did not close and restore focus")
	}
	if err := tester.Click("添加项目空间"); err != nil {
		t.Fatal(err)
	}
	tester.ClickAt(850, 600)
	if s.settingsUnit.state.meegleProjectCatalog.pickerOpen {
		t.Fatal("outside click did not close picker")
	}
}

func TestMeegleProjectCatalogCloseCancelsReadAndRejectsLateResult(t *testing.T) {
	s, _ := newMeeglePopoverTestApp(t)
	state := s.settingsUnit.state.meegleProjectCatalog
	state.pickerOpen, state.loading, state.loaded = true, true, false
	state.generation = 7
	action := application.Action{Kind: "meegle.projects", Payload: map[string]any{"catalogGeneration": "7"}}
	request, err := s.beginCLIJob(action)
	if err != nil {
		t.Fatal(err)
	}
	s.closeMeegleProjectPicker()
	if request.ctx.Err() == nil || state.loading || s.toolsUnit.state.cliJobs.requests["meegle.projects"] != nil {
		t.Fatal("closing did not cancel the owned directory read")
	}
	s.finishMeegleProjectCatalog(action, application.ActionResult{Data: map[string]any{"projects": []meegleCatalogProject{{Name: "late", ProjectKey: "late", SimpleName: "late"}}}}, nil)
	if state.loaded || state.projects[0].Name == "late" {
		t.Fatal("closed picker accepted a late directory")
	}
	state.pickerOpen = true
	s.settingsUnit.state.settingsPage = "git"
	s.observeMeegleProjectPicker()
	if state.pickerOpen {
		t.Fatal("leaving the owning settings page kept picker open")
	}
}

func TestMeegleProjectCatalogWorkerUsesConfirmedIdentity(t *testing.T) {
	for _, reuse := range []bool{false, true} {
		s, backend := newMeeglePopoverTestApp(t)
		if !reuse {
			s.settingsUnit.state.meegleProjectCatalog.verifiedIdentity = ""
		}
		request, err := s.beginCLIJob(application.Action{Kind: "meegle.projects", Payload: map[string]any{"catalogGeneration": "1"}})
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = executeCLIJob(backend, request)
		if err != nil {
			t.Fatal(err)
		}
		action := <-backend.actions
		if got := payloadString(action.Payload, "expectedIdentity"); got != "reqid-v1:test-account" {
			t.Fatalf("missing account-bound payload: %q", got)
		}
		if payloadString(request.action.Payload, "expectedIdentity") != "" {
			t.Fatal("worker mutated the UI request payload")
		}
		if reuse && backend.identityCalls != 0 || !reuse && backend.identityCalls != 1 {
			t.Fatalf("identity probe reuse=%t calls=%d", reuse, backend.identityCalls)
		}
		s.settingsUnit.state.configJSON = strings.TrimSuffix(s.settingsUnit.state.configJSON, "}") + `,"meegleExecutablePath":"different"}`
		if s.cliJobRelevant(request) {
			t.Fatal("changed executable accepted the previous account's result")
		}
	}
}

func TestMeegleProjectCatalogAccountChangesDiscardOptions(t *testing.T) {
	s, _ := newMeeglePopoverTestApp(t)
	state := s.settingsUnit.state.meegleProjectCatalog
	state.pickerOpen, state.loading, state.generation = true, true, 9
	action := application.Action{Kind: "meegle.projects", Payload: map[string]any{"catalogGeneration": "9"}}
	request, err := s.beginCLIJob(action)
	if err != nil {
		t.Fatal(err)
	}
	s.settingsUnit.state.configJSON = strings.TrimSuffix(s.settingsUnit.state.configJSON, "}") + `,"meegleExecutablePath":"changed"}`
	s.observeMeegleProjectPicker()
	if state.pickerOpen || state.loading || state.loaded || len(state.projects) != 0 || request.ctx.Err() == nil {
		t.Fatal("config change did not cancel the read and retire account-bound options")
	}
	s, _ = newMeeglePopoverTestApp(t)
	state = s.settingsUnit.state.meegleProjectCatalog
	state.generation = 9
	s.finishMeegleProjectCatalog(action, application.ActionResult{}, &application.RequirementIdentityChangedError{Snapshot: application.RequirementIdentitySnapshot{Identity: "reqid-v1:other", State: application.RequirementIdentityAuthenticated}})
	if state.loaded || len(state.projects) != 0 || state.verifiedIdentity != "" || state.error == "" {
		t.Fatal("backend identity change retained stale options or hid failure")
	}
}

func TestMeegleProjectPopoverNarrowEmptyAndErrorStates(t *testing.T) {
	s, _ := newMeeglePopoverTestApp(t)
	tester := ui.NewTester(func(c *ui.Context) { s.settingsMeegleProjects(c, setTheme(c, false)) }, 480, 600)
	if err := tester.Click("添加项目空间"); err != nil {
		t.Fatal(err)
	}
	panel, ok := tester.Find("选择 Meegle 项目空间")
	if !ok || panel.X < 0 || panel.X+panel.W > 480 || panel.Y < 0 || panel.Y+panel.H > 600 {
		t.Fatalf("narrow popover escaped window: %+v", panel)
	}
	tester.Type("no-matching-project")
	if !tester.HasText("没有匹配的项目。") {
		t.Fatal("missing no-results state")
	}
	s.settingsUnit.state.meegleProjectCatalog.loading = true
	tester.Frame()
	if !tester.HasText("正在加载项目目录…") {
		t.Fatal("missing loading state")
	}
	s.settingsUnit.state.meegleProjectCatalog.loading = false
	s.settingsUnit.state.meegleProjectCatalog.error = "fixture directory failure"
	tester.Frame()
	if !tester.HasText("读取失败：fixture directory failure") {
		t.Fatal("missing retryable error state")
	}
	if _, ok := tester.Find("刷新项目目录"); !ok {
		t.Fatal("missing compact refresh action")
	}
}
