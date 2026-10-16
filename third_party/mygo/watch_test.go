//go:build !js

package mygo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/egoist/mygo/internal/fake"
	"github.com/egoist/mygo/internal/platform"
)

// fileWatchTestNotice reports notice for the target at path, found by
// path: on Windows the root's parent is enrolled before the root, so IDs
// differ.
func fileWatchTestNotice(t *testing.T, w *FileWatcher, path string, notice platform.FileWatchNotice) {
	t.Helper()
	native := w.native.(*fake.FileWatch)
	for _, target := range native.Targets() {
		if target.Path == filepath.Join(w.root, filepath.FromSlash(path)) {
			notice.Target = target.ID
			native.Notify(notice)
			return
		}
	}
	t.Fatalf("no watch target for %q", path)
}

// fileWatchTestEvents holds the events of batches not yet looked at.
var fileWatchTestEvents = map[*FileWatcher][]FileEvent{}

// fileWatchTestNext returns the next event, from the batch Next returned
// last or the next one.
func fileWatchTestNext(t *testing.T, w *FileWatcher) FileEvent {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for len(fileWatchTestEvents[w]) == 0 {
		batch, err := w.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(batch) == 0 {
			t.Fatal("empty batch")
		}
		fileWatchTestEvents[w] = batch
	}
	e := fileWatchTestEvents[w][0]
	fileWatchTestEvents[w] = fileWatchTestEvents[w][1:]
	return e
}

// fileWatchTestBatch returns the next batch.
func fileWatchTestBatch(t *testing.T, w *FileWatcher) []FileEvent {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	batch, err := w.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return batch
}

