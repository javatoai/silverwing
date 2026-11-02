package storage

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
)

func TestConfigImportSnapshotRejectsReplacedArchiveWithoutMutation(t *testing.T) {
	ctx := context.Background()
	source := NewConfigStore(NewPaths(t.TempDir()))
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
	target := NewConfigStore(NewPaths(t.TempDir()))
	if _, err := target.Load(); err != nil {
		t.Fatal(err)
	}
	before, _ := target.readShardFiles()
	backupsBefore, _ := target.Backups()
	preview, fingerprint, err := target.PreviewImportSnapshotContext(ctx, archive)
	if err != nil || preview.Theme != domain.ThemeLight {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	fileFingerprint, err := ConfigurationArchiveFingerprintContext(ctx, archive)
	if err != nil || fingerprint != fileFingerprint {
		t.Fatalf("preview is not bound to the archive bytes: %q / %q %v", fingerprint, fileFingerprint, err)
	}
	config.Theme = domain.ThemeDark
	if err := source.Save(config); err != nil {
		t.Fatal(err)
	}
	if _, err := source.ExportTo(archive); err != nil {
		t.Fatal(err)
	}
	if _, err := target.ImportFromPreview(archive, fingerprint); err == nil || !strings.Contains(err.Error(), "预览后已变化") {
		t.Fatalf("changed archive accepted: %v", err)
	}
	after, _ := target.readShardFiles()
	backupsAfter, _ := target.Backups()
	if !reflect.DeepEqual(before, after) || len(backupsBefore) != len(backupsAfter) {
		t.Fatal("rejected archive changed configuration or created a backup")
	}
	_, latest, err := target.PreviewImportSnapshotContext(ctx, archive)
	if err != nil || latest == fingerprint {
		t.Fatalf("new preview did not observe replacement: %q %v", latest, err)
	}
	imported, err := target.ImportFromPreview(archive, latest)
	if err != nil || imported.Theme != domain.ThemeDark {
		t.Fatalf("freshly confirmed archive did not import: %+v %v", imported, err)
	}
}

func TestConfigImportSnapshotRejectsCanceledAndInvalidSources(t *testing.T) {
	store := NewConfigStore(NewPaths(t.TempDir()))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := store.PreviewImportSnapshotContext(ctx, filepath.Join(t.TempDir(), "missing.zip")); err != context.Canceled {
		t.Fatalf("cancelled preview: %v", err)
	}
	if _, err := store.ImportFromPreview("unused.zip", ""); err == nil {
		t.Fatal("missing preview fingerprint accepted")
	}
	invalid := filepath.Join(t.TempDir(), "invalid.zip")
	if err := os.WriteFile(invalid, []byte("not a ZIP"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, fingerprint, err := store.PreviewImportSnapshotContext(context.Background(), invalid); err == nil || fingerprint != "" {
		t.Fatalf("invalid ZIP received a usable preview: %q %v", fingerprint, err)
	}
}
