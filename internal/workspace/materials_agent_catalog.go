package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

const materialsCatalogStart = "<!-- silverwing:materials:start -->"
const materialsCatalogEnd = "<!-- silverwing:materials:end -->"

func materialsCatalog(manifest domain.TaskManifest, planned bool) (string, error) {
	if manifest.RequirementMaterials.Status != domain.MaterialsReady || manifest.RequirementMaterials.WriteRoot == nil {
		return "", nil
	}
	scope, ok := documents.AnnualScope(*manifest.RequirementMaterials.WriteRoot)
	if !ok {
		return "", nil
	}
	content, err := scope.DirectoryInstructions(planned)
	if err != nil {
		return "", err
	}
	return materialsCatalogStart + "\n" + content + materialsCatalogEnd + "\n", nil
}

func replaceMaterialsCatalog(content, block string, allowAppend bool) (string, error) {
	starts, ends := strings.Count(content, materialsCatalogStart), strings.Count(content, materialsCatalogEnd)
	if starts == 0 && ends == 0 {
		if !allowAppend {
			return "", errors.New("自定义说明缺少安全的资料目录系统管理区，原文件已保留")
		}
		if block == "" {
			return content, nil
		}
		return strings.TrimRight(content, "\r\n") + "\n\n" + block, nil
	}
	start, end := strings.Index(content, materialsCatalogStart), strings.Index(content, materialsCatalogEnd)
	if starts != 1 || ends != 1 || end < start {
		return "", errors.New("资料目录系统管理区标记不完整或重复，原文件已保留")
	}
	end += len(materialsCatalogEnd)
	// Preserve every byte outside the managed region, including user notes.
	return content[:start] + strings.TrimSuffix(block, "\n") + content[end:], nil
}

func renderRootRouter(manifest domain.TaskManifest, paths storage.Paths) string {
	base := renderBaseRouter(manifest, paths)
	block, err := materialsCatalog(manifest, false)
	if err != nil {
		block, _ = materialsCatalog(manifest, true)
	}
	if block != "" {
		return base + "\n" + block
	}
	return base
}

// Refresh only the generated catalog. Caller holds the task operation lock;
// check revalidates home/config/task identity before each guarded file update.
// This path never creates a task or starts Git.
func (s *AgentDocumentService) RefreshMaterialsDirectories(taskPath string, manifest domain.TaskManifest, config domain.AppConfig, check func() error) error {
	block, err := materialsCatalog(manifest, false)
	if err != nil || block == "" {
		return err
	}
	var failures []error
	for _, path := range []string{filepath.Join(taskPath, AgentRouterFileName), filepath.Join(taskPath, AgentWorkspaceDir, AgentDirectoryName, AgentContextFileName)} {
		if check != nil {
			if err := check(); err != nil {
				return errors.Join(append(failures, err)...)
			}
		}
		if err := s.refreshMaterialsDirectoryFile(taskPath, path, manifest, block, check); err != nil {
			failures = append(failures, fmt.Errorf("%s：%w", path, err))
			var changed *materialsCatalogScopeError
			if errors.As(err, &changed) {
				return errors.Join(failures...)
			}
		}
	}
	return errors.Join(failures...)
}

// Per-document failures do not prevent a different, safely managed document
// from being refreshed. Scope failures still stop the whole task immediately.
func (s *AgentDocumentService) refreshMaterialsDirectoryFile(taskPath, path string, manifest domain.TaskManifest, block string, check func() error) error {
	if err := materialsGitSafePath(taskPath, path, false); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	missing := os.IsNotExist(err) && filepath.Base(path) == AgentRouterFileName
	if missing {
		// A missing generated file may be restored only in an existing,
		// identified task. Guarded writes never recreate its parent directory.
		fresh, loadErr := s.Manifests.Load(taskPath)
		if loadErr != nil {
			return &materialsCatalogScopeError{loadErr}
		}
		if fresh.TaskDirectoryName != manifest.TaskDirectoryName || fresh.CreatedAt != manifest.CreatedAt {
			return &materialsCatalogScopeError{errors.New("任务身份已变化，停止恢复说明")}
		}
		err = nil
	}
	if err != nil {
		return fmt.Errorf("读取资料目录说明：%w", err)
	}
	content := string(data)
	base := renderBaseRouter(manifest, s.Paths)
	if missing {
		content = base
	}
	allow := content == base || content == strings.Replace(base, taskScopeRule, legacyTaskScopeRule, 1)
	if filepath.Base(path) == AgentContextFileName {
		allow = strings.HasPrefix(content, "# 任务上下文\n\n> 本文件由系统生成")
	}
	next, err := replaceMaterialsCatalog(content, block, allow)
	if err != nil || next == content {
		return err
	}
	if err := checkMaterialsCatalogScope(check); err != nil {
		return err
	}
	if err := materialsGitSafePath(taskPath, path, false); err != nil {
		return err
	}
	return writeMaterialsCatalogFile(path, data, []byte(next), missing, check)
}

type materialsCatalogScopeError struct{ err error }

func (e *materialsCatalogScopeError) Error() string { return e.err.Error() }
func (e *materialsCatalogScopeError) Unwrap() error { return e.err }

func checkMaterialsCatalogScope(check func() error) error {
	if check != nil {
		if err := check(); err != nil {
			return &materialsCatalogScopeError{err}
		}
	}
	return nil
}

// These warnings belong to one application operation, never to the manifest.
func (s *AgentDocumentService) MaterialsDirectoryWarnings() error {
	s.catalogWarningsMu.Lock()
	defer s.catalogWarningsMu.Unlock()
	return errors.Join(s.catalogWarnings...)
}
