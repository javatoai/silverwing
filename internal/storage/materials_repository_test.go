package storage

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMaterialsRepositoryLockOutsideRootAndAcrossProcesses(t *testing.T) {
	root := t.TempDir()
	path, err := MaterialsRepositoryLockPath(root)
	if err != nil {
		t.Fatal(err)
	}
	if rel, err := filepath.Rel(root, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
		t.Fatal("runtime lock is inside repository", path)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	alias, err := MaterialsRepositoryLockPath(filepath.Join(root, "missing", ".."))
	if err != nil || alias != path {
		t.Fatalf("canonical key mismatch: %s / %s: %v", path, alias, err)
	}
	release, err := AcquireMaterialsRepositoryLock(root)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := AcquireMaterialsRepositoryLock(root); !errors.Is(err, ErrLockBusy) {
		t.Fatal("same-process lock not shared", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMaterialsRepositoryLockChild$")
	child.Env = append(os.Environ(), "SILVERWING_MATERIALS_LOCK_TEST_ROOT="+root)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("cross-process lock: %s: %v", output, err)
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatal("lock created a repository file", entries, err)
	}
}

func TestMaterialsRepositoryLockChild(t *testing.T) {
	root := os.Getenv("SILVERWING_MATERIALS_LOCK_TEST_ROOT")
	if root == "" {
		return
	}
	if release, err := AcquireMaterialsRepositoryLock(root); !errors.Is(err, ErrLockBusy) {
		if release != nil {
			release()
		}
		t.Fatal("another process acquired the held root", err)
	}
}

func TestMaterialsRepositoryRetiresOnlyUnusedEmptyLegacyLock(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, legacyMaterialsLock)
	release, err := AcquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if next, err := AcquireMaterialsRepositoryLock(root); !errors.Is(err, ErrLockBusy) {
		if next != nil {
			next()
		}
		t.Fatal("active legacy operation was not protected", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("active legacy lock removed", err)
	}
	release()
	next, err := AcquireMaterialsRepositoryLock(root)
	if err != nil {
		t.Fatal(err)
	}
	next()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("inactive generated lock retained", err)
	}
	user := []byte("user-owned content at historical filename\r\n")
	if err := os.WriteFile(path, user, 0600); err != nil {
		t.Fatal(err)
	}
	next, err = AcquireMaterialsRepositoryLock(root)
	if err != nil {
		t.Fatal(err)
	}
	next()
	if data, err := os.ReadFile(path); err != nil || string(data) != string(user) {
		t.Fatal("unknown legacy-name content removed", err)
	}
}

func TestMaterialsIgnoreMigrationRecognizesBOMAtFirstLine(t *testing.T) {
	bom := "\xef\xbb\xbf"
	temporary := "# Silverwing local temporary files\r\n*\r\n"
	exclude := "# Silverwing local runtime and temporary files\r\n.silverwing-*\r\n/[0-9][0-9][0-9][0-9]/*/[tT][eE][mM][pP]/\r\n"
	for _, test := range []struct {
		name, before, want string
		migrate            func(string) error
	}{
		{"temporary-only", bom + temporary, bom, MigrateMaterialsTemporaryIgnore},
		{"temporary-user-rules", bom + temporary + "# user\r\n*.scratch\r\n*\r\n", bom + "# user\r\n*.scratch\r\n*\r\n", MigrateMaterialsTemporaryIgnore},
		{"temporary-user-prefix", bom + "# user\r\n*\r\n" + temporary, bom + "# user\r\n*\r\n", MigrateMaterialsTemporaryIgnore},
		{"exclude-user-rules", bom + exclude + "# user\r\n.silverwing-*\r\n", bom + "# user\r\n.silverwing-*\r\n", MigrateMaterialsGitExclude},
		{"unmarked-user-file", bom + "*.log\n", bom + "*.log\n", MigrateMaterialsTemporaryIgnore},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".gitignore")
			if err := os.WriteFile(path, []byte(test.before), 0600); err != nil {
				t.Fatal(err)
			}
			if err := test.migrate(path); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != test.want {
				t.Fatalf("BOM/default-block migration: got %q want %q err=%v", got, test.want, err)
			}
			if err := test.migrate(path); err != nil {
				t.Fatal(err)
			}
			if next, err := os.ReadFile(path); err != nil || string(next) != test.want {
				t.Fatal("BOM migration was not idempotent", err)
			}
		})
	}
}

func TestMaterialsIgnoreMigrationPreservesUserBytesAndIdenticalRules(t *testing.T) {
	const temp = "# Silverwing local temporary files\n*\n"
	const exclude = "# Silverwing local runtime and temporary files\n.silverwing-*\n/[0-9][0-9][0-9][0-9]/*/[tT][eE][mM][pP]/\n"
	for _, test := range []struct {
		name, before, after string
		exclude, removed    bool
	}{
		{"temp-generated", temp, "", false, true},
		{"temp-user-identical", "# user\r\n*\r\n# Silverwing local temporary files\r\n*\r\n!keep\r\n", "# user\r\n*\r\n!keep\r\n", false, false},
		{"temp-user-duplicate-after", temp + "*\n", "*\n", false, false},
		{"temp-unmarked", "*\n", "*\n", false, false},
		{"temp-marker-custom", "# Silverwing local temporary files\n*.custom\n", "# Silverwing local temporary files\n*.custom\n", false, false},
		{"exclude-generated", "\n" + exclude, "", true, true},
		{"exclude-user-identical", ".silverwing-*\r\n\n" + exclude + "!user\n", ".silverwing-*\r\n\n!user\n", true, false},
		{"exclude-partial-generated", ".silverwing-*\n# Silverwing local runtime and temporary files\n/[0-9][0-9][0-9][0-9]/*/[tT][eE][mM][pP]/\n", ".silverwing-*\n", true, false},
		{"exclude-unmarked", ".silverwing-*\n/[0-9][0-9][0-9][0-9]/*/[tT][eE][mM][pP]/\n", ".silverwing-*\n/[0-9][0-9][0-9][0-9]/*/[tT][eE][mM][pP]/\n", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".gitignore")
			if err := os.WriteFile(path, []byte(test.before), 0600); err != nil {
				t.Fatal(err)
			}
			migrate := MigrateMaterialsTemporaryIgnore
			if test.exclude {
				migrate = MigrateMaterialsGitExclude
			}
			if err := migrate(path); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if test.removed {
				if !os.IsNotExist(err) {
					t.Fatal("wholly generated empty file not removed", err)
				}
			} else if err != nil || string(data) != test.after {
				t.Fatalf("user bytes changed: %q want %q: %v", data, test.after, err)
			}
			if err := migrate(path); err != nil {
				t.Fatal("migration is not repeatable", err)
			}
		})
	}
}
