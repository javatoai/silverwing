package documents

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func supportsExclusiveDirectoryRename() bool {
	switch runtime.GOOS {
	case "windows", "darwin", "ios", "linux", "android":
		return true
	default:
		return false
	}
}

func directoryMoveFixture(t *testing.T, name string) (string, string) {
	t.Helper()
	root := t.TempDir()
	source, destination := filepath.Join(root, name), filepath.Join(root, name+"-moved")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "payload.txt"), []byte("synthetic original\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return source, destination
}

func assertDirectoryPayload(t *testing.T, directory string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(directory, "payload.txt"))
	if err != nil || string(data) != "synthetic original\n" {
		t.Fatalf("directory payload was changed or lost: %q %v", data, err)
	}
}

func TestRenameNoReplaceDirectorySuccessAndUnicode(t *testing.T) {
	for _, name := range []string{"synthetic", "目录-中文"} {
		t.Run(name, func(t *testing.T) {
			source, destination := directoryMoveFixture(t, name)
			err := renameNoReplace(source, destination)
			if !supportsExclusiveDirectoryRename() {
				if !errors.Is(err, errors.ErrUnsupported) {
					t.Fatalf("unsupported platform did not fail explicitly: %v", err)
				}
				assertDirectoryPayload(t, source)
				if _, err := os.Lstat(destination); !os.IsNotExist(err) {
					t.Fatal("unsupported rename created a destination")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			assertDirectoryPayload(t, destination)
			if _, err := os.Lstat(source); !os.IsNotExist(err) {
				t.Fatal("successful rename retained source")
			}
		})
	}
}

func TestRenameNoReplaceDirectoryPreservesExistingTarget(t *testing.T) {
	for _, kind := range []string{"empty-directory", "file"} {
		t.Run(kind, func(t *testing.T) {
			source, destination := directoryMoveFixture(t, "synthetic")
			var err error
			if kind == "empty-directory" {
				err = os.Mkdir(destination, 0700)
			} else {
				err = os.WriteFile(destination, []byte("existing target"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			err = renameNoReplace(source, destination)
			if err == nil {
				t.Fatal("rename replaced the existing target")
			}
			if supportsExclusiveDirectoryRename() && !errors.Is(err, os.ErrExist) {
				t.Fatalf("existing-target error identity was lost: %v", err)
			}
			if runtime.GOOS != "windows" {
				var linkError *os.LinkError
				if !errors.As(err, &linkError) || linkError.Old != source || linkError.New != destination {
					t.Fatalf("rename error lost source/destination: %v", err)
				}
			}
			assertDirectoryPayload(t, source)
			if kind == "empty-directory" {
				entries, err := os.ReadDir(destination)
				if err != nil || len(entries) != 0 {
					t.Fatal("existing empty target was changed")
				}
			} else if data, err := os.ReadFile(destination); err != nil || string(data) != "existing target" {
				t.Fatal("existing target file was changed")
			}
		})
	}
}

func TestRenameNoReplaceDirectoryConcurrentClaim(t *testing.T) {
	if !supportsExclusiveDirectoryRename() {
		t.Skip("platform has no exclusive directory rename API")
	}
	for range 48 {
		source, destination := directoryMoveFixture(t, "synthetic")
		start := make(chan struct{})
		claimed, moved := make(chan error, 1), make(chan error, 1)
		go func() { <-start; claimed <- os.Mkdir(destination, 0700) }()
		go func() { <-start; moved <- renameNoReplace(source, destination) }()
		close(start)
		claimErr, moveErr := <-claimed, <-moved
		switch {
		case claimErr == nil:
			if moveErr == nil {
				t.Fatal("rename overwrote a concurrently created directory")
			}
			assertDirectoryPayload(t, source)
			entries, err := os.ReadDir(destination)
			if err != nil || len(entries) != 0 {
				t.Fatal("concurrent target was replaced")
			}
		case moveErr == nil:
			if !errors.Is(claimErr, os.ErrExist) {
				t.Fatalf("unexpected concurrent mkdir failure: %v", claimErr)
			}
			assertDirectoryPayload(t, destination)
		default:
			t.Fatalf("neither exclusive operation succeeded: %v / %v", claimErr, moveErr)
		}
	}
}

func TestRenameNoReplaceRegularFilePreservesNoClobber(t *testing.T) {
	root := t.TempDir()
	source, destination := filepath.Join(root, "source.txt"), filepath.Join(root, "target.txt")
	if err := os.WriteFile(source, []byte("source bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("target bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := renameNoReplace(source, destination); err == nil {
		t.Fatal("file rename overwrote target")
	}
	for path, want := range map[string]string{source: "source bytes", destination: "target bytes"} {
		if data, err := os.ReadFile(path); err != nil || string(data) != want {
			t.Fatal("failed rename changed a file")
		}
	}
	if err := os.Remove(destination); err != nil {
		t.Fatal(err)
	}
	if err := renameNoReplace(source, destination); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(destination); err != nil || string(data) != "source bytes" {
		t.Fatal("file rename changed source content")
	}
}
