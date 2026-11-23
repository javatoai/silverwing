package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"silverwing-go/internal/domain"
)

const profileRouterStart = "<!-- silverwing:instructions:start -->"
const profileRouterEnd = "<!-- silverwing:instructions:end -->"

func (s *AgentDocumentService) taskInstructionsBlock(manifest domain.TaskManifest, config domain.AppConfig) string {
	var block strings.Builder
	block.WriteString(profileRouterStart + "\n")
	block.WriteString("必须先阅读并遵守下列规则文件。修改范围以本文的 Worktree 范围为准。未关联服务时可在任务目录处理分析和文档。\n\n")
	fmt.Fprintf(&block, "- 全局规则 `%s`\n", s.Paths.GlobalAgents)
	project := config.ProjectInstructionsGitSettings()
	if root := domain.ProjectInstructionsRepositoryRoot(s.Paths.Home, project); root != "" {
		fmt.Fprintf(&block, "- 项目资料规则 `%s`\n", filepath.Join(root, domain.ProjectInstructionsDirectory, AgentRouterFileName))
		block.WriteString("\n项目资料中的规则优先于全局规则。\n")
	}
	if s.Paths.ProfileID == "" {
		if groupPath, err := s.Paths.GroupAgents(manifest.GroupID); err == nil {
			fmt.Fprintf(&block, "- 组规则：`%s`\n", groupPath)
		}
		block.WriteString("- 任务专属规则：`.workspace/agent/TASK-RULES.md`\n\n发生冲突时优先使用任务专属规则，其次使用组规则和全局规则。\n")
	}
	block.WriteByte('\n')
	block.WriteString(renderTaskFacts(manifest))
	block.WriteString(renderWorktreeScope(manifest, repositoryInfos(config)))
	block.WriteString(profileRouterEnd)
	return block.String()
}

func (s *AgentDocumentService) renderTaskAgents(manifest domain.TaskManifest, config domain.AppConfig, catalog string) string {
	content := "# 任务说明\n\n" + s.taskInstructionsBlock(manifest, config) + "\n"
	if catalog != "" {
		content += "\n" + catalog
	}
	return content
}

func (s *AgentDocumentService) renderTaskAgentsUpdate(original string, manifest domain.TaskManifest, config domain.AppConfig, regenerate bool) (string, error) {
	if original == "" {
		catalog, err := materialsCatalog(manifest, false)
		if err != nil {
			catalog, _ = materialsCatalog(manifest, true)
		}
		return s.renderTaskAgents(manifest, config, catalog), nil
	}
	return replaceGeneratedRouter(original, s.taskInstructionsBlock(manifest, config), regenerate)
}

// RebindProfileTaskDocuments refreshes generated paths once the application
// opens a moved profile. Task rules and text outside managed blocks stay intact.
func (s *AgentDocumentService) RebindProfileTaskDocuments(taskPath string, manifest domain.TaskManifest, config domain.AppConfig) error {
	if err := NewTaskOperationLocks(s.Paths).ValidateWorkspaceDirectory(config, taskPath, manifest); err != nil {
		return err
	}
	if err := s.writeTaskInstructions(taskPath, manifest, config, false, false); err != nil {
		return err
	}
	return s.RefreshMaterialsDirectories(taskPath, manifest, config, nil)
}

func validateAgentTaskDocumentPaths(taskPath string, paths ...string) error {
	info, err := os.Lstat(taskPath)
	if err != nil {
		return err
	}
	for _, path := range paths {
		if _, _, _, err := inspectOwnedTaskPath(taskPath, info, path, false); err != nil {
			return err
		}
	}
	return nil
}

