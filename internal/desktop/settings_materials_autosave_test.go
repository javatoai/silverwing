package desktop

import (
	"testing"
	"time"

	"silverwing-go/internal/application"
)

func TestMaterialsRootSaveResultPreservesNewerAutosaveDraft(t *testing.T) {
	s := newApp(nil, Options{CapturePath: "isolated-test"})
	s.snapshot.Home = "isolated-home"
	s.settingsUnit.state.configJSON = `{"requirementMaterialsRoot":"old"}`
	draft := s.input("setting:requirementMaterialsRoot", "old")
	s.autosaveSettingsInput("path:materials", draft, func(string) {})
	*draft = "submitted"
	s.autosaveSettingsInput("path:materials", draft, func(string) {})
	entry := s.settingsUnit.state.settingsAutosave.entries["path:materials"]
	entry.pending = false
	s.settingsUnit.state.settingsAutosave.active = &settingsAutosaveAttempt{entry: entry, value: "submitted", kind: "config.materials-root.save", config: s.settingsUnit.state.configJSON}
	// Native typing can happen before the next frame observes the new draft.
	*draft = "newer"
	action := application.Action{Kind: "config.materials-root.save", Path: "submitted"}
	result := application.ActionResult{Path: "normalized-submitted"}
	s.applySettingsActionResult(action, result)
	before := s.settingsUnit.state.configJSON
	s.settingsUnit.state.configJSON = `{"requirementMaterialsRoot":"normalized-submitted"}`
	s.syncSettingsConfigInputs(before, s.settingsUnit.state.configJSON)
	entry.due = time.Now().Add(time.Hour)
	s.finishSettingsAutosaveAction(action, result, nil)
	if *draft != "newer" || entry.value != "newer" || !entry.pending {
		t.Fatalf("older save replaced or dropped the newer draft: input=%q entry=%+v", *draft, entry)
	}
}
