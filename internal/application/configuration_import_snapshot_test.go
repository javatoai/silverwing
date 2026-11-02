package application

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestConfigurationImportChecksConfirmedPreviewFingerprint(t *testing.T) {
	paths := storage.NewPaths(t.TempDir())
	a := &App{Paths: paths, Config: storage.NewConfigStore(paths)}
	if _, err := a.Config.Load(); err != nil {
		t.Fatal(err)
	}
	source := storage.NewConfigStore(storage.NewPaths(t.TempDir()))
	config, err := source.Load()
	if err != nil {
		t.Fatal(err)
	}
	config.Theme = domain.ThemeLight
	if err := source.Save(config); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "import.zip")
	if _, err := source.ExportTo(archive); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	result, err := a.Dispatch(ctx, Action{Kind: "config.import-preview", Path: archive})
	if err != nil {
		t.Fatal(err)
	}
	preview := result.Data.(ConfigImportPreview)
	if preview.Fingerprint == "" {
		t.Fatal("preview did not return its archive fingerprint")
	}
	config.Theme = domain.ThemeDark
	if err := source.Save(config); err != nil {
		t.Fatal(err)
	}
	if _, err := source.ExportTo(archive); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Dispatch(ctx, Action{Kind: "config.import", Path: preview.Path, Payload: map[string]any{"expectedFingerprint": preview.Fingerprint}}); err == nil || !strings.Contains(err.Error(), "预览后已变化") {
		t.Fatalf("Dispatch imported an archive that differs from its preview: %v", err)
	}
	current, err := a.Config.Load()
	if err != nil || current.Theme == domain.ThemeDark {
		t.Fatalf("rejected import changed current configuration: %+v %v", current, err)
	}
	result, err = a.Dispatch(ctx, Action{Kind: "config.import-preview", Path: archive})
	if err != nil {
		t.Fatal(err)
	}
	latest := result.Data.(ConfigImportPreview)
	if _, err := a.Dispatch(ctx, Action{Kind: "config.import", Path: latest.Path, Payload: map[string]any{"expectedFingerprint": latest.Fingerprint}}); err != nil {
		t.Fatal("fresh preview import failed", err)
	}
}
