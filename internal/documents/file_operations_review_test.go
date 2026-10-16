package documents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Interrupt after the source has moved into staging, simulating another writer
// claiming the original path before a case-only rename can finish.
type renameRaceContext struct {
	context.Context
	onCheck func()
}

func (c renameRaceContext) Err() error {
	c.onCheck()
	return nil
}

func TestRenameCaseOnlyPreservesFilesWhenDestinationIsClaimed(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "sample.md")
	destination := filepath.Join(root, "SAMPLE.md")
	if err := os.WriteFile(source, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(source)
	if err != nil {
		t.Fatal(err)
	}
	ctx := renameRaceContext{Context: context.Background(), onCheck: func() {
		if err := os.WriteFile(destination, []byte("concurrent writer"), 0o600); err != nil {
			t.Fatal(err)
		}
	}}
	err = renameCaseOnly(ctx, root, root, source, destination, fileIdentity(source, info))
	if err == nil {
		t.Fatal("case-only rename reported success after its destination was claimed")
	}
	data, readErr := os.ReadFile(destination)
	if readErr != nil || string(data) != "concurrent writer" {
		t.Fatalf("rollback overwrote the competing file: %q %v", data, readErr)
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		t.Fatal(readErr)
	}
	foundOriginal := false
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		data, readErr := os.ReadFile(path)
		if readErr == nil && string(data) == "original" {
			foundOriginal = true
			if !strings.EqualFold(entry.Name(), "sample.md") && !strings.Contains(err.Error(), path) {
				t.Fatalf("error did not identify the recoverable staged file: %v", err)
			}
		}
	}
	if !foundOriginal {
		t.Fatal("original file was lost during rollback")
	}
}
