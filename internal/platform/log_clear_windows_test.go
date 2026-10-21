//go:build windows

package platform

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestApplicationLogsClearRejectsWindowsJunctions(t *testing.T) {
	s, paths, names := applicationLogsClearFixture(t)
	external := t.TempDir()
	marker := filepath.Join(external, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(paths.Logs, filepath.Join(paths.Home, "logs-original")); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", paths.Logs, external).CombinedOutput(); err != nil {
		t.Fatalf("isolated junction: %s %v", out, err)
	}
	if _, err := s.Execute(context.Background(), "logs.clear", map[string]any{"confirmed": true, "expectedHome": paths.Home}); err == nil {
		t.Fatal("redirected log root accepted")
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "keep" {
		t.Fatal("external junction target changed")
	}
	if data, err := os.ReadFile(filepath.Join(paths.Home, "logs-original", names[0])); err != nil || string(data) != "old log content\n" {
		t.Fatal("old log changed")
	}
}
