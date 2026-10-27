//go:build windows

package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterialsCatalogWindowsExclusiveWriteProtection(t *testing.T) {
	path := filepath.Join(t.TempDir(), AgentRouterFileName)
	before, next := []byte("old managed block\nUSER NOTE\n"), []byte("new managed block\nUSER NOTE\n")
	if err := os.WriteFile(path, before, 0644); err != nil {
		t.Fatal(err)
	}
	writer, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeMaterialsCatalogFile(path, before, next, false, nil); err == nil {
		t.Fatal("an already-open external writer was ignored")
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	checks := 0
	err = writeMaterialsCatalogFile(path, before, next, false, func() error {
		checks++
		external, openErr := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0644)
		if openErr == nil {
			_ = external.Close()
			t.Fatal("external editor obtained write access inside the protected update")
		}
		if renameErr := os.Rename(path, path+".moved"); renameErr == nil {
			t.Fatal("external editor replaced the document during the protected update")
		}
		return nil
	})
	if err != nil || checks == 0 {
		t.Fatal("retry after closing the external writer failed", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(next) || !strings.Contains(string(data), "USER NOTE") {
		t.Fatal("protected update lost original user text", string(data), err)
	}
}
