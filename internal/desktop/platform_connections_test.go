package desktop

import (
	"strconv"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

func seedPlatformConnectionsForDesktopTest(s *app, genbu, dragon bool) {
	state := s.ensurePlatformConnections()
	state.loaded = true
	state.value = application.PlatformConnections{Username: "fixture", Configured: true, Revision: 1, GenbuEnabled: genbu, DragonEnabled: dragon}
	state.username = state.value.Username
}

func setTestPlatformSwitch(s *app, tool string, enabled bool) {
	state := s.ensurePlatformConnections()
	next := state.value
	if tool == "genbu" {
		next.GenbuEnabled = enabled
	} else {
		next.DragonEnabled = enabled
	}
	s.publishPlatformConnections(state, next)
}

func TestPlatformSettingsLegacyLinksAndSearch(t *testing.T) {
	s := newApp(nil, Options{Page: "settings"})
	s.loading = false
	s.settingsUnit.state.configJSON = `{"genbuExecutablePath":"legacy-genbu.exe","dragonExecutablePath":"legacy-dragon.exe"}`
	seedPlatformConnectionsForDesktopTest(s, true, false)
	for _, tc := range []struct {
		page, query, title string
	}{
		{"cli-genbu", "Genbu CLI 可执行文件", "Genbu 连接"},
		{"cli-dragon", "Dragon CLI 状态", "Dragon 连接"},
	} {
		matches := settingsSearch(tc.query)
		if len(matches) != 1 || matches[0].page != platformConnectionsPage || matches[0].title != tc.title {
			t.Fatalf("legacy search %q = %v", tc.query, matches)
		}
		if !s.navigateSettingsPage(tc.page, tc.query) || s.settingsUnit.state.settingsPage != platformConnectionsPage ||
			s.settingsUnit.state.settingsSearchTarget != tc.title {
			t.Fatal("old route did not open the new service settings")
		}
	}
	tester := ui.NewTester(func(c *ui.Context) { s.settingsScreen(c, setTheme(c, false)) }, 1150, 850)
	for _, label := range []string{"共用平台账号", "平台账号", "平台密码", "保存账号", "Genbu 连接", "Dragon 连接"} {
		if _, ok := tester.Find(label); !ok {
			t.Errorf("settings control %q missing", label)
		}
	}
	for _, label := range []string{"测试 Genbu 登录", "测试 Dragon 登录"} {
		if _, ok := tester.Find(label); ok {
			t.Errorf("manual login test button %q is visible", label)
		}
	}
	for _, obsolete := range []string{"Genbu CLI 可执行文件", "Dragon CLI 可执行文件", "legacy-genbu.exe", "legacy-dragon.exe"} {
		if tester.HasText(obsolete) {
			t.Errorf("old executable setting %q is visible", obsolete)
		}
	}
	if desktopCLIJobSupported("genbu") || desktopCLIJobSupported("dragon") || desktopHasOptionalCLI("genbu") || desktopHasOptionalCLI("dragon") {
		t.Fatal("native integrations are still admitted to the CLI probe path")
	}
	s.navigateSettingsPage(platformConnectionsPage, "")
	tester.Frame()
	if err := tester.Click("平台密码"); err != nil {
		t.Fatal(err)
	}
	const draft = "synthetic-platform-password"
	tester.Type(draft)
	if s.settingsUnit.state.platformConnections.password != draft || tester.HasText(draft) {
		t.Fatal("password input did not mask the entered draft")
	}
}

func TestPlatformSettingsAlignsStatusAndSwitch(t *testing.T) {
	for _, width := range []int{720, 1150} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			s := newApp(nil, Options{Page: "settings"})
			s.loading = false
			seedPlatformConnectionsForDesktopTest(s, true, false)
			s.ensurePlatformConnections().genbu.testMessage = "登录成功"
			if !s.navigateSettingsPage(platformConnectionsPage, "") {
				t.Fatal("could not open platform connections")
			}
			tester := ui.NewTester(func(c *ui.Context) { s.settingsScreen(c, setTheme(c, false)) }, width, 850)
			var previous ui.Rect
			for _, row := range []struct{ name, status string }{{"Genbu", "登录成功"}, {"Dragon", "已停用"}} {
				title, hasTitle := tester.Find(row.name + " 连接")
				status, hasStatus := tester.Find(row.status)
				switchInfo, hasSwitch := tester.AccessByRole(ui.RoleSwitch, "启用 "+row.name)
				if !hasTitle || !hasStatus || !hasSwitch {
					t.Fatalf("incomplete %s row: title=%t status=%t switch=%t", row.name, hasTitle, hasStatus, hasSwitch)
				}
				toggle := switchInfo.Bounds
				if title.X+title.W > status.X || status.X+status.W > toggle.X || status.Y+status.H <= toggle.Y || status.Y >= toggle.Y+toggle.H {
					t.Errorf("%s status is not between its title and switch: title=%+v status=%+v switch=%+v", row.name, title, status, toggle)
				}
				if previous.W > 0 && (previous.X != toggle.X || previous.Y+previous.H > toggle.Y) {
					t.Errorf("platform switches are not vertically stacked and aligned: first=%+v second=%+v", previous, toggle)
				}
				previous = toggle
			}
		})
	}
}