func (s *AgentDocumentService) writeTaskInstructions(taskPath string, manifest domain.TaskManifest, config domain.AppConfig, restoreRoot, appendBlock bool) error {
	task, err := os.Lstat(taskPath)
	if err != nil {
		return err
	}
	path, previous, exists, err := inspectOwnedTaskPath(taskPath, task, AgentRouterFileName, false)
	if err != nil || (!exists && !restoreRoot) {
		return err
	}
	var original []byte
	if exists {
		original, err = os.ReadFile(path)
		if err != nil {
			return err
		}
	}
	next, err := s.renderTaskAgentsUpdate(string(original), manifest, config, appendBlock)
	if err != nil {
		return fmt.Errorf("更新 %s：%w", path, err)
	}
	if exists && next == string(original) {
		return nil
	}
	check := func() error {
		_, current, stillExists, err := inspectOwnedTaskPath(taskPath, task, AgentRouterFileName, false)
		if err == nil && (stillExists != exists || (exists && !os.SameFile(previous, current))) {
			return errMaterialsCatalogChanged
		}
		return err
	}
	return writeMaterialsCatalogFile(path, original, []byte(next), !exists, check)
}

func replaceGeneratedRouter(current, block string, regenerate bool) (string, error) {
	if err := validateTaskAgentsBlocks(current); err != nil {
		return "", err
	}
	start, end := strings.Index(current, profileRouterStart), strings.Index(current, profileRouterEnd)
	if start < 0 {
		if indices := legacyTaskRouterPrefix.FindStringSubmatchIndex(current); indices != nil {
			prefix := current[:indices[3]]
			endOfLine := "\n"
			if strings.HasSuffix(prefix, "\r\n") {
				endOfLine = "\r\n"
				block = strings.ReplaceAll(block, "\n", endOfLine)
			}
			return prefix + block + endOfLine + current[indices[1]:], nil
		}
		if !regenerate {
			return current, nil
		}
		return current + "\n\n" + block + "\n", nil
	}
	return current[:start] + block + current[end+len(profileRouterEnd):], nil
}

func validateTaskAgentsBlocks(content string) error {
	instructionsStart, instructionsEnd := strings.Index(content, profileRouterStart), strings.Index(content, profileRouterEnd)
	catalogStart, catalogEnd := strings.Index(content, materialsCatalogStart), strings.Index(content, materialsCatalogEnd)
	if !validAgentBlock(content, profileRouterStart, profileRouterEnd, instructionsStart, instructionsEnd) ||
		!validAgentBlock(content, materialsCatalogStart, materialsCatalogEnd, catalogStart, catalogEnd) ||
		(instructionsStart >= 0 && catalogStart >= 0 && instructionsStart < catalogEnd && catalogStart < instructionsEnd) {
		return errors.New("任务说明的系统管理区标记缺失、重复或顺序错误，请修复标记后重试")
	}
	return nil
}

func validAgentBlock(content, start, end string, startIndex, endIndex int) bool {
	return (startIndex == -1 && endIndex == -1) || (startIndex >= 0 && endIndex > startIndex && strings.Count(content, start) == 1 && strings.Count(content, end) == 1)
}

// Legacy routers were generated without markers. Match only the original
// template with its two variable file paths so appended user text stays intact.
var legacyTaskRouterPrefix = func() *regexp.Regexp {
	const title = "# 任务说明\n\n"
	const globalPath, groupPath = "SILVERWING-GLOBAL-RULES", "SILVERWING-GROUP-RULES"
	template := regexp.QuoteMeta(strings.TrimPrefix(renderLegacyTaskRouter(globalPath, groupPath), title))
	for _, path := range []string{globalPath, groupPath} {
		template = strings.Replace(template, path, "[^`\\r\\n]+", 1)
	}
	template = strings.Replace(template, regexp.QuoteMeta(taskScopeRule),
		"(?:"+regexp.QuoteMeta(taskScopeRule)+"|"+regexp.QuoteMeta(legacyTaskScopeRule)+")", 1)
	return regexp.MustCompile(`\A((?:\x{FEFF})?# 任务说明\r?\n\r?\n)` + strings.ReplaceAll(template, "\n", `\r?\n`))
}()
