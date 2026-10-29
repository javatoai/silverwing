package documents

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type materialsDirectoryIdentity struct {
	path string
	info os.FileInfo
	pin  *os.File
}

func captureMaterialsDirectory(path string, optional bool) (materialsDirectoryIdentity, error) {
	identity := materialsDirectoryIdentity{path: path}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) && optional {
		return identity, nil
	}
	if err != nil {
		return identity, err
	}
	if !info.IsDir() || isUnsafeDirectory(info, path) || !sameResolvedPath(path) {
		return identity, fmt.Errorf("本地资料目录包含不安全的链接：%s", path)
	}
	// Windows FileInfo loads its file ID lazily. Capture it before opening
	// the nofollow directory handle, then keep that identity alive until close.
	if !os.SameFile(info, info) {
		return identity, errors.New("无法捕获本地资料目录身份")
	}
	pin, err := openLocalMaterialsDirectory(path)
	if err != nil {
		return identity, err
	}
	opened, err := pin.Stat()
	if err != nil || !os.SameFile(info, opened) {
		_ = pin.Close()
		return identity, errors.New("捕获本地资料目录身份时目录发生了变化")
	}
	identity.info, identity.pin = opened, pin
	return identity, nil
}

func (d *materialsDirectoryIdentity) close() {
	if d.pin != nil {
		_ = d.pin.Close()
		d.pin = nil
	}
}

func (d materialsDirectoryIdentity) check() error {
	info, err := os.Lstat(d.path)
	if d.info == nil && os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if d.info == nil {
		return errors.New("本地资料目录在操作期间出现，请刷新后重试")
	}
	if !info.IsDir() || isUnsafeDirectory(info, d.path) || !sameResolvedPath(d.path) || !os.SameFile(d.info, info) {
		return errors.New("本地资料目录身份已变化或包含链接，请刷新后重试")
	}
	return nil
}

// A scope is private to one IO operation. Public paths and draft identities
// retain their configured spelling; all filesystem IO uses the pinned real
// subtree. The ordinary path validators remain strict on that real subtree.
type materialsOperationScope struct {
	publicRoot  string
	root        string
	local       *MaterialsScope
	task        string
	realTask    string
	directories []materialsDirectoryIdentity
}

func newMaterialsOperationScope(root string) (*materialsOperationScope, error) {
	public, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	public = filepath.Clean(public)
	if hasGitSegment(public) {
		return nil, errors.New("不能在 Git 内部目录管理任务资料")
	}
	scope := &materialsOperationScope{publicRoot: public}
	ready := false
	defer func() {
		if !ready {
			scope.close()
		}
	}()
	local, ok := LocalScope(filepath.Join(public, LocalMaterialsSubdirectory))
	if !ok {
		scope.root, err = validatedMaterialsRoot(public)
		ready = err == nil
		return scope, err
	}
	realTask, err := validatedLocalTaskPath(local.Base)
	if err != nil {
		return nil, err
	}
	real := LocalTaskMaterials(realTask)
	if _, err := validatedMaterialsRoot(real.Root); err != nil {
		return nil, err
	}
	scope.local, scope.task, scope.realTask, scope.root = &local, local.Base, realTask, real.Root
	for _, path := range []string{real.Base, filepath.Dir(real.Root), real.Root, real.Owned, real.Temporary} {
		identity, err := captureMaterialsDirectory(path, path == real.Owned || path == real.Temporary)
		if err != nil {
			return nil, err
		}
		scope.directories = append(scope.directories, identity)
	}
	if err := scope.revalidate(); err != nil {
		return nil, err
	}
	ready = true
	return scope, nil
}

func (s *materialsOperationScope) close() {
	if s == nil {
		return
	}
	for i := range s.directories {
		s.directories[i].close()
	}
}

func (s *materialsOperationScope) revalidate() error {
	if s == nil || s.local == nil {
		return nil
	}
	current, err := validatedLocalTaskPath(s.task)
	if err != nil {
		return err
	}
	if !samePathName(current, s.realTask) {
		return errors.New("本地资料所属任务的实际目录已变化，请刷新后重试")
	}
	for _, expected := range s.directories {
		if err := expected.check(); err != nil {
			return err
		}
	}
	return nil
}

