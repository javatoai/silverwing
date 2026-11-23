package desktop

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

const modulePopupTestConfig = `{
	"tagEnabled": true,
	"services": [{
		"id": "service", "repositoryId": "repo", "displayName": "Example service",
		"masterBranch": "origin/main", "testTagBaselineRef": "origin/release/test",
		"modules": [
			{"id": "core", "name": "Core", "strategy": "STANDARD_WORKTREE",
			 "tagEnabled": true, "tagMode": "MERGE_TO_TARGET_BRANCH",
			 "tagTargetRef": "origin/release/core", "tagMessagePrefix": "Core Tag",
			 "customCommands": [{
				"id": "build-core", "name": "Build core", "executable": "go",
				"arguments": ["build", "./..."], "workingDirectory": "core",
				"timeoutSeconds": 600, "enabled": true, "iconKey": "build", "extra": "retained"
			 }]},
			{"id": "client", "name": "Client", "strategy": "INDEPENDENT_CLONE",
			 "tagEnabled": false, "tagMode": "MERGE_TO_TARGET_BRANCH", "tagMessagePrefix": "Client Tag"}
		]
	}]
}`

type modulePopupTestBackend struct {
	actions chan application.Action
}

func (b *modulePopupTestBackend) Dispatch(_ context.Context, action application.Action) (application.ActionResult, error) {
	b.actions <- action
	return application.ActionResult{}, nil
}

func (*modulePopupTestBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{ConfigJSON: modulePopupTestConfig}, nil
}

func newServiceEditorModulePopupTester(t *testing.T) (*app, *serviceEditorState, *modulePopupTestBackend, *ui.Tester) {
	t.Helper()
	backend := &modulePopupTestBackend{actions: make(chan application.Action, 8)}
	s := newApp(backend, Options{CapturePath: "isolated-test"})
	s.settingsUnit.state.configJSON = modulePopupTestConfig
	s.openServiceEditor("service", "basic")
	state := s.workspaceUnit.state.serviceEditor
	if state == nil {
		t.Fatalf("open service editor: %s", s.lastError)
	}
	state.section, state.remotesLoaded = "modules", true
	key := serviceEditorBranchChoiceKey(s.serviceRepositoryChoiceScope(), "origin")
	state.branchChoices[key] = []string{"origin/release/core", "origin/release/staging"}
	state.branchChoicesLoaded[key] = true

	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		ui.Column(c).Fill().Background(p.bg).Children(func() {
			s.actionDialog(c, p)
			s.renderConfirmationDialog(c, p)
		})
	}, 1200, 900)
	return s, state, backend, tester
}

func serviceModulePopupBounds(t *testing.T, tester *ui.Tester, label string) ui.Rect {
	t.Helper()
	bounds, found := tester.Find(label)
	if !found || bounds.W <= 0 || bounds.H <= 0 {
		t.Fatalf("control %q is not visible: %+v", label, bounds)
	}
	return bounds
}

func clickServiceModulePopupControl(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	serviceModulePopupBounds(t, tester, label)
	if err := tester.Click(label); err != nil {
		t.Fatal(err)
	}
}

func cancelServiceModulePopup(t *testing.T, tester *ui.Tester) {
	t.Helper()
	complete := serviceModulePopupBounds(t, tester, "完成")
	// The service editor and popup have separate Cancel buttons. Target
	// the one beside the popup's Complete button.
	tester.ClickAt(complete.X-24, complete.Y+complete.H/2)
}

func focusServiceModulePopupField(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	bounds := serviceModulePopupBounds(t, tester, label)
	clickServiceModulePopupControl(t, tester, label)
	if !tester.Focused(label) {
		// The visible field caption precedes the labeled input in the tree.
		tester.ClickAt(bounds.X+min(bounds.W/2, 32), bounds.Y+bounds.H+12)
	}
	if !tester.Focused(label) {
		t.Fatalf("field %q did not take keyboard focus", label)
	}
}

func typeServiceModulePopupField(t *testing.T, tester *ui.Tester, label, value string) {
	t.Helper()
	focusServiceModulePopupField(t, tester, label)
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type(value)
}

