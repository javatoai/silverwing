package appupdate_test

import (
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/appupdate"
)

func TestUpdatePathsRejectTraversalAndSpecialFiles(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"../escape", "/absolute", `C:\outside`, "a/../../escape", ".", ""} {
		if _, err := appupdate.Resolve(root, path); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "redirect")); err == nil {
		if _, err := appupdate.Resolve(root, "redirect/file"); err == nil {
			t.Fatal("symlink traversal accepted")
		}
	}
}
