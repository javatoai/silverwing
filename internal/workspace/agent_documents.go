package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"strings"
	"sync"
	"unicode"

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
	taskScopeRule        = "修改范围以 `TASK-CONTEXT.md` 中的任务范围说明为准。"
)

type AgentDocumentPreviewFile struct {
	RelativePath string `json:"relativePath"`
	Content      string `json:"content"`
}

type AgentDocumentPreview struct {
	Files []AgentDocumentPreviewFile `json:"files"`
}

// AgentDocumentService writes referenced task files for every manifest.
// INLINE_V1 is persisted historical metadata, not a file-generation mode.
type AgentDocumentService struct {
	Paths             storage.Paths
	Manifests         *storage.ManifestStore
	catalogWarningsMu sync.Mutex
	catalogWarnings   []error
}

func NewAgentDocumentService(paths storage.Paths, manifests *storage.ManifestStore) *AgentDocumentService {
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
	return s.writeTaskDocuments(taskPath, manifest, nil, true)
}

func (s *AgentDocumentService) WriteTaskDocument(taskPath string, manifest domain.TaskManifest, config domain.AppConfig) (string, error) {
	return s.writeTaskDocuments(taskPath, manifest, nil, false)
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
	context := renderTaskContext(manifest, s.Paths)
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

func (s *AgentDocumentService) writeTaskDocuments(taskPath string, manifest domain.TaskManifest, taskNotes *string, restoreRoot bool) (string, error) {
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
	if _, err := s.EnsureGlobalFile(); err != nil {
		return "", err
	}
	if _, err := s.EnsureGroupFile(manifest.GroupID); err != nil {
		return "", err
	}
	agentDir := filepath.Join(taskPath, AgentWorkspaceDir, AgentDirectoryName)
	if err := writeIfChanged(filepath.Join(agentDir, AgentContextFileName), renderTaskContext(manifest, s.Paths)); err != nil {
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
	b.WriteString("在分析需求或修改资料前，必须阅读以下文件，以及下方列出的共享规则：\n\n")
	b.WriteString("1. `.workspace/agent/TASK-CONTEXT.md`：需求链接、资料目录、任务交接、系统元数据及任务工作范围。\n")
	b.WriteString("2. `.workspace/agent/TASK-RULES.md`：本任务专属规则。\n\n")
	b.WriteString(taskScopeRule + "\n\n")
	b.WriteString("## 共享规则来源\n\n> 先阅读以下共享规则文件，再结合任务专属规则执行。\n\n")
	b.WriteString("### 全局规则\n\n`" + globalPath + "`\n\n")
	b.WriteString("### 组规则\n\n`" + groupPath + "`\n\n")
	b.WriteString("### 规则优先级\n\n发生冲突时按：任务专属规则 > 组规则 > 全局规则。\n")
	return b.String()
}

func renderTaskContext(manifest domain.TaskManifest, _ storage.Paths) string {
	var b strings.Builder
	b.WriteString("# 任务上下文\n\n> 本文件由系统生成，会随需求和任务资料更新。\n\n")
	b.WriteString(renderTaskFacts(manifest))
	b.WriteString("\n## 工作范围\n\n本任务用于需求分析、测试用例、日志分析与资料整理。请在任务目录和已关联的资料目录内工作。不要修改任务清单和系统管理文件。\n")
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
	fmt.Fprintf(&b, "## 任务信息\n\n- 任务：%s\n- 项目组：%s\n", manifest.FolderName, manifest.GroupID)
	if manifest.RequirementLink != "" {
		fmt.Fprintf(&b, "- 需求：%s\n", manifest.RequirementLink)
	}
	if manifest.RequirementMaterials.Status == domain.MaterialsReady && manifest.RequirementMaterials.WriteRoot != nil {
		fmt.Fprintf(&b, "- 资料目录：%s\n", *manifest.RequirementMaterials.WriteRoot)
	}
	if manifest.AgentContext != nil {
		handoffPath := manifest.AgentContext.HandoffRelativePath
		if handoffPath == "" {
			handoffPath = filepath.ToSlash(filepath.Join(AgentWorkspaceDir, HandoffFileName))
		}
		b.WriteString("\n## 任务交接\n\n")
		b.WriteString("在阅读、修改或执行任何任务操作前，先阅读 `" + handoffPath + "`。\n")
		b.WriteString("将其作为本任务的目标、范围、已验证事实、风险与下一步的权威交接记录。\n")
		b.WriteString("若该文件缺失、损坏、过期或与当前任务状态矛盾，停止执行会产生副作用的操作，先向用户报告差异。\n")
	}
	return b.String()
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