func (s *materialsOperationScope) relative(path string) (string, error) {
	if s.local != nil {
		for _, part := range strings.Split(filepath.ToSlash(filepath.FromSlash(path)), "/") {
			if part == ".." {
				return "", errors.New("资料路径不能包含上级目录")
			}
		}
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.publicRoot, filepath.FromSlash(path))
	}
	relative, err := filepath.Rel(s.publicRoot, filepath.Clean(path))
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || hasGitSegment(path) {
		return "", errors.New("文件路径超出资料目录")
	}
	return relative, nil
}

func (s *materialsOperationScope) resolve(path string, missing, write, allowRoot bool) (string, error) {
	relative, err := s.relative(path)
	if err != nil {
		return "", err
	}
	if s.local != nil {
		if !s.local.Readable(relative) || write && !s.local.Writable(relative, allowRoot) {
			return "", errors.New("该资料属于只读或系统管理区域")
		}
	}
	if err := s.revalidate(); err != nil {
		return "", err
	}
	return safePath(s.root, relative, missing)
}

func (s *materialsOperationScope) importSource(path string) (string, error) {
	if s.local == nil || !filepath.IsAbs(path) {
		return path, nil
	}
	if pathWithin(s.publicRoot, path) {
		return s.resolve(path, false, false, false)
	}
	if !samePathName(s.publicRoot, s.root) && pathWithin(s.root, path) {
		return "", errors.New("不能用实际目录路径绕过当前资料路径范围")
	}
	return path, nil // External import sources retain their separate strict checks.
}

func (s *materialsOperationScope) public(path string) string {
	if path == "" || s.local == nil || !pathWithin(s.root, path) {
		return path
	}
	relative, _ := filepath.Rel(s.root, path)
	return filepath.Join(s.publicRoot, relative)
}

func (s *materialsOperationScope) revalidateDestination(root, directory, parent string) error {
	if err := s.revalidate(); err != nil {
		return err
	}
	return revalidateMaterialsDestination(root, directory, parent)
}

func (s *materialsOperationScope) copyNew(source, destination string) error {
	infoPath, info, err := validateImportSource(source)
	if err != nil {
		return err
	}
	parent := filepath.Dir(destination)
	if info.IsDir() {
		if pathWithin(infoPath, parent) {
			return errors.New("不能把文件夹导入到自身目录内")
		}
		if err := validateImportTree(infoPath); err != nil {
			return err
		}
		return copyDirectoryStaged(context.Background(), s.root, parent, infoPath, destination, info, s)
	}
	return copyFileStaged(context.Background(), s.root, relativeMaterials(s.root, parent), parent, infoPath, destination, info.Mode().Perm(), s)
}

func (s *materialsOperationScope) finish(err *error) bool {
	defer s.close()
	if failure := s.revalidate(); failure != nil {
		*err = errors.Join(*err, failure)
		return false
	}
	return true
}

func (s *materialsOperationScope) finishContent(content *Content, err *error) {
	if !s.finish(err) {
		*content = Content{}
		return
	}
	content.Path = s.public(content.Path)
}

func (s *materialsOperationScope) finishSnapshot(snapshot *MarkdownFileSnapshot, err *error) {
	if !s.finish(err) {
		*snapshot = MarkdownFileSnapshot{}
		return
	}
	snapshot.Path = s.public(snapshot.Path)
}

func (s *materialsOperationScope) finishPath(path *string, err *error) {
	if !s.finish(err) {
		*path = ""
		return
	}
	*path = s.public(*path)
}

func (s *materialsOperationScope) finishPaths(paths *[]string, err *error) {
	if !s.finish(err) {
		*paths = nil
		return
	}
	for i := range *paths {
		(*paths)[i] = s.public((*paths)[i])
	}
}

func (s *materialsOperationScope) projectEntries(entries []Entry) {
	for i := range entries {
		entries[i].Path = s.public(entries[i].Path)
		s.projectEntries(entries[i].Children)
	}
}