func clickServiceModulePopupSwitch(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	caption := serviceModulePopupBounds(t, tester, label)
	// The switch follows the growing caption in the same row.
	tester.ClickAt(caption.X+caption.W+19, caption.Y+caption.H/2)
}

func serviceModulePopupDraft(t *testing.T, s *app) map[string]any {
	t.Helper()
	config, err := s.buildServiceEditorConfig()
	if err != nil {
		t.Fatalf("build service draft: %v", err)
	}
	return config
}

func assertServiceModulePopupHasNotSaved(t *testing.T, s *app, state *serviceEditorState, backend *modulePopupTestBackend) {
	t.Helper()
	if state.saving || s.busy || s.settingsUnit.state.configJSON != modulePopupTestConfig {
		t.Fatal("editing a module popup changed persisted service settings")
	}
	select {
	case action := <-backend.actions:
		t.Fatalf("module popup dispatched %s before saving the service", action.Kind)
	default:
	}
}

func TestServiceEditorModuleTagPopupDraft(t *testing.T) {
	s, state, backend, tester := newServiceEditorModulePopupTester(t)
	assertSelectedServiceEditorModule(t, state, tester, "core")
	captureDesktopTestFrame(t, tester, "service-editor-module-tabs-core")
	*s.serviceEditorText("name", "Example service") = "Service draft"
	*s.serviceEditorModuleText(&state.modules[0], "name", "Core") = "Core.v2"
	*s.serviceEditorText("testTagBaselineRef", "origin/release/test") = "origin/release/draft"
	tester.Frame()
	before := serviceModulePopupDraft(t, s)

	clickServiceModulePopupControl(t, tester, "测试 Tag · 已启用")
	serviceModulePopupBounds(t, tester, "测试 Tag · Core.v2")
	captureDesktopTestFrame(t, tester, "service-editor-test-tag")
	typeServiceModulePopupField(t, tester, "Tag 信息前缀", "Temporary Tag")
	typeServiceModulePopupField(t, tester, "Tag 目标分支", "invalid-branch")
	if got := serviceModulePopupDraft(t, s); !reflect.DeepEqual(got, before) {
		t.Fatal("typing in the Tag popup changed the service draft")
	}
	clickServiceModulePopupControl(t, tester, "完成")
	if state.modulePopup == nil || !tester.HasText("remote/branch") {
		t.Fatal("invalid explicit target should keep the Tag popup open with a format error")
	}
	cancelServiceModulePopup(t, tester)
	if state.modulePopup != nil || !s.dialogOpen || !reflect.DeepEqual(serviceModulePopupDraft(t, s), before) {
		t.Fatal("cancelling Tag edits lost or changed the service draft")
	}

	clickServiceModulePopupControl(t, tester, "测试 Tag · 已启用")
	if popup := state.modulePopup; popup == nil || popup.tag.MessagePrefix != "Core Tag" {
		t.Fatal("reopening the Tag popup did not restore the original prefix")
	}
	clickServiceModulePopupControl(t, tester, "当前分支直接构建")
	typeServiceModulePopupField(t, tester, "Tag 信息前缀", "Updated Tag")
	clickServiceModulePopupControl(t, tester, "完成")
	if state.modulePopup != nil || !s.dialogOpen {
		t.Fatal("completing Tag edits must return to the service editor")
	}
	updated := serviceModulePopupDraft(t, s)
	modules := mapsFromData(updated["modules"])
	if mapString(updated, "displayName") != "Service draft" || mapString(modules[0], "name") != "Core.v2" {
		t.Fatal("completing Tag edits lost the service or module name draft")
	}
	if mapString(modules[0], "tagMode") != "CURRENT_BRANCH" || modules[0]["tagTargetRef"] != nil ||
		mapString(modules[0], "tagMessagePrefix") != "Updated Tag" {
		t.Fatalf("completing Tag edits did not update the selected module: %#v", modules[0])
	}
	if !reflect.DeepEqual(modules[1], mapsFromData(before["modules"])[1]) {
		t.Fatalf("completing the first module changed its sibling: %#v", modules[1])
	}
	clickServiceModulePopupControl(t, tester, "测试 Tag · 已启用")
	if popup := state.modulePopup; popup == nil || popup.tag.MessagePrefix != "Updated Tag" || tester.HasText("Tag 目标分支") {
		t.Fatal("reopening did not preserve the completed prefix and mode")
	}
	cancelServiceModulePopup(t, tester)

	clickServiceModulePopupControl(t, tester, "Client")
	assertSelectedServiceEditorModule(t, state, tester, "client")
	captureDesktopTestFrame(t, tester, "service-editor-module-tabs-client")
	clickServiceModulePopupControl(t, tester, "测试 Tag · 已关闭")
	serviceModulePopupBounds(t, tester, "测试 Tag · Client")
	clickServiceModulePopupSwitch(t, tester, "启用测试 Tag")
	serviceModulePopupBounds(t, tester, "当前继承：origin/release/draft")
	clickServiceModulePopupControl(t, tester, "完成")
	modules = mapsFromData(serviceModulePopupDraft(t, s)["modules"])
	if !mapBool(modules[1], "tagEnabled", false) || modules[1]["tagTargetRef"] != nil ||
		mapString(modules[0], "tagMessagePrefix") != "Updated Tag" {
		t.Fatalf("enabling the second module did not retain independent Tag drafts: %#v", modules)
	}
	assertServiceModulePopupHasNotSaved(t, s, state, backend)
}

