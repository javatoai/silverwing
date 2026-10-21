package documents

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"silverwing-go/internal/commandruntime"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/executable"
	"silverwing-go/internal/process"
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

type PreparationCommandRunner interface {
	Run(context.Context, process.Spec) (process.Result, error)
}

type PreparationService struct {
	paths  storage.Paths
	runner PreparationCommandRunner
	files  *preparationFileStore
}

func NewPreparationService(paths storage.Paths, runner PreparationCommandRunner) *PreparationService {
	if runner == nil {
		runner = process.NewRunner()
	}
	return &PreparationService{paths: paths, runner: runner, files: &preparationFileStore{}}
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
	requirementDirectory, err := buildRequirementDirectory(root, id, folder)
	if err != nil {
		return fail(err.Error())
	}
	release, err := s.acquireMaterialsLock(root)
	if err != nil {
		return fail("任务资料目录正在被另一个 silverwing 操作修改：" + root)
	}
	defer release()
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
		return domain.RequirementMaterialsDirectory{Status: domain.MaterialsReady, WriteRoot: &writeRoot}, nil
	}
	project, err := s.resolveRequirementProject(ctx, config, id, link)
	if ctx.Err() != nil {
		return domain.RequirementMaterialsDirectory{}, ctx.Err()
	}
	if err != nil {
		return fail(err.Error())
	}
	if project == nil {
		if len(config.MeegleProjects) == 0 {
			return fail("未配置 Meegle 项目")
		}
		return fail("未能唯一匹配需求所属的 Meegle 项目")
	}
	writeRoot := filepath.Join(requirementDirectory, subdirectory)
	if err := ensureMaterialsDirectory(root, writeRoot); err != nil {
		return fail("任务资料目录创建失败：" + err.Error())
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

func (s *PreparationService) resolveRequirementProject(ctx context.Context, config domain.AppConfig, id string, link *domain.FeishuWorkItemLink) (*domain.MeegleProjectConfig, error) {
	candidates := make([]domain.MeegleProjectConfig, 0, len(config.MeegleProjects))
	for _, project := range config.MeegleProjects {
		if link == nil || strings.EqualFold(project.SimpleName, link.Space) {
			candidates = append(candidates, project)
		}
	}
	if len(candidates) == 0 {
		if link != nil {
			return nil, fmt.Errorf("需求链接所属的飞书项目未配置：%s", link.Space)
		}
		return nil, nil
	}
	timeout := 20 * time.Second
	var matches []domain.MeegleProjectConfig
	for _, project := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result, err := s.runMeegle(ctx, config, []string{"workitem", "get", "--project-key", project.ProjectKey, "--work-item-id", id, "--format", "json"}, timeout)
		if err != nil {
			return nil, err
		}
		if !result.Successful() {
			message := strings.TrimSpace(result.Stderr)
			if message == "" {
				message = strings.TrimSpace(result.Stdout)
			}
			if missingRequirement(message) {
				continue
			}
			if runes := []rune(message); len(runes) > 300 {
				message = string(runes[:300])
			}
			message = commandruntime.Redact(config, message)
			return nil, fmt.Errorf("Meegle CLI 执行失败：%s", message)
		}
		var raw any
		decoder := json.NewDecoder(strings.NewReader(result.Stdout))
		decoder.UseNumber()
		if err := decoder.Decode(&raw); err != nil {
			return nil, fmt.Errorf("Meegle CLI 返回不是可解析的 JSON")
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return nil, fmt.Errorf("Meegle CLI 返回不是可解析的 JSON")
		}
		if containsWorkItemID(raw, id) {
			matches = append(matches, project)
		}
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("同一需求编号匹配到多个 Meegle 项目")
	}
	if len(matches) == 1 {
		return &matches[0], nil
	}
	return nil, nil
}

func containsWorkItemID(value any, id string) bool {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), " ", ""))
			if normalized == "itemid" || normalized == "workitemid" {
				if materialsTypedText(child) == id {
					return true
				}
			}
			if containsWorkItemID(child, id) {
				return true
			}
		}
	case []any:
		for _, child := range current {
			if containsWorkItemID(child, id) {
				return true
			}
		}
	}
	return false
}

