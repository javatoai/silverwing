package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

const (
	AgentRouterFileName  = "AGENTS.md"
	AgentWorkspaceDir    = ".workspace"
	AgentDirectoryName   = "agent"
	AgentContextFileName = "TASK-CONTEXT.md"
	AgentRulesFileName   = "TASK-RULES.md"
	HandoffFileName      = "HANDOFF.md"
	legacyTaskScopeRule  = "只允许修改 `TASK-CONTEXT.md` 中“Worktree 范围”标明可改动的 Worktree。"
	taskScopeRule        = "修改范围以 `TASK-CONTEXT.md` 中的任务范围说明为准；未关联服务时可在任务目录处理分析和文档。"
)

type AgentDocumentPreviewFile struct {
	RelativePath string `json:"relativePath"`
	Content      string `json:"content"`
}

type AgentDocumentPreview struct {
	Files []AgentDocumentPreviewFile `json:"files"`
}

// TaskManifestReader is the only persistence operation used by generated task
// documents and their materials catalogues.
type TaskManifestReader interface {
	Load(string) (domain.TaskManifest, error)
}

// AgentDocumentService writes referenced task files for every manifest.
// INLINE_V1 is persisted historical metadata, not a file-generation mode.
type AgentDocumentService struct {
	Paths             storage.Paths
	Manifests         TaskManifestReader
	catalogWarningsMu sync.Mutex
	catalogWarnings   []error
}

func NewAgentDocumentService(paths storage.Paths, manifests TaskManifestReader) *AgentDocumentService {
	if manifests == nil {
		manifests = storage.NewManifestStore()
	}
	return &AgentDocumentService{Paths: paths, Manifests: manifests}
}

func (s *AgentDocumentService) EnsureGlobalFile() (string, error) {
	return ensureDocumentFile(s.Paths.GlobalAgents, "")
}

func (s *AgentDocumentService) EnsureGroupFile(groupID string) (string, error) {
	path, err := s.Paths.GroupAgents(groupID)
	if err != nil {
		return "", err
	}
	return ensureDocumentFile(path, "")
}

func (s *AgentDocumentService) ReadGlobal() (string, error) {
	return readDocument(s.Paths.GlobalAgents)
}

func (s *AgentDocumentService) SaveGlobal(content string) error {
	return writeAtomicDocument(s.Paths.GlobalAgents, content)
}

func (s *AgentDocumentService) ReadGroup(groupID string) (string, error) {
	path, err := s.Paths.GroupAgents(groupID)
	if err != nil {
		return "", err
	}
	return readDocument(path)
}

func (s *AgentDocumentService) SaveGroup(groupID, content string) error {
	path, err := s.Paths.GroupAgents(groupID)
	if err != nil {
		return err
	}
	return writeAtomicDocument(path, content)
}

// EnsureTaskDocuments initializes or refreshes generated files. User-authored
// TASK-RULES.md and existing root AGENTS.md are preserved unless notes are given.
func (s *AgentDocumentService) EnsureTaskDocuments(taskPath string, manifest domain.TaskManifest, config domain.AppConfig) (string, error) {
	return s.writeTaskDocuments(taskPath, manifest, repositoryInfos(config), nil, true)
}

func (s *AgentDocumentService) WriteTaskDocument(taskPath string, manifest domain.TaskManifest, config domain.AppConfig) (string, error) {
	return s.writeTaskDocuments(taskPath, manifest, repositoryInfos(config), nil, false)
}

