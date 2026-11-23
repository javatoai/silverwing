package desktop

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

func assertSelectedServiceEditorModule(t *testing.T, state *serviceEditorState, tester *ui.Tester, wantID string) {
	t.Helper()
	if state.selectedModuleID != wantID {
		t.Fatalf("selected module = %q, want %q", state.selectedModuleID, wantID)
	}
	serviceModulePopupBounds(t, tester, "模块 ID · "+wantID)
	for _, module := range state.modules {
		if _, found := tester.Find("模块 ID · " + module.id); found && module.id != wantID {
			t.Fatalf("details for inactive module %q are still rendered", module.id)
		}
	}
}

func typeServiceEditorModuleName(t *testing.T, tester *ui.Tester, name string) {
	t.Helper()
	caption := serviceModulePopupBounds(t, tester, "模块名")
	tester.ClickAt(caption.X+16, caption.Y+caption.H+16)
	if active, _ := tester.TextInputActive(); !active {
		t.Fatal("module name input did not take focus")
	}
	tester.Key(ui.Cmd, ui.KeyA)
	if name == "" {
		tester.Key(0, ui.KeyBackspace)
	} else {
		tester.Type(name)
	}
}

func TestServiceEditorModuleTabsPreserveDrafts(t *testing.T) {
	s, state, backend, tester := newServiceEditorModulePopupTester(t)
	assertSelectedServiceEditorModule(t, state, tester, "core")
	core := serviceModulePopupBounds(t, tester, "Core")
	client := serviceModulePopupBounds(t, tester, "Client")
	details := serviceModulePopupBounds(t, tester, "模块 ID · core")
	if core.Y != client.Y || core.X+core.W > client.X || client.Y+client.H > details.Y {
		t.Fatalf("module tabs should share a row above the details: core=%+v client=%+v details=%+v", core, client, details)
	}

	clickServiceModulePopupControl(t, tester, "Client")
	assertSelectedServiceEditorModule(t, state, tester, "client")
	if s.serviceEditorDirty() {
		t.Fatal("changing the selected tab marked the configuration dirty")
	}
	clickServiceModulePopupControl(t, tester, "Core")
	typeServiceEditorModuleName(t, tester, "Core.v2")
	serviceModulePopupBounds(t, tester, "Core.v2")
	clickServiceModulePopupControl(t, tester, "独立克隆")
	clickServiceModulePopupSwitch(t, tester, "继承服务主分支")
	typeServiceModulePopupField(t, tester, "默认克隆分支", "origin/feature/core")

	clickServiceModulePopupControl(t, tester, "Client")
	assertSelectedServiceEditorModule(t, state, tester, "client")
	if tester.HasText("模块主分支") || tester.HasText("默认克隆分支") {
		t.Fatal("the branch override control leaked into the second module")
	}
	typeServiceEditorModuleName(t, tester, "Client.v2")
	clickServiceModulePopupControl(t, tester, "基本信息")
	clickServiceModulePopupControl(t, tester, "工作区模块")
	assertSelectedServiceEditorModule(t, state, tester, "client")
	serviceModulePopupBounds(t, tester, "Client.v2")

	clickServiceModulePopupControl(t, tester, "Core.v2")
	assertSelectedServiceEditorModule(t, state, tester, "core")
	serviceModulePopupBounds(t, tester, "默认克隆分支")
	modules := mapsFromData(serviceModulePopupDraft(t, s)["modules"])
	if len(modules) != 2 || mapString(modules[0], "id") != "core" || mapString(modules[0], "name") != "Core.v2" ||
		mapString(modules[0], "strategy") != "INDEPENDENT_CLONE" || mapString(modules[0], "masterBranch") != "origin/feature/core" ||
		mapString(modules[1], "id") != "client" || mapString(modules[1], "name") != "Client.v2" || modules[1]["masterBranch"] != nil {
		t.Fatalf("switching tabs lost a draft or copied it to another module: %#v", modules)
	}
	assertServiceModulePopupHasNotSaved(t, s, state, backend)
}

func TestServiceEditorModuleTabsDistinguishDuplicateDraftNames(t *testing.T) {
	state := newModuleDeleteRegressionState(newServiceModuleDraft("original", "Default", "STANDARD_WORKTREE"))
	s, tester := newServiceEditorModuleDeleteTester(state)
	clickServiceModulePopupControl(t, tester, "添加 Worktree 模块")
	if len(state.modules) != 2 || state.modules[1].strategy != "STANDARD_WORKTREE" {
		t.Fatalf("adding a Worktree module produced unexpected drafts: %#v", state.modules)
	}
	addedID := state.modules[1].id
	assertSelectedServiceEditorModule(t, state, tester, addedID)
	serviceModulePopupBounds(t, tester, "Default · 1")
	serviceModulePopupBounds(t, tester, "default · 2")
	typeServiceEditorModuleName(t, tester, "")
	if got := *s.serviceEditorModuleText(&state.modules[1], "name", state.modules[1].name); got != "" {
		t.Fatalf("clearing the draft name leaves %q in the field", got)
	}
	clickServiceModulePopupControl(t, tester, "Default · 1")
	assertSelectedServiceEditorModule(t, state, tester, "original")
	clickServiceModulePopupControl(t, tester, "default · 2")
	assertSelectedServiceEditorModule(t, state, tester, addedID)
	typeServiceEditorModuleName(t, tester, "Edge.v2")
	serviceModulePopupBounds(t, tester, "Default")
	serviceModulePopupBounds(t, tester, "Edge.v2")
	assertSelectedServiceEditorModule(t, state, tester, addedID)
}

func TestServiceEditorModuleTabsReachOverflowInNarrowWindow(t *testing.T) {
	_, state, _, tester := newServiceEditorModulePopupTester(t)
	for index := range 12 {
		id := fmt.Sprintf("module-%02d", index)
		name := fmt.Sprintf("Workspace-module-%02d", index)
		state.modules = append(state.modules, newServiceModuleDraft(id, name, "STANDARD_WORKTREE"))
	}
	const width, height = 450, 900
	const lastTab = "Workspace-module-11"
	tester.SetSize(width, height)
	if last, found := tester.Find(lastTab); found && last.W > 0 && last.X+last.W <= width {
		t.Fatal("fixture did not overflow the tab row")
	}
	clickServiceModulePopupControl(t, tester, "Core")
	tester.Key(0, ui.KeyRight)
	assertSelectedServiceEditorModule(t, state, tester, "client")
	if !tester.Focused("Client") {
		t.Fatal("Right did not move focus to the next module tab")
	}
	tester.Key(0, ui.KeyEnd)
	assertSelectedServiceEditorModule(t, state, tester, "module-11")
	last := serviceModulePopupBounds(t, tester, lastTab)
	if !tester.Focused(lastTab) || last.X < 0 || last.X+last.W > width {
		t.Fatalf("End did not bring the last module tab into the window: %+v", last)
	}
	tester.Key(0, ui.KeyHome)
	assertSelectedServiceEditorModule(t, state, tester, "core")
	first := serviceModulePopupBounds(t, tester, "Core")
	tester.Scroll(first.X+first.W/2, first.Y+first.H/2, 2400, 0)
	clickServiceModulePopupControl(t, tester, lastTab)
	assertSelectedServiceEditorModule(t, state, tester, "module-11")
}
