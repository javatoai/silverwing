package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateTaskRootRejectsSymlinkOverlap(t *testing.T) {
	assertTaskRootAliasBoundary(t, func(link, target string) {
		t.Helper()
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("directory symlinks unavailable: %v", err)
		}
	})
}

func assertTaskRootAliasBoundary(t *testing.T, linkDirectory func(string, string)) {
	t.Helper()
	base := t.TempDir()
	home := filepath.Join(base, "conf")
	internal := filepath.Join(home, "internal")
	external := filepath.Join(base, "external")
	for _, dir := range []string{internal, external} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	insideAlias := filepath.Join(base, "inside-alias")
	parentAlias := filepath.Join(base, "parent-alias")
	outsideAlias := filepath.Join(base, "outside-alias")
	linkDirectory(insideAlias, internal)
	linkDirectory(parentAlias, base)
	linkDirectory(outsideAlias, external)
	for _, root := range []string{insideAlias, filepath.Join(insideAlias, "missing"), parentAlias} {
		if _, err := ValidateTaskRootPath(home, root); err == nil {
			t.Fatalf("accepted overlap through a directory alias: %s", root)
		}
	}
	if root, err := ValidateTaskRootPath(home, outsideAlias); err != nil || root != outsideAlias {
		t.Fatalf("rejected an external alias or lost its configured spelling: %q, %v", root, err)
	}
	if _, err := ValidateTaskRootPath(insideAlias, home); err == nil {
		t.Fatal("accepted a task root above an aliased conf-dir")
	}
	paths := NewPaths(home)
	paths.ProfileID = "profile"
	store := NewConfigStore(paths)
	config, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	config.TaskRoot = &insideAlias
	if err := store.Save(config); err == nil {
		t.Fatal("persisted a root overlapping conf-dir through an alias")
	}
	loaded, err := store.Load()
	if err != nil || loaded.TaskRoot != nil {
		t.Fatalf("failed save changed the configuration: %+v, %v", loaded.TaskRoot, err)
	}
}
