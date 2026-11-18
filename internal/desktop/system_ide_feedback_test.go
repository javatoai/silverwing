package desktop

import (
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"

	"github.com/egoist/mygo/ui"
)

func TestSystemIDEFailureLinksToSelectedToolSettings(t *testing.T) {
	s := &app{page: "tasks"}
	s.snapshot.Home = t.TempDir()
	request, err := s.beginSystemOpenJob(application.Action{Kind: "system.ide", Path: t.TempDir()})
	if err != nil || request == nil {
		t.Fatalf("begin IDE open: %v", err)
	}
	failure := &application.DevelopmentToolUnavailableError{Tool: domain.DevelopmentToolWebStorm}
	s.finishSystemOpenJob(request, application.ActionResult{}, failure)
	s.completeSystemOpenJob(request)
	assertDevelopmentToolSettingsToast(t, s, failure)
}

func TestProductionIDEFailureLinksToSelectedToolSettings(t *testing.T) {
	s, v, b, backend, window := productionRequestFixture(t)
	s.productionRequestFor(v, application.Action{Kind: "production.open-ide", Payload: map[string]any{"batchId": b.ID}}, "")
	productionTestCall(t, backend)
	failure := &application.DevelopmentToolUnavailableError{Tool: domain.DevelopmentToolWebStorm}
	backend.replies <- productionTestAnswer{err: failure}
	productionTestUpdate(t, window)
	assertDevelopmentToolSettingsToast(t, s, failure)
}

func assertDevelopmentToolSettingsToast(t *testing.T, s *app, failure *application.DevelopmentToolUnavailableError) {
	t.Helper()
	tester := ui.NewTester(func(c *ui.Context) {
		setTheme(c, false)
		s.renderOperationNotifications(c)
	}, 800, 600)
	tester.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
	if !tester.HasText(failure.Error()) {
		t.Fatalf("IDE failure missing from feedback: %v", tester.Texts())
	}
	if err := tester.Click("打开 IDE 设置"); err != nil {
		t.Fatal(err)
	}
	settings := &s.settingsUnit.state
	if s.page != "settings" || settings.settingsTab != "工具与连接" || settings.settingsPage != "dev-tools" || settings.settingsSearchTarget != failure.Tool.DisplayName() {
		t.Fatalf("IDE settings target was not selected: page=%q, tab=%q, settings page=%q, target=%q", s.page, settings.settingsTab, settings.settingsPage, settings.settingsSearchTarget)
	}
}
