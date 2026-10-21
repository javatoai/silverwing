//go:build aix || dragonfly || freebsd || illumos || netbsd || openbsd || solaris

package documents

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUnsupportedDirectoryRenamePreservesSourceAndTarget(t *testing.T) {
	for _, existing := range []bool{false, true} {
		root := t.TempDir()
		source, destination := filepath.Join(root, "source"), filepath.Join(root, "target")
		if err := os.Mkdir(source, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, "payload"), []byte("source bytes"), 0600); err != nil {
			t.Fatal(err)
		}
		if existing {
			if err := os.Mkdir(destination, 0700); err != nil {
				t.Fatal(err)
			}
		}
		err := renameNoReplace(source, destination)
		var linkError *os.LinkError
		if !errors.Is(err, errors.ErrUnsupported) || !errors.As(err, &linkError) || linkError.Old != source || linkError.New != destination {
			t.Fatalf("unsupported error lost its identity: %v", err)
		}
		if data, err := os.ReadFile(filepath.Join(source, "payload")); err != nil || string(data) != "source bytes" {
			t.Fatal("unsupported rename changed the source")
		}
		if existing {
			if entries, err := os.ReadDir(destination); err != nil || len(entries) != 0 {
				t.Fatal("unsupported rename changed the target")
			}
		} else if _, err := os.Lstat(destination); !os.IsNotExist(err) {
			t.Fatal("unsupported rename created a target")
		}
	}
}
