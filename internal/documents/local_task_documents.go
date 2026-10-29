package documents

import (
	"errors"
	"path/filepath"
)

// LocalTaskDocumentScope is an operation-lifetime IO boundary for the two
// generated task documents. It does not grant ordinary materials operations
// access to task files, nor does it alter the configured or persisted paths.
type LocalTaskDocumentScope struct{ operation *materialsOperationScope }

func OpenLocalTaskDocumentScope(taskPath, ownedRoot string) (*LocalTaskDocumentScope, error) {
	local, ok := LocalScope(ownedRoot)
	if !ok || !filepath.IsAbs(taskPath) || !samePathName(local.Base, taskPath) {
		return nil, errors.New("本地资料与说明文件不属于同一任务路径")
	}
	operation, err := newMaterialsOperationScope(local.Root)
	if err != nil {
		return nil, err
	}
	return &LocalTaskDocumentScope{operation: operation}, nil
}

func (s *LocalTaskDocumentScope) Close() {
	if s != nil && s.operation != nil {
		s.operation.close()
		s.operation = nil
	}
}

func (s *LocalTaskDocumentScope) Check() error {
	if s == nil || s.operation == nil {
		return errors.New("本地任务说明操作已结束")
	}
	return s.operation.revalidate()
}

// WithDocument supplies a physical path only for these generated documents.
// The caller retains its existing content, file-lock and hard-link rules and
// must invoke check after its identity/cancellation callback before each write.
// A context-parent failure remains local to that document, so a safe router
// can still be refreshed without recreating a missing parent directory.
func (s *LocalTaskDocumentScope) WithDocument(relative string, operation func(path string, check func() error) error) (err error) {
	name := filepath.ToSlash(relative)
	if name != "AGENTS.md" && name != ".workspace/agent/TASK-CONTEXT.md" {
		return errors.New("本地任务说明路径超出系统生成文件范围")
	}
	if err := s.Check(); err != nil {
		return err
	}
	path := filepath.Join(s.operation.realTask, filepath.FromSlash(name))
	var parent *materialsDirectoryIdentity
	if name != "AGENTS.md" {
		identity, err := captureMaterialsDirectory(filepath.Dir(path), false)
		if err != nil {
			return err
		}
		parent = &identity
		defer parent.close()
	}
	check := func() error {
		if err := s.Check(); err != nil {
			return err
		}
		if parent != nil {
			if err := parent.check(); err != nil {
				return err
			}
		}
		_, err := safePath(s.operation.realTask, path, true)
		return err
	}
	if err := check(); err != nil {
		return err
	}
	defer func() { err = errors.Join(err, check()) }()
	return operation(path, check)
}