func TestServiceEditorModuleTagPopupCorrectsDisabledTarget(t *testing.T) {
	s, state, backend, tester := newServiceEditorModulePopupTester(t)
	clickServiceModulePopupControl(t, tester, "测试 Tag · 已启用")
	typeServiceModulePopupField(t, tester, "Tag 目标分支", "invalid-branch")
	clickServiceModulePopupSwitch(t, tester, "启用测试 Tag")
	if tester.HasText("Tag 目标分支") {
		t.Fatal("disabling Tag should collapse the settings")
	}
	clickServiceModulePopupControl(t, tester, "完成")
	if state.modulePopup == nil || !tester.HasText("remote/branch") {
		t.Fatal("an invalid explicit target must be correctable even when Tag is disabled")
	}
	typeServiceModulePopupField(t, tester, "Tag 目标分支", "origin/release/s")
	tester.Type("taging")
	clickServiceModulePopupControl(t, tester, "完成")
	if state.modulePopup != nil {
		t.Fatal("a corrected target must allow the popup to close")
	}
	module := mapsFromData(serviceModulePopupDraft(t, s)["modules"])[0]
	if mapBool(module, "tagEnabled", true) || mapString(module, "tagTargetRef") != "origin/release/staging" {
		t.Fatalf("correcting the target did not retain the disabled setting or all typed characters: %#v", module)
	}
	assertServiceModulePopupHasNotSaved(t, s, state, backend)
}

func TestServiceEditorModuleTagPopupEscapeClosesOneLayer(t *testing.T) {
	s, state, backend, tester := newServiceEditorModulePopupTester(t)
	save := serviceModulePopupBounds(t, tester, "保存配置")
	before := serviceModulePopupDraft(t, s)
	clickServiceModulePopupControl(t, tester, "测试 Tag · 已启用")
	focusServiceModulePopupField(t, tester, "Tag 目标分支")
	serviceModulePopupBounds(t, tester, "origin/release/staging")

	tester.Key(0, ui.KeyEscape)
	if tester.HasText("origin/release/staging") || state.modulePopup == nil || !s.dialogOpen {
		t.Fatal("first Escape must dismiss only the branch choices")
	}
	tester.Key(0, ui.KeyEscape)
	if state.modulePopup != nil || !s.dialogOpen || s.confirmationDialog != nil {
		t.Fatal("second Escape must dismiss only the Tag popup")
	}

	clickServiceModulePopupControl(t, tester, "测试 Tag · 已启用")
	tester.ClickAt(save.X+save.W/2, save.Y+save.H/2)
	if state.modulePopup != nil || !s.dialogOpen || !reflect.DeepEqual(serviceModulePopupDraft(t, s), before) {
		t.Fatal("clicking the backdrop over the parent Save must dismiss only the Tag popup")
	}
	assertServiceModulePopupHasNotSaved(t, s, state, backend)
	tester.Key(0, ui.KeyEscape)
	if s.dialogOpen || s.workspaceUnit.state.serviceEditor != nil {
		t.Fatal("Escape must close the unchanged service editor after the popup is dismissed")
	}
}