func materialsTypedText(value any) string {
	switch current := value.(type) {
	case string:
		return strings.TrimSpace(current)
	case json.Number:
		return current.String()
	case float64:
		return strings.TrimSpace(fmt.Sprint(current))
	case bool:
		return fmt.Sprint(current)
	case map[string]any:
		for _, key := range []string{"long_value", "string_value", "text_value", "bool_value", "label"} {
			if child, ok := current[key]; ok {
				if result := materialsTypedText(child); result != "" {
					return result
				}
			}
		}
		if values, ok := current["key_label_value_list"].([]any); ok && len(values) > 0 {
			return materialsTypedText(values[0])
		}
	}
	return ""
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
	if _, err := buildRequirementDirectory(root, identity.WorkItemID, folder); err != nil {
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
	existing, err := resolveExistingRequirementDirectory(root, identity.WorkItemID, folder, subdirectory, identity, s.files)
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
		projectKey, err := resolveMeegleProjectKey(config, *link)
		if err != nil {
			return DocumentationPlan{}, err
		}
		title = s.fetchRequirementTitle(ctx, config, requirementLink, projectKey)
		if title == "" {
			return DocumentationPlan{}, fmt.Errorf("需要提供需求中文简写，或确保本地 Meegle 能读取需求标题")
		}
	}
	if strings.TrimSpace(title) == "" {
		return DocumentationPlan{}, fmt.Errorf("需求标题不能为空")
	}
	requirementDirectory, _ := buildRequirementDirectory(root, identity.WorkItemID, folder)
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
	expectedRequirement, err := buildRequirementDirectory(root, plan.Identity.WorkItemID, plan.DirectoryFolderName)
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
	if providedRequirement != expectedRequirement {
		return Materialization{}, fmt.Errorf("需求目录必须符合当前资料目录规则：%s", expectedRequirement)
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
	existing, err := resolveExistingRequirementDirectory(root, plan.Identity.WorkItemID, plan.DirectoryFolderName, subdirectory, plan.Identity, s.files)
	if err != nil {
		return Materialization{}, err
	}
	context := requirementDirectoryContext{RequirementDirectory: providedRequirement, WriteRoot: providedWriteRoot}
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
	manifestPath := filepath.Join(context.WriteRoot, requirementManifestName)
	if err := s.files.write(manifestPath, s.files.encodeRequirement(manifest)); err != nil {
		return Materialization{}, err
	}
	overviewPath := filepath.Join(context.WriteRoot, requirementOverviewName)
	if exists, err := s.files.exists(overviewPath); err != nil {
		return Materialization{}, err
	} else if !exists {
		if err := s.files.write(overviewPath, renderRequirementOverview(manifest)); err != nil {
			return Materialization{}, err
		}
	}
	if err := s.files.updateIndex(root, context.RequirementDirectory, manifest); err != nil {
		return Materialization{}, err
	}
	created := !exists
	actualPlan := plan
	actualPlan.RequirementTitle = title
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

func (s *PreparationService) fetchRequirementTitle(ctx context.Context, config domain.AppConfig, requirementLink, projectKey string) string {
	if s.runner == nil {
		return ""
	}
	link := domain.ParseFeishuWorkItemLink(requirementLink)
	if link == nil {
		return ""
	}
	result, err := s.runMeegle(ctx, config, []string{"workitem", "get", "--project-key", projectKey, "--work-item-id", link.WorkItemID, "--format", "json"}, 8*time.Second)
	if err != nil || !result.Successful() {
		return ""
	}
	var root map[string]json.RawMessage
	if json.Unmarshal([]byte(result.Stdout), &root) != nil {
		return ""
	}
	var attributes map[string]json.RawMessage
	_ = json.Unmarshal(root["work_item_attribute"], &attributes)
	for _, candidate := range []json.RawMessage{root["name"], root["work_item_name"], attributes["name"], attributes["work_item_name"]} {
		if title := jsonPrimitiveText(candidate); title != "" {
			return title
		}
	}
	return ""
}

func resolveMeegleProjectKey(config domain.AppConfig, link domain.FeishuWorkItemLink) (string, error) {
	for _, project := range config.MeegleProjects {
		if strings.EqualFold(project.SimpleName, link.Space) {
			return project.ProjectKey, nil
		}
	}
	if link.ProjectKey != nil {
		return *link.ProjectKey, nil
	}
	return "", fmt.Errorf("需求空间 %s 未配置 Meegle project key", link.Space)
}

func jsonPrimitiveText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return strings.TrimSpace(value)
	}
	var primitive any
	if json.Unmarshal(raw, &primitive) != nil {
		return ""
	}
	switch value := primitive.(type) {
	case float64:
		return strings.TrimSpace(fmt.Sprint(value))
	case bool:
		return strings.TrimSpace(fmt.Sprint(value))
	default:
		return ""
	}
}

// Both project verification and optional title lookup share the desktop CLI
// runtime. A reused directory deliberately requires neither remote lookup.
func (s *PreparationService) runMeegle(ctx context.Context, config domain.AppConfig, args []string, timeout time.Duration) (process.Result, error) {
	configured := ""
	if config.MeegleExecutablePath != nil {
		configured = strings.TrimSpace(*config.MeegleExecutablePath)
	}
	spec, err := commandruntime.Prepare(ctx, s.runner, &config, domain.ProxyTargetMeegle, executable.ToolMeegle, configured)
	if err != nil {
		return process.Result{}, err
	}
	spec.Arguments, spec.Timeout = args, timeout
	result, err := s.runner.Run(ctx, spec)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err != nil {
		return result, fmt.Errorf("Meegle CLI 执行失败：%s", commandruntime.Redact(config, err.Error()))
	}
	return result, nil
}

func missingRequirement(message string) bool {
	lower := strings.ToLower(message)
	if strings.Contains(lower, "node") || strings.Contains(lower, "env:") || strings.Contains(lower, "command not found") || strings.Contains(lower, "no such file") {
		return false
	}
	explicitItem := strings.Contains(lower, "workitem") || strings.Contains(lower, "work item") || strings.Contains(lower, "work-item") || strings.Contains(message, "需求") || strings.Contains(message, "工作项")
	return strings.Contains(lower, "404") || explicitItem && (strings.Contains(lower, "not found") || strings.Contains(lower, "not exist") || strings.Contains(message, "不存在"))
}
