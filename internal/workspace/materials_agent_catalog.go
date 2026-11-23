package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
)

const materialsCatalogStart = "<!-- silverwing:materials:start -->"
const materialsCatalogEnd = "<!-- silverwing:materials:end -->"

func materialsCatalog(manifest domain.TaskManifest, planned bool) (string, error) {
	if manifest.RequirementMaterials.Status != domain.MaterialsReady || manifest.RequirementMaterials.WriteRoot == nil {
		return "", nil
	}
	scope, ok := documents.TaskMaterialsScope(*manifest.RequirementMaterials.WriteRoot)
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
	if err := validateTaskAgentsBlocks(content); err != nil {
		return "", err
	}
	start, end := strings.Index(content, materialsCatalogStart), strings.Index(content, materialsCatalogEnd)
	if start < 0 {
		if !allowAppend {
			return "", errors.New("自定义说明缺少安全的资料目录系统管理区，原文件已保留")
		}
		if block == "" {
			return content, nil
		}
		if strings.HasSuffix(content, "\n") {
			return content + "\n" + block, nil
		}
		return content + "\n\n" + block, nil
	}
	end += len(materialsCatalogEnd)
	// Preserve every byte outside the managed region, including user notes.
	return content[:start] + strings.TrimSuffix(block, "\n") + content[end:], nil
}

// Refresh only the generated catalog. Caller holds the task operation lock;
// check revalidates home/config/task identity before each guarded file update.
// This path never creates a task or starts Git.
func (s *AgentDocumentService) RefreshMaterialsDirectories(taskPath string, manifest domain.TaskManifest, config domain.AppConfig, check func() error) (resultErr error) {
	var local *documents.LocalTaskDocumentScope
	if manifest.RequirementMaterials.Status == domain.MaterialsReady && manifest.RequirementMaterials.WriteRoot != nil {
		if _, ok := documents.LocalScope(*manifest.RequirementMaterials.WriteRoot); ok {
			var err error
			local, err = documents.OpenLocalTaskDocumentScope(taskPath, *manifest.RequirementMaterials.WriteRoot)
			if err != nil {
				return err
			}
			defer local.Close()
			defer func() { resultErr = errors.Join(resultErr, local.Check()) }()
		}
	}
	block, err := materialsCatalog(manifest, false)
	if err != nil || block == "" {
		return err
	}
	if err := checkMaterialsCatalogScope(check); err != nil {
		return err
	}
	path := filepath.Join(taskPath, AgentRouterFileName)
	if local == nil {
		validate := func() error { return materialsGitSafePath(taskPath, path, false) }
		err = s.refreshMaterialsDirectoryFile(taskPath, path, manifest, config, block, check, validate)
	} else {
		err = local.WithDocument(AgentRouterFileName, func(physical string, validate func() error) error {
			guard := func() error {
				if err := checkMaterialsCatalogScope(check); err != nil {
					return err
				}
				return checkMaterialsCatalogScope(validate)
			}
			return s.refreshMaterialsDirectoryFile(taskPath, physical, manifest, config, block, guard, validate)
		})
	}
	if err != nil {
		return fmt.Errorf("%s：%w", path, err)
	}
	return nil
}

func (s *AgentDocumentService) refreshMaterialsDirectoryFile(taskPath, path string, manifest domain.TaskManifest, config domain.AppConfig, block string, check, validate func() error) error {
	if err := validate(); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	missing := os.IsNotExist(err)
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
	next := ""
	if missing {
		next = s.renderTaskAgents(manifest, config, block)
	} else {
		next, err = replaceMaterialsCatalog(content, block, strings.Contains(content, profileRouterStart))
		if err != nil || next == content {
			return err
		}
	}
	if err := checkMaterialsCatalogScope(check); err != nil {
		return err
	}
	if err := validate(); err != nil {
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
