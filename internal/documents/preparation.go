package documents

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

const (
	documentationFormatVersion = 2
	requirementManifestName    = ".silverwing-requirement.json"
	requirementIndexName       = ".silverwing-requirement-index.jsonl"
	requirementOverviewName    = "00-需求总览.md"
	requirementMaterialsLock   = ".silverwing-requirement-materials.lock"
)

type RequirementIdentity struct {
	Space      string `json:"space"`
	Kind       string `json:"kind"`
	WorkItemID string `json:"workItemId"`
}

func (i RequirementIdentity) StableKey() string { return i.Space + "/" + i.Kind + "/" + i.WorkItemID }

func (i RequirementIdentity) Validate() error {
	if !requirementSpacePattern.MatchString(i.Space) {
		return fmt.Errorf("需求空间不合法")
	}
	switch i.Kind {
	case "userstory", "technical", "bug", "othertask":
	default:
		return fmt.Errorf("需求类型不合法")
	}
	if !requirementIDPattern.MatchString(i.WorkItemID) {
		return fmt.Errorf("需求 ID 不合法")
	}
	return nil
}

type DocumentationPlan struct {
	FormatVersion           int                 `json:"formatVersion"`
	Identity                RequirementIdentity `json:"identity"`
	RequirementTitle        string              `json:"requirementTitle"`
	RequirementDirectory    string              `json:"requirementDirectory"`
	DocumentationDirectory  string              `json:"documentationDirectory"`
	DirectoryFolderName     string              `json:"directoryFolderName"`
	ReusedExistingDirectory bool                `json:"reusedExistingDirectory"`
}

func (p DocumentationPlan) Validate() error {
	if p.FormatVersion != documentationFormatVersion {
		return fmt.Errorf("不支持的任务资料计划格式：%d", p.FormatVersion)
	}
	if err := p.Identity.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(p.RequirementTitle) == "" {
		return fmt.Errorf("需求标题不能为空")
	}
	if strings.TrimSpace(p.DirectoryFolderName) == "" {
		return fmt.Errorf("需求文件夹名称不能为空")
	}
	return nil
}

type Materialization struct {
	Plan                        DocumentationPlan       `json:"plan"`
	AgentContext                domain.AgentTaskContext `json:"agentContext"`
	CreatedRequirementDirectory bool                    `json:"createdRequirementDirectory"`
}

type PreparationService struct {
	paths    storage.Paths
	provider RequirementProvider
	files    *preparationFileStore
}

func NewPreparationService(paths storage.Paths, provider RequirementProvider) *PreparationService {
	return &PreparationService{paths: paths, provider: provider, files: &preparationFileStore{}}
}

func (s *PreparationService) resolveRequirement(ctx context.Context, config domain.AppConfig, id string, link *domain.FeishuWorkItemLink) (RequirementIdentity, error) {
	if s.provider == nil {
		return RequirementIdentity{}, fmt.Errorf("任务资料来源服务未配置")
	}
	identity, err := s.provider.Resolve(ctx, config, requirementReference(id, link))
	if err != nil {
		return RequirementIdentity{}, err
	}
	if identity.WorkItemID != id || link != nil && (!strings.EqualFold(identity.Space, link.Space) || !strings.EqualFold(identity.Kind, link.Kind)) {
		return RequirementIdentity{}, fmt.Errorf("需求来源返回的身份与当前需求不一致")
	}
	return identity, nil
}

