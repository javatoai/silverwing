//go:build windows

package desktop

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

var materialsWatchGetFinalPath = materialsWatchKernel.NewProc("GetFinalPathNameByHandleW")
var errMaterialsWindowsWatchRedirect = errors.New("资料观察目录及祖先不能是符号链接或 junction")
var errMaterialsWindowsWatchNotDirectory = errors.New("资料观察根不是目录")

type materialsWindowsWatchTarget struct {
	path      string
	identity  syscall.ByHandleFileInformation
	recursive bool
}

type materialsWindowsWatchRegistration struct {
	materialsWindowsWatchTarget
	cancel    context.CancelFunc
	ready     chan struct{}
	done      chan struct{}
	err       error
	installed bool
}

type materialsWindowsWatchEvent struct {
	registration *materialsWindowsWatchRegistration
	relative     string
	ended        bool
}

func watchMaterialsDirectory(ctx context.Context, root string, changed func(string), ready func()) error {
	var readyOnce sync.Once
	signalReady := func() { readyOnce.Do(ready) }
	defer signalReady()
	// Preserve Source's activation-only network-path fallback.
	if strings.HasPrefix(root, `\\`) && !strings.HasPrefix(root, `\\?\`) || strings.HasPrefix(strings.ToLower(root), `\\?\unc\`) {
		signalReady()
		<-ctx.Done()
		return ctx.Err()
	}
	var err error
	root, err = filepath.Abs(materialsWindowsWatchPlainPath(root))
	if err != nil {
		return err
	}
	root = filepath.Clean(root)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	registrations := make(map[string]*materialsWindowsWatchRegistration)
	events := make(chan materialsWindowsWatchEvent, 256)
	defer func() {
		cancel()
		for _, registration := range registrations {
			registration.cancel()
		}
		for _, registration := range registrations {
			<-registration.done
		}
	}()
	reconcile := func() error {
		// Retry only when registration races a native lifecycle change. This
		// is never a timer or a periodic filesystem scan.
		for ctx.Err() == nil {
			targets, err := materialsWindowsWatchTargets(ctx, root)
			if err != nil {
				return err
			}
			wanted := make(map[string]materialsWindowsWatchTarget, len(targets))
			for _, target := range targets {
				wanted[normalizedReadingPath(target.path)] = target
			}
			for key, registration := range registrations {
				target, exists := wanted[key]
				ended := false
				select {
				case <-registration.done:
					ended = true
				default:
				}
				if exists && materialsWindowsWatchSameIdentity(registration.identity, target.identity) && !ended {
					continue
				}
				registration.cancel()
				<-registration.done
				delete(registrations, key)
				if exists && ended && materialsWindowsWatchSameIdentity(registration.identity, target.identity) && registration.err != nil {
					return registration.err
				}
			}
			retry := false
			// Outer guards are posted first; inner guards and the root follow.
			for index := len(targets) - 1; index >= 0; index-- {
				target := targets[index]
				key := normalizedReadingPath(target.path)
				if registrations[key] != nil {
					continue
				}
				watchCtx, stop := context.WithCancel(ctx)
				registration := &materialsWindowsWatchRegistration{materialsWindowsWatchTarget: target, cancel: stop, ready: make(chan struct{}), done: make(chan struct{})}
				registrations[key] = registration
				go func() {
					registration.err = watchMaterialsDirectoryHandleInstalled(watchCtx, target.path, target.recursive, func(relative string) {
						select {
						case events <- materialsWindowsWatchEvent{registration: registration, relative: relative}:
						case <-watchCtx.Done():
						}
					}, func() { close(registration.ready) }, func(info syscall.ByHandleFileInformation) {
						registration.identity = info
						registration.installed = true
					})
					close(registration.done)
					select {
					case events <- materialsWindowsWatchEvent{registration: registration, ended: true}:
					case <-watchCtx.Done():
					}
				}()
				select {
				case <-registration.ready:
				case <-ctx.Done():
					return ctx.Err()
				}
				if !registration.installed {
					<-registration.done
					stop()
					delete(registrations, key)
					current, currentErr := materialsWindowsWatchDirectoryIdentity(target.path)
					if currentErr == nil && materialsWindowsWatchSameIdentity(target.identity, current) || !materialsWindowsWatchLifecycleError(registration.err) {
						return registration.err
					}
					retry = true
				}
			}
			if retry {
				continue
			}
			// Close the initial capture/registration gap without polling. New
			// ancestors created while the outer guards were posted are included.
			next, err := materialsWindowsWatchTargets(ctx, root)
			if err != nil {
				return err
			}
			stable := len(next) == len(registrations)
			for _, target := range next {
				registration := registrations[normalizedReadingPath(target.path)]
				if registration == nil || !materialsWindowsWatchSameIdentity(registration.identity, target.identity) {
					stable = false
					break
				}
			}
			if stable {
				return nil
			}
		}
		return ctx.Err()
	}
	if err := reconcile(); err != nil {
		return err
	}
	signalReady()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event := <-events:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			registration := event.registration
			if registrations[normalizedReadingPath(registration.path)] != registration {
				continue // cancelled/replaced handles cannot refresh a new task/root.
			}
			current, identityErr := materialsWindowsWatchDirectoryIdentity(registration.path)
			if event.ended || identityErr != nil || !materialsWindowsWatchSameIdentity(registration.identity, current) {
				if err := reconcile(); err != nil {
					return err
				}
				changed("")
				continue
			}
			if event.relative != "" && (!filepath.IsLocal(event.relative) || materialsWindowsWatchIgnoredPath(event.relative)) {
				continue
			}
			if !registration.recursive {
				path := filepath.Join(registration.path, event.relative)
				if event.relative != "" && !materialsWindowsWatchContains(path, root) {
					continue // a guard observes only the branch leading to this root.
				}
				if err := reconcile(); err != nil {
					return err
				}
				changed("")
				continue
			}
			if event.relative != "" {
				if err := materialsWindowsWatchPathSafe(filepath.Join(root, event.relative)); err != nil && !materialsWindowsWatchMissing(err) {
					continue // never deliver events through a child junction/symlink.
				}
			}
			changed(event.relative)
		}
	}
}

// ReadDirectoryChangesW does not report changes to its own watched directory.
// Non-recursive ancestors therefore guard each other's lifecycle, including
// the nearest existing Source anchor, without watching sibling subtrees.
func materialsWindowsWatchTargets(ctx context.Context, root string) ([]materialsWindowsWatchTarget, error) {
	if materialsWindowsWatchIgnoredPath(root) {
		return nil, ctx.Err()
	}
	targets := make([]materialsWindowsWatchTarget, 0, 8)
	for path := root; ; path = filepath.Dir(path) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		identity, err := materialsWindowsWatchDirectoryIdentity(path)
		if err == nil {
			targets = append(targets, materialsWindowsWatchTarget{path: path, identity: identity, recursive: strings.EqualFold(path, root)})
		} else if errors.Is(err, errMaterialsWindowsWatchRedirect) && !strings.EqualFold(path, root) {
			return nil, nil // Source refuses an anchor with redirected ancestry.
		} else if !materialsWindowsWatchMissing(err) && !errors.Is(err, errMaterialsWindowsWatchRedirect) && !errors.Is(err, errMaterialsWindowsWatchNotDirectory) && !errors.Is(err, syscall.ERROR_ACCESS_DENIED) {
			return nil, err
		}
		if path == filepath.Dir(path) {
			return targets, nil
		}
	}
}

func materialsWindowsWatchDirectoryIdentity(path string) (info syscall.ByHandleFileInformation, err error) {
	if err := materialsWindowsWatchPathSafe(path); err != nil {
		return info, err
	}
	name, err := syscall.UTF16PtrFromString(materialsWindowsWatchNativePath(path))
	if err != nil {
		return info, err
	}
	handle, err := syscall.CreateFile(name, 0, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil,
		syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS|syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return info, err
	}
	defer syscall.CloseHandle(handle)
	if err := syscall.GetFileInformationByHandle(handle, &info); err != nil {
		return info, err
	}
	if info.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return info, errMaterialsWindowsWatchRedirect
	}
	if info.FileAttributes&syscall.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return info, errMaterialsWindowsWatchNotDirectory
	}
	return info, materialsWindowsWatchHandlePathSafe(handle, path)
}

func materialsWindowsWatchPathSafe(path string) error {
	components := make([]string, 0, 8)
	for component := filepath.Clean(path); ; component = filepath.Dir(component) {
		components = append(components, component)
		if component == filepath.Dir(component) {
			break
		}
	}
	for index := len(components) - 1; index >= 0; index-- {
		component := components[index]
		name, err := syscall.UTF16PtrFromString(materialsWindowsWatchNativePath(component))
		if err != nil {
			return err
		}
		attributes, err := syscall.GetFileAttributes(name)
		if err != nil {
			return err
		} else if attributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return errMaterialsWindowsWatchRedirect
		}
		if index != 0 && attributes&syscall.FILE_ATTRIBUTE_DIRECTORY == 0 {
			return syscall.ERROR_PATH_NOT_FOUND
		}
	}
	return nil
}

func materialsWindowsWatchHandlePathSafe(handle syscall.Handle, path string) error {
	buffer := make([]uint16, 512)
	for {
		count, _, err := materialsWatchGetFinalPath.Call(uintptr(handle), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0)
		if count == 0 {
			return err
		}
		if count < uintptr(len(buffer)) {
			actual := materialsWindowsWatchPlainPath(syscall.UTF16ToString(buffer[:count]))
			if normalizedReadingPath(actual) != normalizedReadingPath(materialsWindowsWatchPlainPath(path)) {
				return fmt.Errorf("%w: 观察句柄路径已改变", errMaterialsWindowsWatchRedirect)
			}
			return materialsWindowsWatchPathSafe(path)
		}
		if count > 32768 {
			return errors.New("资料观察句柄路径过长")
		}
		buffer = make([]uint16, int(count)+1)
	}
}

func materialsWindowsWatchNativePath(path string) string {
	if len(path) >= 248 && filepath.IsAbs(path) && !strings.HasPrefix(path, `\\?\`) {
		return `\\?\` + path
	}
	return path
}

func materialsWindowsWatchPlainPath(path string) string {
	if len(path) >= 7 && strings.HasPrefix(path, `\\?\`) && path[5] == ':' {
		return path[4:]
	}
	return path
}

func materialsWindowsWatchSameIdentity(a, b syscall.ByHandleFileInformation) bool {
	return a.VolumeSerialNumber == b.VolumeSerialNumber && a.FileIndexHigh == b.FileIndexHigh && a.FileIndexLow == b.FileIndexLow
}

func materialsWindowsWatchContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && (relative == "." || filepath.IsLocal(relative))
}

func materialsWindowsWatchIgnoredPath(path string) bool {
	for _, segment := range strings.Split(filepath.ToSlash(path), "/") {
		if strings.EqualFold(segment, ".git") || materialsRefreshStagingName.MatchString(segment) {
			return true
		}
	}
	return false
}

func materialsWindowsWatchMissing(err error) bool {
	return errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) || errors.Is(err, syscall.ERROR_PATH_NOT_FOUND)
}

func materialsWindowsWatchLifecycleError(err error) bool {
	return materialsWindowsWatchMissing(err) || errors.Is(err, syscall.ERROR_ACCESS_DENIED) || errors.Is(err, syscall.ERROR_OPERATION_ABORTED) || errors.Is(err, errMaterialsWindowsWatchRedirect) || errors.Is(err, errMaterialsWindowsWatchNotDirectory)
}
