package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestControlledSkillDeleteOrdinaryAndScope(t *testing.T) {
	for _, remover := range []struct {
		name string
		run  func(string, string) error
	}{{"cache", removeWithin}, {"single-skill", removeSkillExact}} {
		t.Run(remover.name, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "synthetic-skill")
			if err := os.MkdirAll(filepath.Join(target, "nested"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(target, "nested", "example.txt"), []byte("synthetic"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := remover.run(root, target); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(target); !os.IsNotExist(err) {
				t.Fatal("ordinary controlled directory was not removed", err)
			}
			if err := remover.run(root, target); err != nil {
				t.Fatal("missing deletion is no longer idempotent", err)
			}
			outside := t.TempDir()
			if err := remover.run(root, outside); err == nil {
				t.Fatal("out-of-scope path accepted")
			}
			if err := remover.run(root, root); err == nil {
				t.Fatal("controlled root itself accepted for deletion")
			}
		})
	}
}
