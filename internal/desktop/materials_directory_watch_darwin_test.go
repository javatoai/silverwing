//go:build darwin

package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func materialsDarwinTemporaryRoot(t *testing.T) string {
	t.Helper()
	// macOS's system temporary path can itself pass through /var's alias.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestMaterialsDarwinWatcherCoalescesTemporaryTreeCapturesAndKeepsLocalWrites(t *testing.T) {
	base := materialsDarwinTemporaryRoot(t)
	root := filepath.Join(base, "materials")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready, done := make(chan struct{}), make(chan error, 1)
	changes := make(chan string, 512)
	var captures atomic.Int64
	go func() {
		done <- watchMaterialsDirectoryDarwin(ctx, root, func(path string) {
			select {
			case changes <- path:
			default:
			}
		}, func() { close(ready) }, func(ctx context.Context, root string) (materialsDarwinWatchSnapshot, error) {
			captures.Add(1)
			return captureMaterialsDarwinWatchSnapshot(ctx, root)
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("cancel did not wake native kevent")
		}
	})
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("native watcher was not ready")
	}
	for i := 0; i < 128; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("file%03d.txt", i)), []byte("before"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-changes:
	case <-time.After(3 * time.Second):
		t.Fatal("batched capture did not report new files")
	}
	<-time.After(300 * time.Millisecond)
	if got := captures.Load(); got < 2 || got > 4 {
		t.Fatalf("128 structural changes used %d captures (including activation)", got)
	}
	for len(changes) > 0 {
		<-changes
	}
	before := captures.Load()
	if err := os.WriteFile(filepath.Join(root, "file000.txt"), []byte("after!"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case path := <-changes:
		if path != "file000.txt" {
			t.Fatalf("local write reported unrelated path %q", path)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("file-only write was not reported")
	}
	<-time.After(100 * time.Millisecond)
	if captures.Load() != before {
		t.Fatal("ordinary file write triggered full capture")
	}
}

func TestMaterialsDarwinWatchIdentityAndRedirectSafety(t *testing.T) {
	base := materialsDarwinTemporaryRoot(t)
	parent, root := filepath.Join(base, "parent"), filepath.Join(base, "parent", "materials")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "safe.txt"), []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := captureMaterialsDarwinWatchSnapshot(context.Background(), root)
	if err != nil || !materialsDarwinWatchIdentityCurrent(before.identity) {
		t.Fatalf("initial identity unavailable: %v", err)
	}
	if err := os.Rename(parent, filepath.Join(base, "old-parent")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if materialsDarwinWatchIdentityCurrent(before.identity) {
		t.Fatal("ancestor/root replacement retained identity")
	}
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "outside.txt"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, root); err != nil {
		t.Fatal(err)
	}
	unsafe, err := captureMaterialsDarwinWatchSnapshot(context.Background(), root)
	if err != nil || unsafe.state != "unsafe" || len(unsafe.entries) != 0 {
		t.Fatalf("redirect was captured: state=%s entries=%d error=%v", unsafe.state, len(unsafe.entries), err)
	}
}

func TestMaterialsDarwinWatcherClosesRemovedAndRetiredDescriptors(t *testing.T) {
	root := materialsDarwinTemporaryRoot(t)
	path := filepath.Join(root, "file.txt")
	if err := os.WriteFile(path, []byte("value"), 0600); err != nil {
		t.Fatal(err)
	}
	queue, err := syscall.Kqueue()
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(queue)
	watcher := materialsDarwinWatcher{queue: queue, paths: make(map[string]*materialsDarwinWatch), descriptors: make(map[int]*materialsDarwinWatch)}
	snapshot, err := captureMaterialsDarwinWatchSnapshot(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := watcher.reconcile(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	entry := watcher.paths[path]
	if entry == nil {
		t.Fatal("safe file vnode was not registered")
	}
	removedFD := entry.fd
	delete(snapshot.entries, path)
	if err := watcher.reconcile(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	var stat syscall.Stat_t
	if err := syscall.Fstat(removedFD, &stat); !errors.Is(err, syscall.EBADF) {
		t.Fatalf("removed vnode FD remained open: %v", err)
	}
	var remaining []int
	for fd := range watcher.descriptors {
		remaining = append(remaining, fd)
	}
	watcher.close()
	for _, fd := range remaining {
		if err := syscall.Fstat(fd, &stat); !errors.Is(err, syscall.EBADF) {
			t.Fatalf("retired vnode FD %d remained open: %v", fd, err)
		}
	}
}