func TestServiceEditorModuleCommandsPopupDraftAndSave(t *testing.T) {
	s, state, backend, tester := newServiceEditorModulePopupTester(t)
	before := serviceModulePopupDraft(t, s)
	clickServiceModulePopupControl(t, tester, "快捷命令 · 1 条")
	serviceModulePopupBounds(t, tester, "快捷命令 · Core")
	captureDesktopTestFrame(t, tester, "service-editor-shortcut-commands-list")
	if tester.HasText("可执行程序") {
		t.Fatal("opening the shortcuts popup should show the list")
	}
	clickServiceModulePopupControl(t, tester, "Build core")
	typeServiceModulePopupField(t, tester, "命令名称", "Discarded edit")
	clickServiceModulePopupControl(t, tester, "返回列表")
	clickServiceModulePopupControl(t, tester, "添加命令")
	typeServiceModulePopupField(t, tester, "命令名称", "Discarded addition")
	clickServiceModulePopupControl(t, tester, "返回列表")
	clickServiceModulePopupControl(t, tester, "Discarded edit")
	clickServiceModulePopupControl(t, tester, "删除快捷命令")
	serviceModulePopupBounds(t, tester, "Discarded addition")
	if tester.HasText("Discarded edit") || !reflect.DeepEqual(serviceModulePopupDraft(t, s), before) {
		t.Fatal("deleting a command must change only the popup list")
	}
	cancelServiceModulePopup(t, tester)
	if state.modulePopup != nil || !s.dialogOpen || s.serviceEditorDirty() || !reflect.DeepEqual(serviceModulePopupDraft(t, s), before) {
		t.Fatal("cancelling shortcuts edits changed the service draft")
	}
	assertServiceModulePopupHasNotSaved(t, s, state, backend)

	clickServiceModulePopupControl(t, tester, "快捷命令 · 1 条")
	if tester.HasText("Discarded addition") {
		t.Fatal("reopening shortcuts restored a discarded command")
	}
	clickServiceModulePopupControl(t, tester, "Build core")
	typeServiceModulePopupField(t, tester, "命令名称", "Compile core")
	clickServiceModulePopupControl(t, tester, "返回列表")
	clickServiceModulePopupControl(t, tester, "添加命令")
	typeServiceModulePopupField(t, tester, "命令名称", "Run tests")
	typeServiceModulePopupField(t, tester, "可执行程序", "go")
	clickServiceModulePopupControl(t, tester, "完成")
	if state.modulePopup != nil || !s.dialogOpen || !s.serviceEditorDirty() {
		t.Fatal("completing the shortcuts popup from an edit did not return to the service editor")
	}
	clickServiceModulePopupControl(t, tester, "Client")
	assertSelectedServiceEditorModule(t, state, tester, "client")
	serviceModulePopupBounds(t, tester, "快捷命令 · 0 条")
	clickServiceModulePopupControl(t, tester, "Core")
	assertSelectedServiceEditorModule(t, state, tester, "core")
	serviceModulePopupBounds(t, tester, "快捷命令 · 2 条")
	updated := serviceModulePopupDraft(t, s)
	modules := mapsFromData(updated["modules"])
	if !reflect.DeepEqual(modules[1], mapsFromData(before["modules"])[1]) {
		t.Fatalf("completing the shortcuts popup changed another module: %#v", modules[1])
	}
	commands := mapsFromData(modules[0]["customCommands"])
	if len(commands) != 2 {
		t.Fatalf("completed shortcuts did not update the service draft: %#v", commands)
	}
	expectedCommand := mapsFromData(mapsFromData(before["modules"])[0]["customCommands"])[0]
	expectedCommand["name"] = "Compile core"
	if !reflect.DeepEqual(commands[0], expectedCommand) {
		t.Fatalf("editing a shortcut changed its ID or other fields: %#v", commands[0])
	}
	if mapString(commands[1], "name") != "Run tests" || mapString(commands[1], "executable") != "go" ||
		mapString(commands[1], "id") == "" || mapString(commands[1], "id") == "build-core" {
		t.Fatalf("new shortcut did not retain its draft fields and own ID: %#v", commands[1])
	}
	assertServiceModulePopupHasNotSaved(t, s, state, backend)
	clickServiceModulePopupControl(t, tester, "保存配置")
	select {
	case action := <-backend.actions:
		if action.Kind != "service.save" || !reflect.DeepEqual(action.Payload["service"], updated) {
			t.Fatalf("Save did not dispatch the completed service draft: %#v", action)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("saving the service did not dispatch service.save")
	}
}

func TestServiceEditorModuleCommandsPopupScrollsShortViewport(t *testing.T) {
	s, state, backend, tester := newServiceEditorModulePopupTester(t)
	clickServiceModulePopupControl(t, tester, "快捷命令 · 1 条")
	clickServiceModulePopupControl(t, tester, "Build core")
	const width, height = 720, 420
	tester.SetSize(width, height)
	body := serviceModulePopupBounds(t, tester, "模块配置内容")
	footer := serviceModulePopupBounds(t, tester, "完成")
	name := serviceModulePopupBounds(t, tester, "命令名称")
	if footer.X < 0 || footer.Y < body.Y+body.H || footer.X+footer.W > width || footer.Y+footer.H > height {
		t.Fatalf("short viewport clipped the footer or let it overlap the body: body=%+v footer=%+v", body, footer)
	}
	captureDesktopTestFrame(t, tester, "service-editor-shortcut-command-edit-narrow")

	tester.Scroll(body.X+body.W*.7, body.Y+body.H*.75, 0, 1000)
	if got, _ := tester.Find("命令名称"); got.Y == name.Y && got.H == name.H {
		t.Fatal("scrolling the popup body did not move the first field")
	}
	timeout := serviceModulePopupBounds(t, tester, "超时（秒）")
	if timeout.Y < body.Y || timeout.Y+timeout.H > body.Y+body.H {
		t.Fatalf("scrolling did not reveal the later field in the popup body: body=%+v timeout=%+v", body, timeout)
	}
	if got := serviceModulePopupBounds(t, tester, "完成"); got != footer {
		t.Fatalf("scrolling the body moved the footer: before=%+v after=%+v", footer, got)
	}
	typeServiceModulePopupField(t, tester, "超时（秒）", "300")
	clickServiceModulePopupControl(t, tester, "完成")
	if state.modulePopup != nil || !s.dialogOpen {
		t.Fatal("the popup footer was not interactive after scrolling")
	}
	module := mapsFromData(serviceModulePopupDraft(t, s)["modules"])[0]
	command := mapsFromData(module["customCommands"])[0]
	if configNumberString(command["timeoutSeconds"], "") != "300" {
		t.Fatalf("the field revealed by scrolling could not be edited: %#v", command)
	}
	assertServiceModulePopupHasNotSaved(t, s, state, backend)
}

func TestServiceEditorModulePopupTriggersWrapOnNarrowWindow(t *testing.T) {
	_, _, _, tester := newServiceEditorModulePopupTester(t)
	tag := serviceModulePopupBounds(t, tester, "测试 Tag · 已启用")
	command := serviceModulePopupBounds(t, tester, "快捷命令 · 1 条")
	if tag.Y != command.Y || tag.X+tag.W > command.X {
		t.Fatalf("the module actions should share a row at wide widths: Tag=%+v commands=%+v", tag, command)
	}

	const width = 450
	tester.SetSize(width, 900)
	tag = serviceModulePopupBounds(t, tester, "测试 Tag · 已启用")
	command = serviceModulePopupBounds(t, tester, "快捷命令 · 1 条")
	if tag.Y+tag.H > command.Y || tag.X != command.X || tag.X < 0 || tag.X+tag.W > width || command.X+command.W > width {
		t.Fatalf("the module actions should stack within a narrow window: Tag=%+v commands=%+v", tag, command)
	}
	captureDesktopTestFrame(t, tester, "service-editor-module-tabs-narrow")
}
