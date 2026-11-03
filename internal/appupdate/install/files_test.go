package install

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/appupdate"
)

func programFixture(t *testing.T, version string) (string, appupdate.Layout, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "program")
	os.MkdirAll(filepath.Join(root, "resources"), 0o700)
	os.WriteFile(filepath.Join(root, "app.exe"), []byte(version), 0o700)
	os.WriteFile(filepath.Join(root, "helper.exe"), []byte("helper"), 0o700)
	identity := appupdate.Identity{Version: version, Executable: "app", Repository: "owner/repo", TagPrefix: "v", Platform: "windows/amd64"}
	files, err := appupdate.Inventory(root, "resources/update-installation.json")
	if err != nil {
		t.Fatal(err)
	}
	layout := appupdate.Layout{Schema: 1, Identity: identity, Executable: "app.exe", Helper: "helper.exe", Files: files}
	marker := filepath.Join(root, "resources/update-installation.json")
	if err := appupdate.WriteJSON(marker, layout); err != nil {
		t.Fatal(err)
	}
	return root, layout, marker
}

func portablePlan(t *testing.T) appupdate.Plan {
	t.Helper()
	oldRoot, old, marker := programFixture(t, "1.0.0")
	newRoot, next, _ := programFixture(t, "1.0.1")
	os.WriteFile(filepath.Join(newRoot, "new.txt"), []byte("new managed file"), 0o600)
	next.Files, _ = appupdate.Inventory(newRoot, "")
	return appupdate.Plan{ID: "0123456789abcdef0123456789abcdef", Installation: appupdate.Installation{Root: oldRoot, LayoutPath: marker, Layout: old, Identity: old.Identity, Kind: appupdate.Portable}, Backup: filepath.Join(t.TempDir(), "backup"), New: appupdate.Bundle{Directory: filepath.Dir(newRoot), Metadata: appupdate.Metadata{Kind: appupdate.Portable, Payload: filepath.Base(newRoot), Identity: next.Identity, Layout: &next}}}
}

func TestPortableUpgradeRollbackPreservesExtraFiles(t *testing.T) {
	p := portablePlan(t)
	extra := filepath.Join(p.Installation.Root, "user-note.md")
	os.WriteFile(extra, []byte("keep this"), 0o600)
	installer := Portable{}
	if err := installer.Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if content, _ := os.ReadFile(filepath.Join(p.Installation.Root, "app.exe")); string(content) != "1.0.1" {
		t.Fatal("new version not installed")
	}
	if err := installer.Rollback(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if content, _ := os.ReadFile(filepath.Join(p.Installation.Root, "app.exe")); string(content) != "1.0.0" {
		t.Fatal("old version not restored")
	}
	if content, _ := os.ReadFile(extra); string(content) != "keep this" {
		t.Fatal("user file changed")
	}
	if _, err := os.Stat(filepath.Join(p.Installation.Root, "new.txt")); !os.IsNotExist(err) {
		t.Fatal("new managed file survived rollback")
	}
}

func TestPortableCollisionAndBadPayloadDoNotTouchInstallation(t *testing.T) {
	p := portablePlan(t)
	os.WriteFile(filepath.Join(p.Installation.Root, "new.txt"), []byte("user content"), 0o600)
	if err := (Portable{}).Apply(context.Background(), p); err == nil {
		t.Fatal("user file collision accepted")
	}
	if content, _ := os.ReadFile(filepath.Join(p.Installation.Root, "new.txt")); string(content) != "user content" {
		t.Fatal("user file overwritten")
	}
	os.Remove(filepath.Join(p.Installation.Root, "new.txt"))
	root, _ := payload(p.New)
	os.WriteFile(filepath.Join(root, "app.exe"), []byte("tampered"), 0o700)
	if err := (Portable{}).Apply(context.Background(), p); err == nil {
		t.Fatal("changed prepared payload accepted")
	}
	if content, _ := os.ReadFile(filepath.Join(p.Installation.Root, "app.exe")); string(content) != "1.0.0" {
		t.Fatal("installed program changed")
	}
}
