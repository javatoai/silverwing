//go:build darwin

package desktop

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// kqueue directory events describe entry changes, while file vnodes describe
// writes (including writes which preserve size and mtime). Register both to
// reproduce Source's recursive ENTRY_CREATE/DELETE/MODIFY observer.
func watchMaterialsDirectory(ctx context.Context, root string, changed func(string), ready func()) error {
	return watchMaterialsDirectoryDarwin(ctx, root, changed, ready, captureMaterialsDarwinWatchSnapshot)
}

type materialsDarwinSnapshotCapture func(context.Context, string) (materialsDarwinWatchSnapshot, error)

func watchMaterialsDirectoryDarwin(ctx context.Context, root string, changed func(string), ready func(), capture materialsDarwinSnapshotCapture) error {
	var readyOnce sync.Once
	signalReady := func() { readyOnce.Do(ready) }
	defer signalReady()
	// Preserve Source's activation-only UNC fallback before normalizing a path.
	if strings.HasPrefix(root, `\\`) {
		signalReady()
		<-ctx.Done()
		return ctx.Err()
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	root = filepath.Clean(root)
	queue, err := syscall.Kqueue()
	if err != nil {
		return err
	}
	syscall.CloseOnExec(queue)
	defer syscall.Close(queue)
	wake := make([]int, 2)
	if err := syscall.Pipe(wake); err != nil {
		return err
	}
	for _, fd := range wake {
		syscall.CloseOnExec(fd)
	}
	defer syscall.Close(wake[0])
	defer syscall.Close(wake[1])
	if _, err := syscall.Kevent(queue, []syscall.Kevent_t{{Ident: uint64(wake[0]), Filter: syscall.EVFILT_READ, Flags: syscall.EV_ADD | syscall.EV_CLEAR}}, nil, nil); err != nil {
		return err
	}
	// A pipe wakes the indefinitely blocked kevent immediately on cancellation.
	// Wait for an already running callback before closing its descriptor.
	wakeDone := make(chan struct{})
	stopWake := context.AfterFunc(ctx, func() {
		defer close(wakeDone)
		_, _ = syscall.Write(wake[1], []byte{1})
	})
	defer func() {
		if !stopWake() {
			<-wakeDone
		}
	}()
	watcher := materialsDarwinWatcher{queue: queue, paths: make(map[string]*materialsDarwinWatch), descriptors: make(map[int]*materialsDarwinWatch)}
	defer watcher.close()
	previous, err := capture(ctx, root)
	if err != nil {
		return err
	}
	if err := watcher.reconcile(ctx, previous); err != nil {
		return err
	}
	signalReady()
	events := make([]syscall.Kevent_t, 64)
	var batch materialsWatchRebuildBatch
	pendingTouched := make(map[string]struct{})
	for ctx.Err() == nil {
		var timeout *syscall.Timespec
		if deadline, pending := batch.deadline(); pending {
			remaining := max(time.Until(deadline), 0)
			value := syscall.NsecToTimespec(remaining.Nanoseconds())
			timeout = &value
		}
		count, err := syscall.Kevent(queue, nil, events, timeout)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rebuild := false
		touched := make(map[string]struct{})
		for _, event := range events[:count] {
			if int(event.Ident) == wake[0] {
				continue
			}
			if event.Flags&syscall.EV_ERROR != 0 {
				return syscall.Errno(event.Data)
			}
			entry := watcher.descriptors[int(event.Ident)]
			if entry == nil {
				continue
			}
			if entry.anchor || entry.info.IsDir() {
				// A directory event has no child name. Compare only the scoped
				// metadata tree; sibling and excluded-entry changes produce no diff.
				rebuild = true
			} else {
				touched[entry.path] = struct{}{}
				if event.Fflags&(syscall.NOTE_DELETE|syscall.NOTE_RENAME|syscall.NOTE_REVOKE) != 0 {
					rebuild = true
				}
			}
		}
		// Identity is checked before the batching delay. Root/ancestor swaps
		// invalidate readers and reconcile unsafe descriptors immediately.
		identityChanged := count > 0 && !materialsDarwinWatchIdentityCurrent(previous.identity)
		rebuild = rebuild || identityChanged
		if !rebuild {
			// Keep the comparison baseline current after file-only events. A
			// later parent sibling event must not re-emit an earlier file write.
			updated := make(map[string]os.FileInfo, len(touched))
			for path := range touched {
				info, err := os.Lstat(path)
				entry := watcher.paths[path]
				if err != nil || entry == nil || !os.SameFile(entry.info, info) || !materialsDarwinPathSafe(path) {
					rebuild = true
					break
				}
				updated[path] = info
			}
			if !rebuild {
				for path, info := range updated {
					previous.entries[path] = info
				}
			}
		}
		if rebuild {
			for path := range touched {
				pendingTouched[path] = struct{}{}
			}
			clear(touched)
			batch.record(time.Now())
		}
		if identityChanged || batch.ready(time.Now()) {
			if identityChanged {
				changed("")
			}
			for path := range pendingTouched {
				touched[path] = struct{}{}
			}
			clear(pendingTouched)
			batch.reset()
			next, err := capture(ctx, root)
			if err != nil {
				return err
			}
			if err := watcher.reconcile(ctx, next); err != nil {
				return err
			}
			if previous.state != next.state {
				changed("")
			}
			for path, info := range previous.entries {
				other := next.entries[path]
				if other == nil || !materialsDarwinSameStamp(info, other) {
					touched[path] = struct{}{}
				}
			}
			for path := range next.entries {
				if previous.entries[path] == nil {
					touched[path] = struct{}{}
				}
			}
			previous = next
		}
		for path := range touched {
			relative, err := filepath.Rel(root, path)
			if err == nil && relative == "." {
				changed("")
			} else if err == nil && filepath.IsLocal(relative) && !materialsDarwinIgnoredPath(relative) {
				changed(filepath.ToSlash(relative))
			}
		}
	}
	return ctx.Err()
}

type materialsDarwinWatchSnapshot struct {
	state    string
	entries  map[string]os.FileInfo
	anchor   string
	identity []materialsDarwinIdentityStamp
}

type materialsDarwinIdentityStamp struct {
	path    string
	info    os.FileInfo
	failure string
}

func materialsDarwinIdentityFailure(err error) string {
	if err == nil {
		return ""
	}
	if os.IsNotExist(err) {
		return "missing"
	}
	return err.Error()
}

func captureMaterialsDarwinWatchIdentity(root string) []materialsDarwinIdentityStamp {
	var identity []materialsDarwinIdentityStamp
	for path := root; ; path = filepath.Dir(path) {
		info, err := os.Lstat(path)
		identity = append(identity, materialsDarwinIdentityStamp{path: path, info: info, failure: materialsDarwinIdentityFailure(err)})
		if path == filepath.Dir(path) {
			return identity
		}
	}
}

func materialsDarwinWatchIdentityCurrent(identity []materialsDarwinIdentityStamp) bool {
	for _, expected := range identity {
		info, err := os.Lstat(expected.path)
		if materialsDarwinIdentityFailure(err) != expected.failure {
			return false
		}
		if err == nil && (!os.SameFile(expected.info, info) || expected.info.Mode().Type() != info.Mode().Type()) {
			return false
		}
	}
	return true
}

type materialsDarwinWatch struct {
	path   string
	info   os.FileInfo
	fd     int
	anchor bool
}

type materialsDarwinWatcher struct {
	queue       int
	paths       map[string]*materialsDarwinWatch
	descriptors map[int]*materialsDarwinWatch
}

func (w *materialsDarwinWatcher) close() {
	for _, entry := range w.paths {
		_ = syscall.Close(entry.fd)
	}
}

func (w *materialsDarwinWatcher) reconcile(ctx context.Context, snapshot materialsDarwinWatchSnapshot) error {
	wanted := make(map[string]os.FileInfo, len(snapshot.entries)+1)
	for path, info := range snapshot.entries {
		wanted[path] = info
	}
	if snapshot.anchor != "" {
		if info, err := os.Lstat(snapshot.anchor); err == nil && info.IsDir() {
			wanted[snapshot.anchor] = info
		}
	}
	for path, entry := range w.paths {
		if info := wanted[path]; info == nil || !os.SameFile(entry.info, info) || !materialsDarwinPathSafe(path) {
			_ = syscall.Close(entry.fd) // close also removes its kqueue filter.
			delete(w.paths, path)
			delete(w.descriptors, entry.fd)
		}
	}
	for path, info := range wanted {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry := w.paths[path]; entry != nil {
			entry.anchor = path == snapshot.anchor
			continue
		}
		if !materialsDarwinPathSafe(path) {
			continue
		}
		// O_EVTONLY never reads document bytes. O_NOFOLLOW rejects a final
		// redirect; ancestry and identity are checked on both sides of open.
		fd, err := syscall.Open(path, syscall.O_EVTONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		if err != nil {
			if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.EACCES) {
				continue // Source retries concurrent removal/access changes on the next event/activation.
			}
			return err
		}
		var opened syscall.Stat_t
		err = syscall.Fstat(fd, &opened)
		actual, statErr := os.Lstat(path)
		expected, ok := info.Sys().(*syscall.Stat_t)
		if err != nil || statErr != nil || !ok || opened.Dev != expected.Dev || opened.Ino != expected.Ino || !os.SameFile(info, actual) || !materialsDarwinPathSafe(path) {
			_ = syscall.Close(fd)
			continue
		}
		filter := syscall.Kevent_t{Ident: uint64(fd), Filter: syscall.EVFILT_VNODE, Flags: syscall.EV_ADD | syscall.EV_CLEAR,
			Fflags: syscall.NOTE_WRITE | syscall.NOTE_EXTEND | syscall.NOTE_ATTRIB | syscall.NOTE_LINK | syscall.NOTE_DELETE | syscall.NOTE_RENAME | syscall.NOTE_REVOKE}
		if _, err := syscall.Kevent(w.queue, []syscall.Kevent_t{filter}, nil, nil); err != nil {
			_ = syscall.Close(fd)
			return err
		}
		entry := &materialsDarwinWatch{path: path, info: info, fd: fd, anchor: path == snapshot.anchor}
		w.paths[path], w.descriptors[fd] = entry, entry
	}
	return nil
}

func captureMaterialsDarwinWatchSnapshot(ctx context.Context, root string) (materialsDarwinWatchSnapshot, error) {
	snapshot := materialsDarwinWatchSnapshot{state: "missing", entries: make(map[string]os.FileInfo), identity: captureMaterialsDarwinWatchIdentity(root)}
	if materialsDarwinIgnoredPath(root) {
		snapshot.state = "unsafe"
		return snapshot, ctx.Err()
	}
	// As in Source, the anchor is the nearest existing safe ancestor. It
	// observes root deletion and construction of several missing ancestors.
	for parent := filepath.Dir(root); ; parent = filepath.Dir(parent) {
		if err := ctx.Err(); err != nil {
			return snapshot, err
		}
		if !materialsDarwinPathSafe(parent) {
			break
		}
		if info, err := os.Lstat(parent); err == nil && info.IsDir() {
			snapshot.anchor = parent
			break
		}
		if parent == filepath.Dir(parent) {
			break
		}
	}
	if !materialsDarwinPathSafe(root) {
		snapshot.state = "unsafe"
		return snapshot, ctx.Err()
	}
	info, err := os.Lstat(root)
	if err != nil {
		if !os.IsNotExist(err) {
			snapshot.state = "unavailable"
		}
		return snapshot, ctx.Err()
	}
	if !info.IsDir() {
		snapshot.state = "unsafe"
		return snapshot, ctx.Err()
	}
	snapshot.state = "directory"
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if materialsDarwinIgnoredPath(entry.Name()) || entry.Type()&os.ModeSymlink != 0 || !materialsDarwinPathSafe(path) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() || info.Mode().IsRegular() {
			snapshot.entries[path] = info
		}
		return nil
	})
	if err != nil && ctx.Err() == nil {
		snapshot.state = "unavailable"
		clear(snapshot.entries)
	}
	return snapshot, ctx.Err()
}

func materialsDarwinPathSafe(path string) bool {
	// Lstat each component: EvalSymlinks would erase redirect evidence.
	for component := filepath.Clean(path); ; component = filepath.Dir(component) {
		info, err := os.Lstat(component)
		if err != nil && !os.IsNotExist(err) {
			return false
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return false
		}
		if component == filepath.Dir(component) {
			return true
		}
	}
}

func materialsDarwinIgnoredPath(path string) bool {
	for _, segment := range strings.Split(filepath.ToSlash(path), "/") {
		if strings.EqualFold(segment, ".git") || materialsRefreshStagingName.MatchString(segment) {
			return true
		}
	}
	return false
}

func materialsDarwinSameStamp(before, after os.FileInfo) bool {
	if before.IsDir() != after.IsDir() || !os.SameFile(before, after) {
		return false
	}
	if before.IsDir() {
		return true // Source's directory catalog contains paths, not mtimes.
	}
	a, aOK := before.Sys().(*syscall.Stat_t)
	b, bOK := after.Sys().(*syscall.Stat_t)
	return before.Size() == after.Size() && before.ModTime().Equal(after.ModTime()) && aOK && bOK && a.Birthtimespec == b.Birthtimespec
}
