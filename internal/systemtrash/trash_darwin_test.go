//go:build darwin && !ios

package systemtrash

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDarwinTrashRoundTrip(t *testing.T) {
	if os.Getenv("SILVERWING_TEST_MACOS_TRASH") != "1" {
		t.Skip("set SILVERWING_TEST_MACOS_TRASH=1 to trash and restore temporary fixtures")
	}
	for _, directory := range []bool{false, true} {
		kind := "file"
		if directory {
			kind = "directory"
		}
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, filepath.Base(filepath.Dir(root))+"-测试 # ' \" % ?\n.zip")
			marker := source
			if directory {
				if err := os.Mkdir(source, 0o700); err != nil {
					t.Fatal(err)
				}
				marker = filepath.Join(source, "marker.txt")
			}
			if err := os.WriteFile(marker, []byte(root), 0o600); err != nil {
				t.Fatal(err)
			}
			result, err := Move(context.Background(), source)
			if err != nil {
				t.Fatalf("move temporary fixture to Trash: %v", err)
			}
			if _, err := os.Lstat(source); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("temporary fixture still visible at its original path: %v", err)
			}
			trashedMarker := result
			if directory {
				trashedMarker = filepath.Join(result, "marker.txt")
			}
			if data, err := os.ReadFile(trashedMarker); err != nil || string(data) != root {
				t.Fatalf("cannot verify the temporary fixture at %q: %v", result, err)
			}
			if err := unix.RenamexNp(result, source, unix.RENAME_EXCL); err != nil {
				t.Fatalf("restore temporary fixture from %q: %v", result, err)
			}
			if data, err := os.ReadFile(marker); err != nil || string(data) != root {
				t.Fatalf("restored fixture differs: %v", err)
			}
		})
	}
}