func (s *AgentDocumentService) WriteTaskNotes(taskPath, notes string) error {
	manifest, err := s.Manifests.Load(taskPath)
	if err != nil {
		return err
	}
	path := s.TaskNotesPath(taskPath, manifest)
	current, err := readDocument(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	document, err := s.ReplaceTaskNotesDocument(manifest, current, notes)
	if err != nil {
		return err
	}
	return writeAtomicDocument(path, document)
}

func (s *AgentDocumentService) ReadTaskNotes(taskPath string) (string, error) {
	manifest, err := s.Manifests.Load(taskPath)
	if err != nil {
		return "", err
	}
	return s.ReadTaskNotesForManifest(taskPath, manifest)
}

func (s *AgentDocumentService) ReadTaskNotesForManifest(taskPath string, manifest domain.TaskManifest) (string, error) {
	document, err := readDocument(s.TaskNotesPath(taskPath, manifest))
	if err != nil {
		return "", err
	}
	return s.TaskNotesFromDocument(manifest, document)
}

// TaskNotesPath ignores historical layout metadata, matching Source's only
// editable task-rules location.
func (s *AgentDocumentService) TaskNotesPath(taskPath string, _ domain.TaskManifest) string {
	return s.taskNotesPath(taskPath)
}

func (s *AgentDocumentService) TaskNotesFile(taskPath string) string {
	return s.taskNotesPath(taskPath)
}

// Reading trims only CR/LF, as Source's taskNotesFromDocument does.
func (s *AgentDocumentService) TaskNotesFromDocument(_ domain.TaskManifest, document string) (string, error) {
	return strings.TrimRight(document, "\r\n"), nil
}

// Saving task rules uses Kotlin/JVM trimEnd() semantics, including Unicode
// space separators and information separators, but excluding NEL (U+0085).
func (s *AgentDocumentService) ReplaceTaskNotesDocument(_ domain.TaskManifest, _, notes string) (string, error) {
	return trimTaskNotesEnd(notes), nil
}

func trimTaskNotesEnd(notes string) string {
	return strings.TrimRightFunc(notes, func(r rune) bool {
		return r != '\u0085' && (unicode.IsSpace(r) || r >= '\u001c' && r <= '\u001f')
	})
}

func (s *AgentDocumentService) WriteHandoff(taskPath, content string) (string, error) {
	path := filepath.Join(taskPath, AgentWorkspaceDir, HandoffFileName)
	if strings.TrimSpace(content) == "" {
		content = HandoffTemplate()
	}
	content = SanitizeHandoffMarkdown(content)
	return path, writeAtomicDocument(path, strings.TrimRight(content, "\r\n")+"\n")
}

func HandoffTemplate() string {
	return "# 任务交接\n\n## 目标\n\n- 待补充。\n\n## 范围与边界\n\n- 待补充。\n\n## 已验证事实\n\n- 待补充。\n\n## 风险与未决项\n\n- 待补充。\n\n## 建议下一步\n\n- 待补充。\n"
}

var (
	handoffSecretAssignment = regexp.MustCompile(`(?im)^([ \t]*(?:[-*][ \t]*)?["']?(?:api[_-]?key|access[_-]?token|refresh[_-]?token|password|secret|authorization|proxy-authorization|cookie|set-cookie)["']?[ \t]*[:=][ \t]*)[^\r\n]+`)
	handoffToken            = regexp.MustCompile(`\b(?:(?:sk|rk)[_-]|ghp_)[A-Za-z0-9_-]{12,}\b`)
)

func SanitizeHandoffMarkdown(content string) string {
	content = handoffSecretAssignment.ReplaceAllString(content, `${1}[REDACTED]`)
	return handoffToken.ReplaceAllString(content, "[REDACTED]")
}

func (s *AgentDocumentService) RenderPreview(taskPath string, manifest domain.TaskManifest, config domain.AppConfig, taskNotes string) AgentDocumentPreview {
	preview, err := s.RenderPreviewChecked(taskPath, manifest, config, taskNotes)
	if err != nil {
		return AgentDocumentPreview{Files: []AgentDocumentPreviewFile{{RelativePath: AgentRouterFileName, Content: "预览失败：" + err.Error()}}}
	}
	return preview
}

func (s *AgentDocumentService) RenderPreviewChecked(taskPath string, manifest domain.TaskManifest, config domain.AppConfig, taskNotes string) (AgentDocumentPreview, error) {
	block, err := materialsCatalog(manifest, true)
	if err != nil {
		return AgentDocumentPreview{}, err
	}
	context := renderTaskContext(manifest, repositoryInfos(config), s.Paths)
	if block != "" {
		context, err = replaceMaterialsCatalog(context, block, true)
		if err != nil {
			return AgentDocumentPreview{}, err
		}
	}
	root := renderBaseRouter(manifest, s.Paths)
	if block != "" {
		root += "\n" + block
	}
	return AgentDocumentPreview{Files: []AgentDocumentPreviewFile{
		{RelativePath: AgentRouterFileName, Content: root},
		{RelativePath: filepath.ToSlash(filepath.Join(AgentWorkspaceDir, AgentDirectoryName, AgentContextFileName)), Content: context},
		{RelativePath: filepath.ToSlash(filepath.Join(AgentWorkspaceDir, AgentDirectoryName, AgentRulesFileName)), Content: trimTaskNotesEnd(taskNotes)},
	}}, nil
}

func (s *AgentDocumentService) taskNotesPath(taskPath string) string {
	return filepath.Join(taskPath, AgentWorkspaceDir, AgentDirectoryName, AgentRulesFileName)
}

func (s *AgentDocumentService) writeTaskDocuments(taskPath string, manifest domain.TaskManifest, repositories []domain.RepositoryInfo, taskNotes *string, restoreRoot bool) (string, error) {
	if err := os.MkdirAll(taskPath, 0o755); err != nil {
		return "", err
	}
	root := filepath.Join(taskPath, AgentRouterFileName)
	if restoreRoot {
		if _, err := os.Stat(root); os.IsNotExist(err) {
			if err := writeAtomicDocument(root, renderRootRouter(manifest, s.Paths)); err != nil {
				return "", err
			}
		} else if err != nil {
			return "", err
		}
	}
	// Upgrade only the exact old generated router. Custom/user-edited AGENTS.md
	// stays intact, including when the final workspace is removed.
	router := renderRootRouter(manifest, s.Paths)
	legacyRouter := strings.Replace(router, taskScopeRule, legacyTaskScopeRule, 1)
	if content, err := os.ReadFile(root); err == nil && string(content) == legacyRouter {
		if err := writeAtomicDocument(root, router); err != nil {
			return "", err
		}
	}
	if _, err := s.EnsureGlobalFile(); err != nil {
		return "", err
	}
	if _, err := s.EnsureGroupFile(manifest.GroupID); err != nil {
		return "", err
	}
	agentDir := filepath.Join(taskPath, AgentWorkspaceDir, AgentDirectoryName)
	if err := writeIfChanged(filepath.Join(agentDir, AgentContextFileName), renderTaskContext(manifest, repositories, s.Paths)); err != nil {
		return "", err
	}
	if err := s.RefreshMaterialsDirectories(taskPath, manifest, domain.AppConfig{}, nil); err != nil {
		s.catalogWarningsMu.Lock()
		s.catalogWarnings = append(s.catalogWarnings, err)
		s.catalogWarningsMu.Unlock()
	}
	rules := filepath.Join(agentDir, AgentRulesFileName)
	if taskNotes != nil {
		if err := writeIfChanged(rules, trimTaskNotesEnd(*taskNotes)); err != nil {
			return "", err
		}
	} else if _, err := os.Stat(rules); os.IsNotExist(err) {
		if err := writeAtomicDocument(rules, ""); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	return root, nil
}

func renderBaseRouter(manifest domain.TaskManifest, paths storage.Paths) string {
	groupPath, err := paths.GroupAgents(manifest.GroupID)
	if err != nil {
		groupPath = "<无效组 ID>"
	}
	globalPath := absoluteClean(paths.GlobalAgents)
	groupPath = absoluteClean(groupPath)
	var b strings.Builder
	b.WriteString("# 任务说明\n\n")
	b.WriteString("> 本文件是稳定的系统路由。不要在这里写任务专属要求；请编辑 `.workspace/agent/TASK-RULES.md`。\n\n")
	b.WriteString("在分析、修改代码或执行 Git 写操作前，必须阅读以下文件，以及下方列出的共享规则：\n\n")
	b.WriteString("1. `.workspace/agent/TASK-CONTEXT.md`：需求链接、资料目录、任务交接、系统元数据，以及 Worktree 修改范围。\n")
	b.WriteString("2. `.workspace/agent/TASK-RULES.md`：本任务专属规则。\n\n")
	b.WriteString(taskScopeRule + "\n\n")
	b.WriteString("## 共享规则来源\n\n> 先阅读以下共享规则文件，再结合任务专属规则执行。\n\n")
	b.WriteString("### 全局规则\n\n`" + globalPath + "`\n\n")
	b.WriteString("### 组规则\n\n`" + groupPath + "`\n\n")
	b.WriteString("### 规则优先级\n\n发生冲突时按：任务专属规则 > 组规则 > 全局规则。\n")
	return b.String()
}

func renderTaskContext(manifest domain.TaskManifest, repositories []domain.RepositoryInfo, _ storage.Paths) string {
	var b strings.Builder
	b.WriteString("# 任务上下文\n\n> 本文件由系统生成，会随需求、任务上下文、服务和 Worktree 变动更新。\n\n")
	b.WriteString(renderTaskFacts(manifest))
	b.WriteString("## 系统元数据\n\n- 任务清单是只读元数据；不要手动改写。\n")
	b.WriteString("- `.workspace/HANDOFF.md` 存在时，是 Agent CLI 任务的交接记录。\n\n")
	b.WriteString(renderWorktreeScope(manifest, repositories))
	block, err := materialsCatalog(manifest, false)
	if err != nil {
		block, _ = materialsCatalog(manifest, true)
	}
	if block != "" {
		b.WriteString("\n" + block)
	}
	return b.String()
}

func renderTaskFacts(manifest domain.TaskManifest) string {
	var b strings.Builder
	b.WriteString("## 需求链接\n\n")
	if strings.TrimSpace(manifest.RequirementLink) == "" {
		b.WriteString("（未填写）\n\n")
	} else {
		b.WriteString(manifest.RequirementLink + "\n\n")
	}
	if manifest.AgentContext != nil {
		handoffPath := manifest.AgentContext.HandoffRelativePath
		if handoffPath == "" {
			handoffPath = ".workspace/HANDOFF.md"
		}
		b.WriteString("## 任务交接（仅 Agent CLI 创建）\n\n")
		b.WriteString("在阅读、修改或执行任何任务操作前，先阅读 `" + handoffPath + "`。\n")
		b.WriteString("将其作为本任务的目标、范围、已验证事实、风险与下一步的权威交接记录。\n")
		b.WriteString("若该文件缺失、损坏、过期或与当前工作区状态矛盾，停止执行会产生副作用的操作，先向用户报告差异。\n\n")
	}
	materials := manifest.RequirementMaterials
	if materials.Status == domain.MaterialsReady && materials.WriteRoot != nil {
		if _, ok := documents.TaskMaterialsScope(*materials.WriteRoot); ok {
			return b.String()
		}
	}
	if materials.Status != domain.MaterialsNotRequested {
		b.WriteString("## 任务资料目录\n\n")
		switch materials.Status {
		case domain.MaterialsReady:
			root := ""
			if materials.WriteRoot != nil {
				root = *materials.WriteRoot
			}
			b.WriteString("`" + root + "`\n\n")
			if manifest.AgentContext != nil {
				b.WriteString("Agent 过程文档与需求分析、方案、验收、风险、SQL 和脚本等研发辅助资料统一写入此目录。\n")
				b.WriteString("`.workspace/HANDOFF.md` 位于任务目录，用于记录本任务交接上下文。\n")
			} else {
				b.WriteString("需求辅助 Markdown、SQL 和脚本写入此目录；产品源代码仍写入本任务 Worktree。\n")
			}
			b.WriteString("产品源代码仍写入本任务 Worktree；服务仓库自身的 README、ADR、API 文档等仍留在相应 Worktree。\n")
		case domain.MaterialsFailed:
			b.WriteString("（暂不可用）\n")
			if materials.FailureReason != nil {
				b.WriteString("原因：" + *materials.FailureReason + "\n")
			}
			b.WriteString("可在任务详情中重试。\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func renderWorktreeScope(manifest domain.TaskManifest, allRepositories []domain.RepositoryInfo) string {
	taskRepositories := map[string]bool{}
	for _, workspace := range manifest.Services {
		taskRepositories[workspace.RepositoryID] = true
	}
	other := make([]domain.RepositoryInfo, 0, len(allRepositories))
	for _, repo := range allRepositories {
		if !taskRepositories[repo.ID] {
			other = append(other, repo)
		}
	}
	sort.Slice(other, func(i, j int) bool { return strings.ToLower(other[i].Name) < strings.ToLower(other[j].Name) })
	var b strings.Builder
	b.WriteString("## Worktree 范围\n\n### 本任务可改动的 Worktree\n\n| 服务名 | 创建基线 | 策略 | Worktree 路径 |\n|--------|----------|------|---------------|\n")
	seen := map[string]bool{}
	workspaces := make([]domain.ServiceWorkspace, 0, len(manifest.Services))
	for _, ws := range manifest.Services {
		key := strings.Join([]string{ws.ServiceName, stringValue(ws.BaseRef), string(ws.Strategy), ws.WorktreePath}, "\x00")
		if !seen[key] {
			seen[key] = true
			workspaces = append(workspaces, ws)
		}
	}
	if len(workspaces) == 0 {
		b.WriteString("| （无） |  |  |  |\n")
	} else {
		for _, ws := range workspaces {
			base := ws.Branch
			if ws.BaseRef != nil {
				base = *ws.BaseRef
			}
			b.WriteString("| " + escapeTableCell(ws.ServiceName) + " | `" + escapeTableCell(base) + "` | " + string(ws.Strategy) + " | `" + escapeTableCell(ws.WorktreePath) + "` |\n")
		}
	}
	b.WriteString("\n### 其他本地服务（只读上下文）\n\n下列仓库不在本任务 Worktree 中。它们仅用于阅读代码和梳理调用链，禁止直接修改或提交。\n\n| 服务名 | 本地仓库路径 |\n|--------|--------------|\n")
	if len(other) == 0 {
		b.WriteString("| （无） |  |\n")
	} else {
		for _, repo := range other {
			b.WriteString("| " + escapeTableCell(repo.Name) + " | `" + escapeTableCell(repo.RootPath) + "` |\n")
		}
	}
	b.WriteString("\n## Agent 使用提示\n\n")
	if len(workspaces) == 0 {
		b.WriteString("- 本任务未关联服务，可在任务目录中的分析和文档中开展工作；不要修改任务清单、生成的 Agent 说明或其他系统管理文件。\n- 不要修改主 checkout 或其他本地服务；需要代码工作区时，先在任务中添加服务。\n\n")
	} else {
		b.WriteString("- 产品源代码只允许修改上方“本任务可改动的 Worktree”中的路径；不要修改主 checkout 或其他本地服务。资料按本文件的资料目录清单处理。\n- 需要额外服务时，请先让用户在任务中添加服务。\n\n")
	}
	return b.String()
}

func repositoryInfos(config domain.AppConfig) []domain.RepositoryInfo {
	result := make([]domain.RepositoryInfo, 0, len(config.Repositories))
	for _, repo := range config.Repositories {
		result = append(result, repo.ToInfo())
	}
	return result
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
func escapeTableCell(value string) string { return strings.ReplaceAll(value, "|", "\\|") }
func absoluteClean(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(absolute)
}

func readDocument(path string) (string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func ensureDocumentFile(path, content string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if os.IsExist(err) {
		return path, nil
	}
	if err != nil {
		return "", err
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return path, nil
}

func writeAtomicDocument(target, content string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+"-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temporary.Name()
	if _, err := temporary.WriteString(content); err != nil {
		_ = temporary.Close()
		_ = os.Remove(tempPath)
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		_ = os.Remove(tempPath)
		return err
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	if err := replaceDocumentFile(tempPath, target); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("写入 %s：%w", target, err)
	}
	return nil
}

func writeIfChanged(path, content string) error {
	current, err := os.ReadFile(path)
	if err == nil && string(current) == content {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return writeAtomicDocument(path, content)
}

func replaceDocumentFile(source, target string) error {
	if err := os.Rename(source, target); err == nil {
		return nil
	}
	// Windows does not replace an existing target with Rename. Move the old
	// file aside first and restore it if the replacement fails.
	backup := target + ".silverwing-backup"
	_ = os.Remove(backup)
	if err := os.Rename(target, backup); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(source, target); err != nil {
		_ = os.Rename(backup, target)
		return err
	}
	_ = os.Remove(backup)
	return nil
}
