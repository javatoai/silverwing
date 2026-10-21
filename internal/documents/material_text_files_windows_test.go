//go:build windows

package documents

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestMaterialTextWindowsJunctionRejectsSnapshotAndSave(t *testing.T) {
	root, target := t.TempDir(), t.TempDir()
	original := []byte("\xef\xbb\xbfoutside must stay unchanged\r\n")
	outside := filepath.Join(target, "outside.txt")
	if err := os.WriteFile(outside, original, 0600); err != nil {
		t.Fatal(err)
	}
	junction := filepath.Join(root, "junction")
	command := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", junction, target)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	if output, err := command.CombinedOutput(); err != nil {
		t.Skipf("junction creation unavailable: %v: %s", err, output)
	}
	path := filepath.Join(junction, "outside.txt")
	service := NewService(filepath.Join(root, "cache"))
	if _, err := service.MaterialTextSnapshot(root, path); err == nil {
		t.Fatal("junction entered editable snapshot")
	}
	if _, err := service.SaveMaterialText(context.Background(), root, path, "overwrite", Hash(original), true); err == nil {
		t.Fatal("junction target saved")
	}
	if _, err := service.ReadMaterialsPreview(root, path); err == nil {
		t.Fatal("junction entered material preview")
	}
	identity, err := NewMarkdownDraftIdentity("task", root, root, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.WriteMaterialTextDraft(context.Background(), MaterialTextDraft{Identity: identity, Content: "draft", BaselineHash: Hash(original)}); err == nil {
		t.Fatal("junction entered draft journal")
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != string(original) {
		t.Fatalf("outside target changed: %q %v", data, err)
	}
}
