package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreSaveCreatesAndReplacesAccount(t *testing.T) {
	store := Store{Directory: filepath.Join(t.TempDir(), "本地账号 fixture")}
	first := Credentials{Username: "  fixture  ", Password: "synthetic-first"}
	saved, err := store.Save(first)
	if err != nil || saved.Username != "fixture" || saved.Password != first.Password {
		t.Fatal("initial account save failed", err)
	}
	file, err := os.Open(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	// File.Stat eagerly captures the identity on Windows. Path Stat can
	// resolve identity lazily during SameFile, by which time it has changed.
	before, err := file.Stat()
	closeErr := file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	assertPrivateCredentialFile(t, store.Path())

	if err := writePrivateJSON(store.Path(), make(chan int)); err == nil {
		t.Fatal("invalid content encoded successfully")
	}
	if account, err := store.Load(); err != nil || account != saved {
		t.Fatal("failed write changed the saved account", err)
	}

	next := Credentials{Username: "fixture", Password: "synthetic-replacement"}
	if _, err := store.Save(next); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) {
		t.Fatal("replacement wrote into the existing file")
	}
	if account, err := store.Load(); err != nil || account != next {
		t.Fatal("replacement account could not be read", err)
	}
	assertPrivateCredentialFile(t, store.Path())
	entries, err := os.ReadDir(store.Directory)
	if err != nil || len(entries) != 1 {
		t.Fatal("save left temporary credential files", err)
	}
}
