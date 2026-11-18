package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigBackupTrashStaging(t *testing.T) {
	trashFailure := errors.New("trash unavailable")
	for _, scenario := range []struct {
		name        string
		moveErr     error
		replacement bool
	}{
		{name: "transport preserves the filename"},
		{name: "native failure restores the ZIP", moveErr: trashFailure},
		{name: "occupied original leaves the ZIP staged", moveErr: trashFailure, replacement: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			root, original, expected := configBackupTrashFixture(t)
			name := filepath.Base(original)
			var stagedPath string
			destination := filepath.Join(t.TempDir(), name)
			err := trashListedConfigBackup(context.Background(), root, name, expected, renameConfigBackupFixture,
				func(_ context.Context, path string) (string, error) {
					stagedPath = path
					if !filepath.IsAbs(path) || path == original || filepath.Base(path) != name || filepath.Dir(filepath.Dir(path)) != root.Name() {
						t.Fatalf("unexpected staged ZIP path: %s", path)
					}
					if info, err := os.Lstat(path); err != nil || !sameConfigBackup(expected, info) {
						t.Fatalf("wrong staged file: %v", err)
					}
					if _, err := os.Lstat(original); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("original location still occupied: %v", err)
					}
					if scenario.replacement {
						if err := os.WriteFile(original, []byte("incoming replacement"), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					if scenario.moveErr != nil {
						return "", scenario.moveErr
					}
					return destination, os.Rename(path, destination)
				})
			if !errors.Is(err, scenario.moveErr) || stagedPath == "" {
				t.Fatalf("trash returned %v, staged path %q", err, stagedPath)
			}
			if scenario.replacement {
				reason := configBackupDeleteReason(&configBackupTrashError{err: err})
				if !strings.Contains(reason, stagedPath) {
					t.Fatalf("retained ZIP location missing from the failure: %s", reason)
				}
				assertConfigBackupTrashContents(t, original, "incoming replacement")
				assertConfigBackupTrashContents(t, stagedPath, "selected ZIP")
				return
			}
			if _, err := os.Lstat(filepath.Dir(stagedPath)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("empty staging directory was retained: %v", err)
			}
			if scenario.moveErr != nil {
				assertConfigBackupTrashContents(t, original, "selected ZIP")
			} else {
				assertConfigBackupTrashContents(t, destination, "selected ZIP")
			}
		})
	}
}

func TestConfigBackupTrashRejectsReplacementDuringStaging(t *testing.T) {
	root, original, expected := configBackupTrashFixture(t)
	name := filepath.Base(original)
	retired := filepath.Join(t.TempDir(), name)
	err := trashListedConfigBackup(context.Background(), root, name, expected,
		func(fromDir *os.File, from string, toDir *os.File, to string) error {
			if filepath.Clean(fromDir.Name()) == root.Name() {
				if err := os.Rename(original, retired); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(original, []byte("selected ZIP"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(original, expected.ModTime(), expected.ModTime()); err != nil {
					t.Fatal(err)
				}
			}
			return renameConfigBackupFixture(fromDir, from, toDir, to)
		}, func(context.Context, string) (string, error) {
			t.Fatal("same metadata replacement reached native trash")
			return "", nil
		})
	if err == nil {
		t.Fatal("changed file identity was accepted")
	}
	assertConfigBackupTrashContents(t, retired, "selected ZIP")
	assertConfigBackupTrashContents(t, original, "selected ZIP")
}

func TestConfigBackupTrashUnconfirmedMoveRestoresFile(t *testing.T) {
	root, original, expected := configBackupTrashFixture(t)
	err := trashListedConfigBackup(context.Background(), root, filepath.Base(original), expected, renameConfigBackupFixture,
		func(context.Context, string) (string, error) { return filepath.Join(t.TempDir(), "not-moved.zip"), nil })
	if err == nil {
		t.Fatal("move with the ZIP still in staging was reported as successful")
	}
	assertConfigBackupTrashContents(t, original, "selected ZIP")
}

func configBackupTrashFixture(t *testing.T) (*os.Root, string, os.FileInfo) {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "silverwing-config-备份 #1.zip")
	if err := os.WriteFile(path, []byte("selected ZIP"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	info, err := root.Lstat(filepath.Base(path))
	if err != nil {
		t.Fatal(err)
	}
	return root, path, info
}

func renameConfigBackupFixture(fromDir *os.File, from string, toDir *os.File, to string) error {
	target := filepath.Join(toDir.Name(), to)
	if _, err := os.Lstat(target); err == nil {
		return os.ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(filepath.Join(fromDir.Name(), from), target)
}

func assertConfigBackupTrashContents(t *testing.T, path, expected string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != expected {
		t.Fatalf("unexpected contents at %s: %q, %v", path, data, err)
	}
}