func TestPlatformScopeRetirementOnSwitchAndCredentialChanges(t *testing.T) {
	s := newApp(nil, Options{Page: "settings"})
	s.loading = false
	seedPlatformConnectionsForDesktopTest(s, true, true)
	state := s.settingsUnit.state.platformConnections
	genbuContext, dragonContext := s.integrationContext("genbu"), s.integrationContext("dragon")
	state.genbu.testMessage, state.dragon.testMessage = "old Genbu test", "old Dragon test"
	setTestPlatformSwitch(s, "genbu", false)
	if genbuContext.Err() == nil || s.integrationReady("genbu") || state.genbu.testMessage != "" {
		t.Fatal("disabling Genbu retained admission or its prior login result")
	}
	if dragonContext.Err() != nil || !s.integrationReady("dragon") || state.dragon.testMessage != "old Dragon test" {
		t.Fatal("disabling Genbu affected Dragon")
	}
	setTestPlatformSwitch(s, "genbu", true)
	newGenbuContext := s.integrationContext("genbu")
	next := state.value
	next.Revision++
	s.publishPlatformConnections(state, next)
	if newGenbuContext.Err() == nil || dragonContext.Err() == nil || state.dragon.testMessage != "" {
		t.Fatal("editing the shared credentials retained an old read scope or login result")
	}
	if !s.integrationReady("genbu") || !s.integrationReady("dragon") {
		t.Fatal("saved credentials require only enabled connections for operation admission")
	}
}

func TestPlatformPasswordPrefillsMaskedAndCanBeRevealed(t *testing.T) {
	const platformPasswordFixture = "sample-platform-account-password"
	s := newApp(nil, Options{Page: "settings"})
	s.loading = false
	seedPlatformConnectionsForDesktopTest(s, true, true)
	state := s.ensurePlatformConnections()
	s.applyPlatformPassword(state, state.value.Revision, platformPasswordFixture, nil)
	if state.password != "" {
		t.Fatal("applied a password outside the platform connection page")
	}
	if !s.navigateSettingsPage(platformConnectionsPage, "") {
		t.Fatal("could not open platform settings")
	}
	s.applyPlatformPassword(state, state.value.Revision+1, platformPasswordFixture, nil)
	if state.password != "" {
		t.Fatal("applied a password from a different account revision")
	}
	s.applyPlatformPassword(state, state.value.Revision, platformPasswordFixture, nil)
	tester := ui.NewTester(func(c *ui.Context) { s.settingsPlatformAccount(c, setTheme(c, false), state) }, 900, 300)
	tester.Frame()
	if field, ok := tester.Access("平台密码"); !ok || !field.Password || field.Value != "" ||
		state.password != platformPasswordFixture || state.passwordEdited {
		t.Fatal("saved password was not prefilled and masked")
	}
	if button, ok := tester.AccessByRole(ui.RoleButton, "显示密码"); !ok || !button.Focusable || button.Bounds.W != 30 || button.Bounds.H != 30 || button.Description != "显示密码" {
		t.Fatalf("expected an accessible icon-only button, got %+v", button)
	}
	if err := tester.Click("显示密码"); err != nil {
		t.Fatal(err)
	}
	if field, ok := tester.Access("平台密码"); !ok || field.Password || field.Value != platformPasswordFixture {
		t.Fatal("show password did not reveal the prefilled value")
	}
	if button, ok := tester.AccessByRole(ui.RoleButton, "隐藏密码"); !ok || button.Description != "隐藏密码" {
		t.Fatal("visibility button did not update its accessible name and tooltip")
	}
	if err := tester.Click("隐藏密码"); err != nil {
		t.Fatal(err)
	}
	if field, ok := tester.Access("平台密码"); !ok || !field.Password || field.Value != "" {
		t.Fatal("hide password did not mask the value again")
	}
	if err := tester.Click("显示密码"); err != nil {
		t.Fatal(err)
	}
	if !s.navigateSettingsPage("basic", "") || !s.navigateSettingsPage(platformConnectionsPage, "") {
		t.Fatal("could not leave and return to platform settings")
	}
	s.applyPlatformPassword(state, state.value.Revision, platformPasswordFixture, nil)
	tester.Frame()
	if field, ok := tester.Access("平台密码"); !ok || !field.Password || field.Value != "" || state.passwordVisible {
		t.Fatal("platform settings returned with the saved password exposed")
	}
	s.page, s.loading = "tasks", true
	s.observePageLocalRead()
	if state.password != "" || state.passwordVisible || state.passwordRequested {
		t.Fatal("leaving platform settings retained the password")
	}
}