// Ensure provisions only the configured requirement write directory. It does
// not create the Agent handoff file, documentation manifest, or overview.
// Lookup and filesystem failures are returned as a source-compatible FAILED
// result so desktop task creation can continue without materials.
func (s *PreparationService) Ensure(ctx context.Context, config domain.AppConfig, requirementInput, folderName string) (domain.RequirementMaterialsDirectory, error) {
	fail := func(reason string) (domain.RequirementMaterialsDirectory, error) {
		return domain.RequirementMaterialsDirectory{Status: domain.MaterialsFailed, FailureReason: &reason}, nil
	}
	if ctx == nil {
		ctx = context.Background()
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
	release, err := s.acquireMaterialsLock(root)
	if err != nil {
		return fail("任务资料目录正在被另一个 silverwing 操作修改：" + root)
	}
	defer release()
	// Resolve again while holding the shared lock: another task may have
	// created this requirement (possibly under a different name) meanwhile.
	requirementDirectory, err = buildRequirementDirectory(root, id, folder, materialsCreationYear(ctx))
	if err != nil {
		return fail(err.Error())
	}
	if err := ctx.Err(); err != nil {
		return domain.RequirementMaterialsDirectory{}, err
	}
	exists, err := s.files.exists(requirementDirectory)
	if err != nil {
		return fail("任务资料目录预检失败：" + err.Error())
	}
	if exists {
		if err := s.files.assertDirectory(requirementDirectory); err != nil {
			return fail("任务资料目录预检失败：" + err.Error())
		}
		var identity *RequirementIdentity
		if link != nil {
			identity = &RequirementIdentity{Space: link.Space, Kind: link.Kind, WorkItemID: link.WorkItemID}
		}
		if err := validateExistingRequirementDirectoryInput(requirementDirectory, subdirectory, id, identity, s.files); err != nil {
			return fail("任务资料目录预检失败：" + err.Error())
		}
		writeRoot := filepath.Join(requirementDirectory, subdirectory)
		if err := ensureMaterialsDirectory(root, writeRoot); err != nil {
			return fail("任务资料目录创建失败：" + err.Error())
		}
		if scope, ok := AnnualScope(writeRoot); ok {
			if err := scope.EnsureLocal(); err != nil {
				return fail(err.Error())
			}
		}
		return domain.RequirementMaterialsDirectory{Status: domain.MaterialsReady, WriteRoot: &writeRoot}, nil
	}
	identity, err := s.resolveRequirement(ctx, config, id, link)
	if ctx.Err() != nil {
		return domain.RequirementMaterialsDirectory{}, ctx.Err()
	}
	if err != nil {
		return fail(err.Error())
	}
	writeRoot := filepath.Join(requirementDirectory, subdirectory)
	if err := ensureMaterialsDirectory(root, writeRoot); err != nil {
		return fail("任务资料目录创建失败：" + err.Error())
	}
	if scope, ok := AnnualScope(writeRoot); ok {
		if err := scope.EnsureLocal(); err != nil {
			return fail(err.Error())
		}
	}
	if link != nil {
		now := domain.FormatSilverWingTime(time.Now())
		metadata := RequirementDocumentationManifest{FormatVersion: documentationFormatVersion, Identity: identity, RequirementTitle: folder, DirectoryName: filepath.Base(requirementDirectory), CreatedAt: now, UpdatedAt: now}
		if err := s.files.write(filepath.Join(writeRoot, requirementManifestName), s.files.encodeRequirement(metadata)); err != nil {
			return fail(err.Error())
		}
	}
	return domain.RequirementMaterialsDirectory{Status: domain.MaterialsReady, WriteRoot: &writeRoot}, nil
}

var numericRequirementPattern = regexp.MustCompile(`^[0-9]+$`)

func trimRequirementInput(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), ",，。;；")
}

func parseMaterialsInput(value string) (string, *domain.FeishuWorkItemLink, error) {
	if numericRequirementPattern.MatchString(value) {
		return value, nil, nil
	}
	link := domain.ParseFeishuWorkItemLink(value)
	if link == nil {
		return "", nil, fmt.Errorf("需求输入必须是数字需求编号或 Meegle 详情链接")
	}
	return link.WorkItemID, link, nil
}

