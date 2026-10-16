//go:build windows

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func deleteGuardJunction(t *testing.T, path, target string) {
	t.Helper()
	if !filepath.IsAbs(path) || !filepath.IsAbs(target) {
		t.Fatal("junction fixture paths must be absolute")
	}
	if output, err := exec.Command("cmd", "/c", "mklink", "/J", path, target).CombinedOutput(); err != nil {
		t.Fatalf("create isolated junction: %s %v", output, err)
	}
	t.Cleanup(func() {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Error("unlink isolated junction", err)
		}
	})
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	attributes, _ := info.Sys().(*syscall.Win32FileAttributeData)
	t.Logf("fixture junction Mode=%s IsDir=%t Sys=%+v", info.Mode(), info.IsDir(), attributes)
}

func TestControlledSkillDeleteRejectsWindowsRedirectedAncestor(t *testing.T) {
	for _, remover := range []struct {
		name string
		run  func(string, string) error
	}{{"cache", removeWithin}, {"single-skill", removeSkillExact}} {
		t.Run(remover.name, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			linked := filepath.Join(root, "redirected-cache")
			victim := filepath.Join(outside, "skills", "synthetic-skill")
			if err := os.MkdirAll(victim, 0o700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(victim, "outside-marker.txt")
			if err := os.WriteFile(marker, []byte("outside fixture must survive\r\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			deleteGuardJunction(t, linked, outside)
			target := filepath.Join(linked, "skills", "synthetic-skill")
			controlledRoot := root
			if remover.name == "single-skill" {
				controlledRoot = filepath.Join(linked, "skills")
			}
			before := fmt.Sprint(ensureNoExistingSymlinks(target))
			err := remover.run(controlledRoot, target)
			if err == nil {
				t.Fatal("redirected ancestor reached recursive deletion")
			}
			if data, readErr := os.ReadFile(marker); readErr != nil || string(data) != "outside fixture must survive\r\n" {
				t.Fatalf("outside fixture marker changed: %q %v", data, readErr)
			}
			if after := fmt.Sprint(ensureNoExistingSymlinks(target)); after != before {
				t.Fatalf("global ancestor guard behavior changed: before=%q after=%q", before, after)
			}
			t.Logf("controlled deletion rejected: %v; original global guard=%s; outside marker retained", err, before)
		})
	}
}

func TestControlledSkillDeleteWindowsLeafJunctionOnlyUnlinks(t *testing.T) {
	for _, remover := range []struct {
		name string
		run  func(string, string) error
	}{{"cache", removeWithin}, {"single-skill", removeSkillExact}} {
		t.Run(remover.name, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			marker := filepath.Join(outside, "outside-marker.txt")
			if err := os.WriteFile(marker, []byte("leaf target intact"), 0o600); err != nil {
				t.Fatal(err)
			}
			leaf := filepath.Join(root, "synthetic-skill")
			deleteGuardJunction(t, leaf, outside)
			if err := remover.run(root, leaf); err != nil {
				t.Fatal("safe leaf unlink was blocked", err)
			}
			if _, err := os.Lstat(leaf); !os.IsNotExist(err) {
				t.Fatal("leaf junction still exists", err)
			}
			if data, err := os.ReadFile(marker); err != nil || string(data) != "leaf target intact" {
				t.Fatalf("leaf unlink modified its outside target: %q %v", data, err)
			}
		})
	}
}

func TestControlledSkillDeleteWindowsRedirectTagClassification(t *testing.T) {
	for _, tc := range []struct {
		name       string
		attributes uint32
		tag        uint32
		want       bool
	}{
		{"ordinary", syscall.FILE_ATTRIBUTE_DIRECTORY, 0, false},
		{"junction", syscall.FILE_ATTRIBUTE_REPARSE_POINT, 0xa0000003, true},
		{"symlink", syscall.FILE_ATTRIBUTE_REPARSE_POINT, 0xa000000c, true},
		{"non-redirect cloud tag", syscall.FILE_ATTRIBUTE_REPARSE_POINT, 0x9000001a, false},
		{"non-redirect third-party tag", syscall.FILE_ATTRIBUTE_REPARSE_POINT, 0x00000042, false},
		{"no reparse attribute", syscall.FILE_ATTRIBUTE_DIRECTORY, 0xa0000003, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := deleteRedirectTag(tc.attributes, tc.tag); got != tc.want {
				t.Fatalf("redirect classification=%t, want %t", got, tc.want)
			}
		})
	}
}
