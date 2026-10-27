package documents

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"silverwing-go/internal/domain"
)

// Preview resolves the task materials write root without creating directories
// or acquiring the ensure/materialization lock.
func (s *PreparationService) Preview(ctx context.Context, config domain.AppConfig, requirementInput, folderName string) (domain.RequirementMaterialsDirectory, error) {
	fail := func(reason string) (domain.RequirementMaterialsDirectory, error) {
		return domain.RequirementMaterialsDirectory{Status: domain.MaterialsFailed, FailureReason: &reason}, nil
	}
	if s == nil {
		return domain.RequirementMaterialsDirectory{}, fmt.Errorf("任务资料预览服务不可用")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return domain.RequirementMaterialsDirectory{}, err
	}
	input := trimRequirementInput(requirementInput)
	id, link, err := parseMaterialsInput(input)
	if err != nil {
		return fail(err.Error())
	}
	root, err := requireMaterialsRoot(config.RequirementMaterialsRoot)
	if err != nil {
		return fail(err.Error())
	}
	subdirectory, err := requireMaterialsSubdirectory(config.RequirementMaterialsSubdirectory)
	if err != nil {
		return fail(err.Error())
	}
	folder := strings.TrimSpace(folderName)
	if err := validateMaterialsSegment(folder, "需求文件夹名称", 80); err != nil {
		return fail(err.Error())
	}
	requirementDirectory, err := buildRequirementDirectory(root, id, folder, materialsCreationYear(ctx))
	if err != nil {
		return fail(err.Error())
	}
	if err := ctx.Err(); err != nil {
		return domain.RequirementMaterialsDirectory{}, err
	}

	files := s.files
	if files == nil {
		files = &preparationFileStore{}
	}
	exists, err := files.exists(requirementDirectory)
	if err != nil {
		return fail("任务资料目录预检失败：" + err.Error())
	}
	if exists {
		if err := files.assertDirectory(requirementDirectory); err != nil {
			return fail("任务资料目录预检失败：" + err.Error())
		}
		var identity *RequirementIdentity
		if link != nil {
			identity = &RequirementIdentity{Space: link.Space, Kind: link.Kind, WorkItemID: link.WorkItemID}
		}
		if err := validateExistingRequirementDirectoryInput(requirementDirectory, subdirectory, id, identity, files); err != nil {
			return fail("任务资料目录预检失败：" + err.Error())
		}
		if err := ctx.Err(); err != nil {
			return domain.RequirementMaterialsDirectory{}, err
		}
		writeRoot := filepath.Join(requirementDirectory, subdirectory)
		return domain.RequirementMaterialsDirectory{Status: domain.MaterialsReady, WriteRoot: &writeRoot}, nil
	}

	project, err := s.resolveRequirementProject(ctx, config, id, link)
	if cancelErr := ctx.Err(); cancelErr != nil {
		return domain.RequirementMaterialsDirectory{}, cancelErr
	}
	if err != nil {
		return fail("任务资料目录预检失败：" + err.Error())
	}
	if project == nil {
		if len(config.MeegleProjects) == 0 {
			return fail("未配置 Meegle 项目")
		}
		return fail("未能唯一匹配需求所属的 Meegle 项目")
	}
	if err := ctx.Err(); err != nil {
		return domain.RequirementMaterialsDirectory{}, err
	}
	writeRoot := filepath.Join(requirementDirectory, subdirectory)
	return domain.RequirementMaterialsDirectory{Status: domain.MaterialsReady, WriteRoot: &writeRoot}, nil
}
