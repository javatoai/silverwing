package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/storage"
)

func TestDiagnosticsCanceledExportLeavesNoArchive(t *testing.T) {
	root := t.TempDir()
	paths := storage.NewPaths(filepath.Join(root, "home"))
	service := NewService(paths, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	target := filepath.Join(root, "diagnostics.zip")
	_, err := service.exportDiagnostics(ctx, map[string]any{"path": target})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled export was not rejected: %v", err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled export created an archive: %v", err)
	}
	if _, err := os.Stat(paths.Home); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled export initialized the application home: %v", err)
	}
}
