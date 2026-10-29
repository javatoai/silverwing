package desktop

import (
	"path/filepath"

	"silverwing-go/internal/application"

	"silverwing-go/internal/documents"
)

// Legacy fixtures predate Snapshot.Task.Materials. Keep their path-layout
// compatibility in this single adapter. A nonempty Kind is always authoritative,
// including an explicit NONE descriptor that denies writes.
func (s *app) legacyMaterialsScope() (documents.AnnualMaterialsScope, bool) {
	if scope, ok := documents.LocalScope(filepath.Join(s.taskUnit.currentTask().materialsPath, documents.LocalMaterialsSubdirectory)); ok {
		return scope, filepath.Clean(scope.Base) == filepath.Clean(s.taskUnit.currentTask().path)
	}
	config, err := s.readCurrentAppConfig()
	if err != nil || config.RequirementMaterialsSubdirectory == nil {
		return documents.AnnualMaterialsScope{}, false
	}
	root := s.taskUnit.currentTask().materialsPath
	scope, ok := documents.AnnualScope(filepath.Join(root, *config.RequirementMaterialsSubdirectory))
	return scope, ok && scope.Root == filepath.Clean(root)
}

func (s *app) materialsWritable(path string, allowRoot bool) bool {
	if permissions := s.taskUnit.currentTask().materials; permissions.Kind != "" {
		return s.materialsUnit.canWrite(permissions, path, allowRoot)
	}
	if scope, ok := s.legacyMaterialsScope(); ok {
		return scope.Writable(path, allowRoot)
	}
	if scope, ok := documents.AnnualScope(filepath.Join(s.taskUnit.currentTask().materialsPath, "__scope__")); ok && scope.Root == filepath.Clean(s.taskUnit.currentTask().materialsPath) {
		return false
	}
	return true
}

func (s *app) materialsDefaultDirectory() string {
	if permissions := s.taskUnit.currentTask().materials; permissions.Kind != "" {
		return s.materialsUnit.defaultDirectory(permissions)
	}
	if scope, ok := s.legacyMaterialsScope(); ok {
		return scope.Owned
	}
	return s.taskUnit.currentTask().materialsPath
}

func (s *app) requireMaterialsWritable(path string, allowRoot bool) bool {
	if s.materialsWritable(path, allowRoot) {
		return true
	}
	s.lastError = "该资料目录只允许读取和复制，请在正式资料目录或 temp 中操作。"
	return false
}

func (s *materialsController) canWrite(permissions application.MaterialsPermissions, path string, allowRoot bool) bool {
	return permissions.Writable(path, allowRoot)
}

func (s *materialsController) defaultDirectory(permissions application.MaterialsPermissions) string {
	if permissions.OwnedRoot != "" {
		return permissions.OwnedRoot
	}
	return permissions.ReadRoot
}
