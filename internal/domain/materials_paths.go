package domain

import (
	"errors"
	"path/filepath"
	"strings"
)

const RequirementsDirectory = "requirements"

// MaterialsPaths separates the repository root from the annual requirements
// collection. Both supported picker choices resolve to the same pair.
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
	requirements := filepath.Join(root, RequirementsDirectory)
	if strings.EqualFold(filepath.Base(root), RequirementsDirectory) {
		requirements, root = root, filepath.Dir(root)
		if strings.EqualFold(filepath.Base(root), RequirementsDirectory) {
			return MaterialsPaths{}, errors.New("请选择最外层 requirements 目录或其总根目录")
		}
	}
	if filepath.Dir(root) == root {
		return MaterialsPaths{}, errors.New("任务资料总根目录不能是磁盘根目录")
	}
	return MaterialsPaths{Root: root, Requirements: requirements}, nil
}

func (c AppConfig) MaterialsPaths() (MaterialsPaths, error) {
	if c.RequirementMaterialsRoot == nil {
		return ResolveMaterialsPaths("")
	}
	return ResolveMaterialsPaths(*c.RequirementMaterialsRoot)
}