// Plan resolves a local documentation target without creating the requirement directory.
// The source implementation creates the shared lock file while holding its lock.
func (s *PreparationService) Plan(ctx context.Context, config domain.AppConfig, requirementLink, requestedTitle, directoryFolderName string) (DocumentationPlan, error) {
	root, err := requireMaterialsRoot(config.RequirementMaterialsRoot)
	if err != nil {
		return DocumentationPlan{}, err
	}
	subdirectory, err := requireMaterialsSubdirectory(config.RequirementMaterialsSubdirectory)
	if err != nil {
		return DocumentationPlan{}, err
	}
	link := domain.ParseFeishuWorkItemLink(requirementLink)
	if link == nil {
		return DocumentationPlan{}, fmt.Errorf("需求链接不是支持的飞书项目工作项链接")
	}
	identity := RequirementIdentity{Space: link.Space, Kind: link.Kind, WorkItemID: link.WorkItemID}
	if err := identity.Validate(); err != nil {
		return DocumentationPlan{}, err
	}
	folder := strings.TrimSpace(directoryFolderName)
	if folder == "" {
		folder = strings.TrimSpace(requestedTitle)
	}
	if err := validateMaterialsSegment(folder, "需求文件夹名称", 80); err != nil {
		return DocumentationPlan{}, err
	}
	if _, err := buildRequirementDirectory(root, identity.WorkItemID, folder, materialsCreationYear(ctx)); err != nil {
		return DocumentationPlan{}, err
	}
	release, err := s.acquireMaterialsLock(root)
	if err != nil {
		return DocumentationPlan{}, err
	}
	defer release()
	if err := ctx.Err(); err != nil {
		return DocumentationPlan{}, err
	}
	existing, err := resolveExistingRequirementDirectory(root, identity.WorkItemID, folder, subdirectory, identity, s.files, materialsCreationYear(ctx))
	if err != nil {
		return DocumentationPlan{}, err
	}
	var existingManifest *RequirementDocumentationManifest
	if existing != nil {
		existingManifest, err = s.files.readManagedManifest(existing.WriteRoot, identity)
		if err != nil {
			return DocumentationPlan{}, err
		}
	}
	var title string
	if existingManifest != nil {
		title = existingManifest.RequirementTitle
	} else if requested := strings.TrimSpace(requestedTitle); requested != "" {
		title = requested
	} else if existing != nil {
		title = folder
	} else {
		if s.provider == nil {
			return DocumentationPlan{}, fmt.Errorf("任务资料来源服务未配置")
		}
		title, err = s.provider.Title(ctx, config, requirementReference(link.WorkItemID, link))
		if err != nil {
			return DocumentationPlan{}, err
		}
		if title == "" {
			return DocumentationPlan{}, fmt.Errorf("需要提供需求中文简写，或确保本地 Meegle 能读取需求标题")
		}
	}
	if strings.TrimSpace(title) == "" {
		return DocumentationPlan{}, fmt.Errorf("需求标题不能为空")
	}
	requirementDirectory, _ := buildRequirementDirectory(root, identity.WorkItemID, folder, materialsCreationYear(ctx))
	return DocumentationPlan{
		FormatVersion:           documentationFormatVersion,
		Identity:                identity,
		RequirementTitle:        title,
		RequirementDirectory:    requirementDirectory,
		DocumentationDirectory:  filepath.Join(requirementDirectory, subdirectory),
		DirectoryFolderName:     folder,
		ReusedExistingDirectory: existing != nil,
	}, nil
}

