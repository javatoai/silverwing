package domain

import (
	"errors"
	"path/filepath"
	"strings"
)

const RequirementsDirectory = "requirements"

// MaterialsPaths contains the configured repository root and its annual
// requirements collection.
type MaterialsPaths struct {
	Root         string
	Requirements string
}

func ResolveMaterialsPaths(configured string) (MaterialsPaths, error) {
	if strings.TrimSpace(configured) == "" {
		return MaterialsPaths{}, errors.New("请先配置任务资料根目录")
	}
	root, err := filepath.Abs(strings.TrimSpace(configured))
	if err != nil {
		return MaterialsPaths{}, err
	}
	root = filepath.Clean(root)
	if filepath.Dir(root) == root {
		return MaterialsPaths{}, errors.New("任务资料总根目录不能是磁盘根目录")
	}
	return MaterialsPaths{Root: root, Requirements: filepath.Join(root, RequirementsDirectory)}, nil
}

func (c AppConfig) MaterialsPaths() (MaterialsPaths, error) {
	if root := c.MaterialsGitSettings().LocalRoot; root != "" {
		return ResolveMaterialsPaths(root)
	}
	if c.RequirementMaterialsRoot == nil {
		return ResolveMaterialsPaths("")
	}
	return ResolveMaterialsPaths(*c.RequirementMaterialsRoot)
}
