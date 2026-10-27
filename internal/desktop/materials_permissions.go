package desktop

import (
	"path/filepath"

	"silverwing-go/internal/documents"
)

func (s *app) materialsAnnualScope() (documents.AnnualMaterialsScope, bool) {
	config, err := s.readCurrentAppConfig()
	if err != nil || config.RequirementMaterialsSubdirectory == nil {
		return documents.AnnualMaterialsScope{}, false
	}
	root := s.currentTask().materialsPath
	scope, ok := documents.AnnualScope(filepath.Join(root, *config.RequirementMaterialsSubdirectory))
	return scope, ok && scope.Root == filepath.Clean(root)
}

func (s *app) materialsWritable(path string, allowRoot bool) bool {
	if scope, ok := s.materialsAnnualScope(); ok {
		return scope.Writable(path, allowRoot)
	}
	if scope, ok := documents.AnnualScope(filepath.Join(s.currentTask().materialsPath, "__scope__")); ok && scope.Root == filepath.Clean(s.currentTask().materialsPath) {
		return false
	}
	return true
}

func (s *app) materialsDefaultDirectory() string {
	if scope, ok := s.materialsAnnualScope(); ok {
		return scope.Owned
	}
	return s.currentTask().materialsPath
}

func (s *app) requireMaterialsWritable(path string, allowRoot bool) bool {
	if s.materialsWritable(path, allowRoot) {
		return true
	}
	s.lastError = "该资料目录只允许读取和复制，请在正式资料目录或 temp 中操作。"
	return false
}
