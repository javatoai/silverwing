package desktop

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"silverwing-go/internal/storage"
)

func TestTaskNavigationStoreRestartAndOrdering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task-navigation.json")
	store := newTaskNavigationStore(path)
	empty, err := store.Load()
	if err != nil || len(empty.Pinned) != 0 || len(empty.Recent) != 0 {
		t.Fatalf("missing file: %+v, %v", empty, err)
	}
	for _, key := range []string{"first", "second", "first"} {
		if _, err := store.SetPinned(key, true); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Visit(key); err != nil {
			t.Fatal(err)
		}
	}
	want := taskNavigationPreferences{Pinned: []string{"first", "second"}, Recent: []string{"first", "second"}}
	got, err := newTaskNavigationStore(path).Load()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("restart: got %+v, %v; want %+v", got, err, want)
	}
	got, err = store.SetPinned("first", false)
	want.Pinned = []string{"second"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("unpin changed recent visits or ordering: %+v, %v", got, err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(content, &wire); err != nil || wire["version"] != float64(1) {
		t.Fatalf("missing supported wire version: %v, %v", wire, err)
	}
}

func TestTaskNavigationStoreRecentLimitAndOpaqueKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task-navigation.json")
	store := newTaskNavigationStore(path)
	for i := 0; i < 25; i++ {
		if _, err := store.Visit(fmt.Sprintf("task-%02d", i)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.Visit("task-15")
	if err != nil || len(got.Recent) != 20 || got.Recent[0] != "task-15" || got.Recent[1] != "task-24" || got.Recent[19] != "task-05" {
		t.Fatalf("recent limit or move-to-front: %+v, %v", got, err)
	}
	seen := make(map[string]bool)
	for _, key := range got.Recent {
		if seen[key] {
			t.Fatalf("duplicate recent key: %q", key)
		}
		seen[key] = true
	}
	key := "  Task-ID:任務/CreatedAt  "
	if _, err := store.SetPinned(key, true); err != nil {
		t.Fatal(err)
	}
	restarted, err := newTaskNavigationStore(path).Load()
	if err != nil || !reflect.DeepEqual(restarted.Pinned, []string{key}) || !reflect.DeepEqual(restarted.Recent, got.Recent) {
		t.Fatalf("opaque key or recent state changed on restart: %+v, %v", restarted, err)
	}
}

func TestTaskNavigationStoreReadsLatestForEachMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task-navigation.json")
	first, second := newTaskNavigationStore(path), newTaskNavigationStore(path)
	if _, err := second.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := first.SetPinned("a", true); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Visit("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := second.SetPinned("b", true); err != nil {
		t.Fatal(err)
	}
	if _, err := first.SetPinned("a", true); err != nil {
		t.Fatal(err)
	}
	got, err := second.Visit("b")
	want := taskNavigationPreferences{Pinned: []string{"a", "b"}, Recent: []string{"b", "a"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("independent stores overwrote another mutation: %+v, %v", got, err)
	}
}

func TestTaskNavigationStoreNormalizesLoadedDuplicates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task-navigation.json")
	recent := []string{"task-00", "task-00"}
	for i := 1; i <= 21; i++ {
		recent = append(recent, fmt.Sprintf("task-%02d", i))
	}
	content, err := json.Marshal(map[string]any{"version": 1, "pinned": []string{"a", "b", "a"}, "recent": recent})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := newTaskNavigationStore(path).Load()
	if err != nil || !reflect.DeepEqual(got.Pinned, []string{"a", "b"}) || len(got.Recent) != 20 || got.Recent[0] != "task-00" || got.Recent[19] != "task-19" {
		t.Fatalf("loaded duplicate normalization: %+v, %v", got, err)
	}
	unchanged, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(unchanged, content) {
		t.Fatal("read-only Load rewrote the file")
	}
}

func TestTaskNavigationStoreLockBusyPreservesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task-navigation.json")
	store := newTaskNavigationStore(path)
	if _, err := store.SetPinned("keep", true); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	release, err := storage.AcquireLock(path + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := store.Load(); err != nil {
		t.Fatalf("atomic snapshot read unexpectedly required the write lock: %v", err)
	}
	for _, change := range []func() (taskNavigationPreferences, error){
		func() (taskNavigationPreferences, error) { return store.SetPinned("keep", false) },
		func() (taskNavigationPreferences, error) { return store.Visit("new") },
	} {
		if _, err := change(); !errors.Is(err, storage.ErrLockBusy) {
			t.Fatalf("write did not report lock ownership: %v", err)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("busy writer changed the navigation file")
	}
	release()
	if _, err := store.Visit("after-release"); err != nil {
		t.Fatalf("write did not recover after lock release: %v", err)
	}
}

func TestTaskNavigationStoreInvalidDataIsNeverOverwritten(t *testing.T) {
	for name, content := range map[string][]byte{
		"malformed":       []byte(`{"version":1,"pinned":["keep"],`),
		"future-version":  []byte(`{"version":2,"pinned":["keep"],"recent":[]}`),
		"missing-version": []byte(`{"pinned":["keep"],"recent":[]}`),
		"null":            []byte(`null`),
		"wrong-type":      []byte(`{"version":1,"pinned":42,"recent":[]}`),
		"empty-key":       []byte(`{"version":1,"pinned":[""],"recent":[]}`),
		"invalid-utf8":    append([]byte(`{"version":1,"pinned":["`), append([]byte{0xff}, []byte(`"],"recent":[]}`)...)...),
		"oversized":       []byte(strings.Repeat(" ", taskNavigationMaxBytes+1)),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "task-navigation.json")
			if err := os.WriteFile(path, content, 0o600); err != nil {
				t.Fatal(err)
			}
			store := newTaskNavigationStore(path)
			if _, err := store.Load(); err == nil {
				t.Fatal("invalid file loaded without an error")
			}
			if _, err := store.SetPinned("new", true); err == nil {
				t.Fatal("pin overwrote an invalid file")
			}
			if _, err := store.Visit("new"); err == nil {
				t.Fatal("visit overwrote an invalid file")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(content, after) {
				t.Fatal("invalid file was not preserved byte-for-byte")
			}
		})
	}
}

func TestTaskNavigationStoreRejectedUpdatesPreserveFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task-navigation.json")
	store := newTaskNavigationStore(path)
	if _, err := store.SetPinned("keep", true); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"", " \t", string([]byte{0xff}), strings.Repeat("x", taskNavigationMaxBytes)} {
		if _, err := store.SetPinned(key, true); err == nil {
			t.Fatal("invalid or oversized pin accepted")
		}
		if _, err := store.Visit(key); err == nil {
			t.Fatal("invalid or oversized visit accepted")
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("rejected update changed persisted navigation")
	}
}
