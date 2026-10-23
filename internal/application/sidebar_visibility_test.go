package application

import (
	"encoding/json"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestSidebarVisibilityLeafUpdatePreservesOtherSettings(t *testing.T) {
	store := storage.NewConfigStore(storage.NewPaths(t.TempDir()))
	config := domain.DefaultAppConfig()
	config.SidebarVisibility.Services = false
	config.Theme = domain.ThemeDark
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	app := &App{Config: store}
	_, err := app.saveConfigurationFields(Action{Payload: map[string]any{"update": ConfigFieldsUpdate{ObjectFields: map[string]map[string]json.RawMessage{
		"sidebarVisibility": {"requirements": json.RawMessage(`false`)},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil || got.SidebarVisibility.Requirements || got.SidebarVisibility.Services || !got.SidebarVisibility.Tools || !got.SidebarVisibility.Production || got.Theme != domain.ThemeDark || got.ToolVisibility != config.ToolVisibility {
		t.Fatalf("leaf update replaced unrelated preferences: %+v %v", got.SidebarVisibility, err)
	}
	for _, changes := range []map[string]json.RawMessage{
		{"tasks": json.RawMessage(`false`)},
		{"settings": json.RawMessage(`false`)},
		{"tools": json.RawMessage(`"false"`)},
	} {
		if _, err := mergeConfigurationFields(got, ConfigFieldsUpdate{ObjectFields: map[string]map[string]json.RawMessage{"sidebarVisibility": changes}}); err == nil {
			t.Fatalf("invalid navigation settings accepted: %v", changes)
		}
	}
	if _, err := mergeConfigurationFields(got, ConfigFieldsUpdate{Fields: map[string]json.RawMessage{"sidebarVisibility": json.RawMessage(`{}`)}}); err == nil {
		t.Fatal("whole-object update could overwrite concurrent switch changes")
	}
}