func TestFileWatchLifecycle(t *testing.T) {
	dir := t.TempDir()
	w, err := WatchFiles(context.Background(), dir, FileWatchOptions{Debounce: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if want, _ := filepath.EvalSymlinks(dir); w.Root() != want {
		t.Fatalf("root = %q, want %q", w.Root(), want)
	}
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	fileWatchTestNotice(t, w, ".", platform.FileWatchNotice{})
	// Directories written are not reported: their entries are.
	if batch := fileWatchTestBatch(t, w); !slices.Equal(batch, []FileEvent{{Op: FileCreate, Path: "a"}}) {
		t.Fatalf("batch = %+v", batch)
	}
	wait, cancelWait := context.WithCancel(context.Background())
	cancelWait()
	if _, err = w.Next(wait); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	w.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err = w.Next(ctx); err != io.EOF || w.Err() != nil {
		t.Fatal(err, w.Err())
	}
	select {
	case <-w.Done():
	case <-ctx.Done():
		t.Fatal("cleanup timed out")
	}
}

// Canceling the context of a watch stops it as Close does.
func TestFileWatchContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	w, err := WatchFiles(ctx, t.TempDir(), FileWatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	<-w.Done()
	if _, err = w.Next(context.Background()); err != io.EOF || w.Err() != nil {
		t.Fatal(err, w.Err())
	}
}

func TestFileWatchParentSentinel(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows enrolls a parent sentinel")
	}
	w, err := WatchFiles(context.Background(), t.TempDir(), FileWatchOptions{Debounce: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := os.WriteFile(filepath.Join(w.root, "a"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	// A hint for a sibling must not scan the root or expose the sibling.
	fileWatchTestNotice(t, w, "..", platform.FileWatchNotice{Name: filepath.Base(w.root) + "-sibling"})
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := w.Next(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("sibling hint was not ignored: %v", err)
	}
	fileWatchTestNotice(t, w, "..", platform.FileWatchNotice{Name: filepath.Base(w.root)})
	if e := fileWatchTestNext(t, w); e != (FileEvent{Op: FileCreate, Path: "a"}) {
		t.Fatalf("unexpected event: %+v", e)
	}
}

func TestFileWatchStaleFailureIgnored(t *testing.T) {
	w, err := WatchFiles(context.Background(), t.TempDir(), FileWatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	fb.LastFileWatch().Notify(platform.FileWatchNotice{Target: 99999, Err: errors.New("retired target")})
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := w.Next(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stale failure stopped watcher: %v", err)
	}
}

func TestFileWatchValidation(t *testing.T) {
	if w, err := WatchFiles(context.Background(), t.TempDir(), FileWatchOptions{MaxEntries: -1}); err == nil {
		w.Close()
		t.Fatal("accepted a negative limit")
	}
	if _, err := WatchFiles(nil, t.TempDir(), FileWatchOptions{}); err == nil {
		t.Fatal("nil context")
	}
	if _, err := WatchFiles(context.Background(), "", FileWatchOptions{}); err == nil {
		t.Fatal("empty path")
	}
	// A MaxDelay shorter than Debounce is Debounce.
	w, err := WatchFiles(context.Background(), t.TempDir(), FileWatchOptions{Debounce: time.Second, MaxDelay: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if w.opts.MaxDelay != time.Second {
		t.Fatalf("MaxDelay = %v", w.opts.MaxDelay)
	}
}

func TestFileWatchRecursiveLimit(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a", "b"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := WatchFiles(context.Background(), dir, FileWatchOptions{Recursive: true, MaxEntries: 2}); !errors.Is(err, ErrWatchLimit) {
		t.Fatalf("limit: %v", err)
	}
}

// The watch doesn't look again while a batch waits for Next, and what
// changed meanwhile comes in the next batch.
func TestFileWatchBackpressure(t *testing.T) {
	dir := t.TempDir()
	w, err := WatchFiles(context.Background(), dir, FileWatchOptions{Debounce: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for _, name := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
		fileWatchTestNotice(t, w, ".", platform.FileWatchNotice{Name: name, Kind: platform.FileWatchCreate})
		time.Sleep(20 * time.Millisecond)
	}
	if batch := fileWatchTestBatch(t, w); !slices.Equal(batch, []FileEvent{{Op: FileCreate, Path: "a"}}) {
		t.Fatalf("first batch = %+v", batch)
	}
	if batch := fileWatchTestBatch(t, w); !slices.Equal(batch, []FileEvent{{Op: FileCreate, Path: "b"}}) {
		t.Fatalf("second batch = %+v", batch)
	}
	// Many notices while a batch waits look at everything once.
	for i := range 20 {
		name := fmt.Sprint("c", i)
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
		fileWatchTestNotice(t, w, ".", platform.FileWatchNotice{Name: name, Kind: platform.FileWatchCreate})
	}
	seen := 0
	for seen < 20 {
		seen += len(fileWatchTestBatch(t, w))
	}
	if seen != 20 {
		t.Fatalf("%d events", seen)
	}
}

// A watched directory removed ends the watch after an event saying so.
func TestFileWatchRootLoss(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "root")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	w, err := WatchFiles(context.Background(), dir, FileWatchOptions{Debounce: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	fileWatchTestNotice(t, w, ".", platform.FileWatchNotice{Kind: platform.FileWatchRemove})
	if batch := fileWatchTestBatch(t, w); !slices.Equal(batch, []FileEvent{{Op: FileRemove, Path: ".", IsDir: true}}) {
		t.Fatalf("batch = %+v", batch)
	}
	if _, err = w.Next(context.Background()); err != io.EOF || w.Err() != nil {
		t.Fatal(err, w.Err())
	}
}

// An entry replaced or removed between the scan seeing it and the backend
// enrolling it doesn't stop the watch.
func TestFileWatchEnrollmentRace(t *testing.T) {
	for _, recursive := range []bool{false, true} {
		for _, replace := range []bool{false, true} {
			for _, isDir := range []bool{false, true} {
				name := fmt.Sprintf("recursive=%v/replace=%v/directory=%v", recursive, replace, isDir)
				t.Run(name, func(t *testing.T) {
					dir := t.TempDir()
					w, err := WatchFiles(context.Background(), dir, FileWatchOptions{Recursive: recursive, Debounce: -1})
					if err != nil {
						t.Fatal(err)
					}
					defer w.Close()
					path := filepath.Join(dir, "entry")
					create := func(path string) error {
						if isDir {
							return os.Mkdir(path, 0700)
						}
						return os.WriteFile(path, []byte("data"), 0600)
					}
					raced := false
					w.native.(*fake.FileWatch).AddHook = func(target platform.FileWatchTarget) error {
						if target.Path != filepath.Join(w.root, "entry") || raced {
							return nil
						}
						raced = true
						if !replace {
							return os.Remove(path)
						}
						replacement := filepath.Join(dir, "replacement")
						if err := create(replacement); err != nil {
							return err
						}
						if isDir {
							if err := os.Rename(path, filepath.Join(t.TempDir(), "retired")); err != nil {
								return err
							}
						}
						return os.Rename(replacement, path)
					}
					if err := create(path); err != nil {
						t.Fatal(err)
					}
					fileWatchTestNotice(t, w, ".", platform.FileWatchNotice{Name: "entry", Kind: platform.FileWatchCreate})
					// An entry removed meanwhile was never there.
					for replace {
						if e := fileWatchTestNext(t, w); e.Path == "entry" {
							break
						}
					}
					if err := os.WriteFile(filepath.Join(dir, "later"), nil, 0600); err != nil {
						t.Fatal(err)
					}
					fileWatchTestNotice(t, w, ".", platform.FileWatchNotice{Name: "later", Kind: platform.FileWatchCreate})
					for {
						if e := fileWatchTestNext(t, w); e.Path == "later" && e.Op == FileCreate {
							break
						}
					}
					if !raced {
						t.Fatal("the entry was not enrolled")
					}
				})
			}
		}
	}
}

// A notice for a file looks only at it, and one for a directory only at
// its entries.
func TestFileWatchLooksOnlyWhereNotified(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"a", "b/c", "d/e"} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	w, err := WatchFiles(context.Background(), dir, FileWatchOptions{Recursive: true, Debounce: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	// Changes the watch isn't told about stay unseen.
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("unseen"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "d", "f"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b", "c"), []byte("seen"), 0600); err != nil {
		t.Fatal(err)
	}
	fileWatchTestNotice(t, w, "b", platform.FileWatchNotice{Name: "c", Kind: platform.FileWatchWrite})
	if e := fileWatchTestNext(t, w); e != (FileEvent{Op: FileWrite, Path: "b/c"}) {
		t.Fatalf("event = %+v", e)
	}
	fileWatchTestNotice(t, w, "d", platform.FileWatchNotice{})
	if e := fileWatchTestNext(t, w); e != (FileEvent{Op: FileCreate, Path: "d/f"}) {
		t.Fatalf("event = %+v", e)
	}
	// Losing changes looks at everything.
	fb.LastFileWatch().Notify(platform.FileWatchNotice{Err: platform.ErrFileWatchOverflow})
	if e := fileWatchTestNext(t, w); e != (FileEvent{Op: FileWrite, Path: "a"}) {
		t.Fatalf("event = %+v", e)
	}
}

// A directory renamed is one rename, not one for everything in it.
func TestFileWatchDirectoryRename(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a", "b"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a", "b", "c"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	w, err := WatchFiles(context.Background(), dir, FileWatchOptions{Recursive: true, Debounce: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := os.Rename(filepath.Join(dir, "a"), filepath.Join(dir, "z")); err != nil {
		t.Fatal(err)
	}
	native := w.native.(*fake.FileWatch)
	fileWatchTestNotice(t, w, ".", platform.FileWatchNotice{Name: "a", Kind: platform.FileWatchMoveFrom})
	if e := fileWatchTestNext(t, w); e != (FileEvent{Op: FileRename, OldPath: "a", Path: "z", IsDir: true}) {
		t.Fatalf("event = %+v", e)
	}
	// Its directories are enrolled again under their new paths.
	enrolled := map[string]bool{}
	for _, target := range native.Targets() {
		enrolled[target.Path] = true
	}
	for _, p := range []string{"z", "z/b", "z/b/c"} {
		if !enrolled[filepath.Join(w.root, filepath.FromSlash(p))] {
			t.Errorf("%s not enrolled", p)
		}
	}
	if enrolled[filepath.Join(w.root, "a")] {
		t.Error("a still enrolled")
	}
}

// A backend that watches a tree with one target is given only the root,
// and names what changes below by its path.
func TestFileWatchSubtree(t *testing.T) {
	fb.FileWatchSubtree = true
	defer func() { fb.FileWatchSubtree = false }()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a", "b"), 0700); err != nil {
		t.Fatal(err)
	}
	w, err := WatchFiles(context.Background(), dir, FileWatchOptions{Recursive: true, Debounce: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	native := w.native.(*fake.FileWatch)
	for _, target := range native.Targets() {
		if target.Path == w.root {
			if !target.Subtree {
				t.Error("the root is not a subtree target")
			}
		} else if target.Path != filepath.Dir(w.root) {
			t.Errorf("%s enrolled", target.Path)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "a", "b", "c", "d"), 0700); err != nil {
		t.Fatal(err)
	}
	fileWatchTestNotice(t, w, ".", platform.FileWatchNotice{Name: "a/b/c", Kind: platform.FileWatchCreate})
	want := []FileEvent{{Op: FileCreate, Path: "a/b/c", IsDir: true}, {Op: FileCreate, Path: "a/b/c/d", IsDir: true}}
	for _, want := range want {
		if e := fileWatchTestNext(t, w); e != want {
			t.Fatalf("event = %+v, want %+v", e, want)
		}
	}
	// The root's target lost enrolls it again and looks at everything.
	if err := os.WriteFile(filepath.Join(dir, "a", "f"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	fileWatchTestNotice(t, w, ".", platform.FileWatchNotice{Kind: platform.FileWatchLost})
	if e := fileWatchTestNext(t, w); e != (FileEvent{Op: FileCreate, Path: "a/f"}) {
		t.Fatalf("event = %+v", e)
	}
	found := false
	for _, target := range native.Targets() {
		found = found || target.Path == w.root && target.Subtree
	}
	if !found {
		t.Fatal("the root was not enrolled again")
	}
	fileWatchTestNotice(t, w, ".", platform.FileWatchNotice{Name: "../x"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := w.Next(ctx); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a name out of the tree: %v", err)
	}
}

type watchTestInfo struct{ os.FileInfo }

func TestFileWatchDiff(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	if err := os.WriteFile(a, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(a)
	if err != nil {
		t.Fatal(err)
	}
	// Stat through a handle captures the identity now. On Windows os.Stat
	// defers fetching it until SameFile, when the old name no longer exists.
	info, err := f.Stat()
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	old := map[string]os.FileInfo{"a": info}
	if err := os.Rename(a, b); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(b)
	if err != nil {
		t.Fatal(err)
	}
	events := fileWatchDiff(old, map[string]os.FileInfo{"b": info}, map[string]bool{"a": true})
	if len(events) != 2 || events[0].Op != FileRename || events[0].OldPath != "a" || events[1].Op != FileWrite {
		t.Fatalf("%+v", events)
	}
}

// A file is watched through its directory: written, replaced as editors
// save, removed and created again, then gone with its directory.
func TestFileWatchFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dir")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "config.json")
	if err := os.WriteFile(file, []byte("a"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sibling"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	w, err := WatchFiles(context.Background(), file, FileWatchOptions{Debounce: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	native := w.native.(*fake.FileWatch)
	enrolled := map[string]bool{}
	for _, target := range native.Targets() {
		enrolled[target.Path] = true
	}
	if !enrolled[w.Root()] || !enrolled[filepath.Dir(w.Root())] || len(enrolled) != 2 {
		t.Fatalf("enrolled %v", enrolled)
	}
	next := func(want FileEvent) {
		t.Helper()
		if batch := fileWatchTestBatch(t, w); !slices.Equal(batch, []FileEvent{want}) {
			t.Fatalf("batch = %+v, want %+v", batch, want)
		}
	}
	quiet := func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		if batch, err := w.Next(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("batch = %+v, %v", batch, err)
		}
	}
	if err := os.WriteFile(file, []byte("bb"), 0600); err != nil {
		t.Fatal(err)
	}
	fileWatchTestNotice(t, w, ".", platform.FileWatchNotice{Kind: platform.FileWatchWrite})
	next(FileEvent{Op: FileWrite, Path: "."})
	// Other files of the directory are not reported.
	if err := os.WriteFile(filepath.Join(dir, "sibling"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	fileWatchTestNotice(t, w, "..", platform.FileWatchNotice{Name: "sibling", Kind: platform.FileWatchWrite})
	fileWatchTestNotice(t, w, "..", platform.FileWatchNotice{})
	quiet()
	// Replaced, as editors save.
	if err := os.WriteFile(filepath.Join(dir, "config.json.tmp"), []byte("ccc"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "config.json.tmp"), file); err != nil {
		t.Fatal(err)
	}
	fileWatchTestNotice(t, w, "..", platform.FileWatchNotice{Name: "config.json", Kind: platform.FileWatchMoveTo})
	next(FileEvent{Op: FileWrite, Path: "."})
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	fileWatchTestNotice(t, w, "..", platform.FileWatchNotice{Name: "config.json", Kind: platform.FileWatchRemove})
	next(FileEvent{Op: FileRemove, Path: "."})
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	fileWatchTestNotice(t, w, "..", platform.FileWatchNotice{Name: "config.json", Kind: platform.FileWatchCreate})
	next(FileEvent{Op: FileCreate, Path: "."})
	// The new file is watched in its turn.
	found := false
	for _, target := range native.Targets() {
		found = found || target.Path == w.Root()
	}
	if !found {
		t.Fatal("the new file is not enrolled")
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	fileWatchTestNotice(t, w, "..", platform.FileWatchNotice{Kind: platform.FileWatchLost})
	next(FileEvent{Op: FileRemove, Path: "."})
	if _, err := w.Next(context.Background()); err != io.EOF || w.Err() != nil {
		t.Fatal(err, w.Err())
	}
}

// What Ignore ignores is never enrolled, counted or reported.
func TestFileWatchIgnore(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"node_modules/a/b/c", "node_modules/d", "src/main.go", "debug.log"} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var asked []string
	ignore := func(p string, isDir bool) bool {
		asked = append(asked, p)
		return p == "node_modules" && isDir || path.Ext(p) == ".log"
	}
	// Only the root, src, src/main.go and src/new.go, created below,
	// count.
	w, err := WatchFiles(context.Background(), dir, FileWatchOptions{Recursive: true, Debounce: -1, Ignore: ignore, MaxEntries: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for _, p := range asked {
		if strings.HasPrefix(p, "node_modules/") {
			t.Errorf("asked about %s", p)
		}
	}
	for _, target := range w.native.(*fake.FileWatch).Targets() {
		if strings.Contains(target.Path, "node_modules") || strings.HasSuffix(target.Path, ".log") {
			t.Errorf("%s enrolled", target.Path)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "node_modules", "e"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.log"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	fileWatchTestNotice(t, w, ".", platform.FileWatchNotice{Name: "other.log", Kind: platform.FileWatchCreate})
	fileWatchTestNotice(t, w, ".", platform.FileWatchNotice{Name: "node_modules", Kind: platform.FileWatchWrite})
	if err := os.WriteFile(filepath.Join(dir, "src", "new.go"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	fileWatchTestNotice(t, w, "src", platform.FileWatchNotice{Name: "new.go", Kind: platform.FileWatchCreate})
	if batch := fileWatchTestBatch(t, w); !slices.Equal(batch, []FileEvent{{Op: FileCreate, Path: "src/new.go"}}) {
		t.Fatalf("batch = %+v", batch)
	}
}

// Notices in an ignored directory, which a Subtree target reports, are
// dropped.
func TestFileWatchIgnoreSubtree(t *testing.T) {
	fb.FileWatchSubtree = true
	defer func() { fb.FileWatchSubtree = false }()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git", "objects"), 0700); err != nil {
		t.Fatal(err)
	}
	w, err := WatchFiles(context.Background(), dir, FileWatchOptions{Recursive: true, Debounce: -1, Ignore: func(p string, isDir bool) bool { return p == ".git" }})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := os.WriteFile(filepath.Join(dir, ".git", "objects", "x"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	fileWatchTestNotice(t, w, ".", platform.FileWatchNotice{Name: ".git/objects/x", Kind: platform.FileWatchCreate})
	if err := os.WriteFile(filepath.Join(dir, "y"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	fileWatchTestNotice(t, w, ".", platform.FileWatchNotice{Name: "y", Kind: platform.FileWatchCreate})
	if batch := fileWatchTestBatch(t, w); !slices.Equal(batch, []FileEvent{{Op: FileCreate, Path: "y"}}) {
		t.Fatalf("batch = %+v", batch)
	}
}

// Changes of permissions alone are not writes.
func TestFileWatchMetadata(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	w, err := WatchFiles(context.Background(), dir, FileWatchOptions{Debounce: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := os.Chmod(file, 0400); err != nil {
		t.Fatal(err)
	}
	fileWatchTestNotice(t, w, "a", platform.FileWatchNotice{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if batch, err := w.Next(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("batch = %+v, %v", batch, err)
	}
	fileWatchTestNotice(t, w, "a", platform.FileWatchNotice{Kind: platform.FileWatchWrite})
	if batch := fileWatchTestBatch(t, w); !slices.Equal(batch, []FileEvent{{Op: FileWrite, Path: "a"}}) {
		t.Fatalf("batch = %+v", batch)
	}
}

// What the watch can't look at, as a directory without permission or a
// file the system holds (pagefile.sys), is skipped rather than ending the
// watch.
func TestFileWatchInaccessible(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "busy"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	locked := runtime.GOOS != "windows" && os.Geteuid() != 0
	if locked {
		for _, p := range []string{"locked/a", "unsearchable/b"} {
			if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, p), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Chmod(filepath.Join(dir, "locked"), 0); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(dir, "unsearchable"), 0400); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			os.Chmod(filepath.Join(dir, "locked"), 0700)
			os.Chmod(filepath.Join(dir, "unsearchable"), 0700)
		})
	}
	var busy error = fs.ErrPermission
	if runtime.GOOS == "windows" {
		busy = syscall.Errno(32) // ERROR_SHARING_VIOLATION
	}
	fb.FileWatchAddHook = func(target platform.FileWatchTarget) error {
		if filepath.Base(target.Path) == "busy" {
			return busy
		}
		return nil
	}
	defer func() { fb.FileWatchAddHook = nil }()
	w, err := WatchFiles(context.Background(), dir, FileWatchOptions{Recursive: true, Debounce: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for _, target := range w.native.(*fake.FileWatch).Targets() {
		if filepath.Base(target.Path) == "busy" {
			t.Error("busy enrolled")
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "x"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	fileWatchTestNotice(t, w, ".", platform.FileWatchNotice{Name: "x", Kind: platform.FileWatchCreate})
	fileWatchTestNotice(t, w, ".", platform.FileWatchNotice{})
	if batch := fileWatchTestBatch(t, w); !slices.Equal(batch, []FileEvent{{Op: FileCreate, Path: "x"}}) {
		t.Fatalf("batch = %+v", batch)
	}
	if locked {
		fileWatchTestNotice(t, w, "unsearchable", platform.FileWatchNotice{})
		if err := os.Remove(filepath.Join(dir, "x")); err != nil {
			t.Fatal(err)
		}
		fileWatchTestNotice(t, w, ".", platform.FileWatchNotice{Name: "x"})
		if batch := fileWatchTestBatch(t, w); !slices.Equal(batch, []FileEvent{{Op: FileRemove, Path: "x"}}) {
			t.Fatalf("batch = %+v", batch)
		}
	}
}