func (s *PreparationService) Materialize(ctx context.Context, config domain.AppConfig, plan DocumentationPlan) (Materialization, error) {
	if err := plan.Validate(); err != nil {
		return Materialization{}, err
	}
	root, err := requireMaterialsRoot(config.RequirementMaterialsRoot)
	if err != nil {
		return Materialization{}, err
	}
	subdirectory, err := requireMaterialsSubdirectory(config.RequirementMaterialsSubdirectory)
	if err != nil {
		return Materialization{}, err
	}
	if err := validateMaterialsSegment(plan.DirectoryFolderName, "需求文件夹名称", 80); err != nil {
		return Materialization{}, err
	}
	expectedRequirement, err := buildRequirementDirectory(root, plan.Identity.WorkItemID, plan.DirectoryFolderName, materialsCreationYear(ctx))
	if err != nil {
		return Materialization{}, err
	}
	providedRequirement, err := absoluteClean(plan.RequirementDirectory)
	if err != nil {
		return Materialization{}, err
	}
	providedWriteRoot, err := absoluteClean(plan.DocumentationDirectory)
	if err != nil {
		return Materialization{}, err
	}
	providedScope, annual := AnnualScope(providedWriteRoot)
	if !annual || providedScope.Base != root || providedScope.Root != providedRequirement || !strings.HasPrefix(filepath.Base(providedRequirement), plan.Identity.WorkItemID+"-") {
		return Materialization{}, fmt.Errorf("需求目录必须符合年度资料目录规则：%s", expectedRequirement)
	}
	if providedWriteRoot != filepath.Join(providedRequirement, subdirectory) {
		return Materialization{}, fmt.Errorf("任务资料写入目录必须是需求目录的当前资料子目录")
	}
	if !isWithin(root, providedRequirement) || !isWithin(root, providedWriteRoot) {
		return Materialization{}, fmt.Errorf("需求目录或写入目录超出配置的任务资料根目录")
	}
	release, err := s.acquireMaterialsLock(root)
	if err != nil {
		return Materialization{}, err
	}
	defer release()
	if err := ctx.Err(); err != nil {
		return Materialization{}, err
	}
	link := fmt.Sprintf("https://project.feishu.cn/%s/%s/detail/%s", plan.Identity.Space, plan.Identity.Kind, plan.Identity.WorkItemID)
	parsed := domain.ParseFeishuWorkItemLink(link)
	if parsed == nil {
		return Materialization{}, fmt.Errorf("需求身份不能生成有效的飞书项目工作项链接")
	}
	existing, err := resolveExistingRequirementDirectory(root, plan.Identity.WorkItemID, plan.DirectoryFolderName, subdirectory, plan.Identity, s.files, materialsCreationYear(ctx))
	if err != nil {
		return Materialization{}, err
	}
	context := requirementDirectoryContext{RequirementDirectory: expectedRequirement, WriteRoot: filepath.Join(expectedRequirement, subdirectory)}
	if existing != nil {
		context = *existing
	}
	exists, err := s.files.exists(context.RequirementDirectory)
	if err != nil {
		return Materialization{}, err
	}
	if exists {
		if err := s.files.assertDirectory(context.RequirementDirectory); err != nil {
			return Materialization{}, err
		}
	}
	previous, err := s.files.readManagedManifest(context.WriteRoot, plan.Identity)
	if err != nil {
		return Materialization{}, err
	}
	title := plan.RequirementTitle
	now := domain.FormatSilverWingTime(time.Now())
	createdAt := now
	if previous != nil {
		title = previous.RequirementTitle
		createdAt = previous.CreatedAt
	}
	manifest := RequirementDocumentationManifest{
		FormatVersion:    documentationFormatVersion,
		Identity:         plan.Identity,
		RequirementTitle: title,
		DirectoryName:    filepath.Base(context.RequirementDirectory),
		CreatedAt:        createdAt,
		UpdatedAt:        now,
	}
	if err := ensureMaterialsDirectory(root, context.RequirementDirectory); err != nil {
		return Materialization{}, err
	}
	if err := ensureMaterialsDirectory(root, context.WriteRoot); err != nil {
		return Materialization{}, err
	}
	if scope, ok := AnnualScope(context.WriteRoot); ok {
		if err := scope.EnsureLocal(); err != nil {
			return Materialization{}, err
		}
	}
	manifestPath := filepath.Join(context.WriteRoot, requirementManifestName)
	if err := s.files.write(manifestPath, s.files.encodeRequirement(manifest)); err != nil {
		return Materialization{}, err
	}
	created := !exists
	actualPlan := plan
	actualPlan.RequirementTitle = title
	actualPlan.RequirementDirectory = context.RequirementDirectory
	actualPlan.DocumentationDirectory = context.WriteRoot
	actualPlan.ReusedExistingDirectory = !created
	return Materialization{
		Plan:                        actualPlan,
		AgentContext:                domain.AgentTaskContext{ProtocolVersion: "2", DocumentationDirectory: context.WriteRoot, HandoffRelativePath: ".workspace/HANDOFF.md"},
		CreatedRequirementDirectory: created,
	}, nil
}

func (s *PreparationService) acquireMaterialsLock(root string) (func(), error) {
	return storage.AcquireLock(filepath.Join(root, requirementMaterialsLock))
}
