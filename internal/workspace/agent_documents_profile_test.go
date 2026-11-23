package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestProfileTaskDocumentsAndPreviewOmitTaskRules(t *testing.T) {
	paths := storage.NewPaths(t.TempDir())
	paths.ProfileID = "profile"
	service := NewAgentDocumentService(paths, storage.NewProfileManifestStore(paths))
	manifest := domain.DefaultTaskManifest()
	manifest.ProjectInstructionsGitURL = "https://example.invalid/old-project.git"
	manifest.TaskDirectoryName = "task"
	manifest.RequirementLink = "https://project.feishu.cn/obt/userstory/detail/123"
	manifest.AgentContext = &domain.AgentTaskContext{}
	root := t.TempDir()
	task := filepath.Join(root, manifest.TaskDirectoryName)
	if err := os.Mkdir(task, 0o755); err != nil {
		t.Fatal(err)
	}
	config := domain.DefaultAppConfig()
	config.TaskRoot = &root
	config.ProjectInstructionsGitURL = "https://example.invalid/project-materials.git"
	config.ProjectInstructionsGitLocalRoot = filepath.Join(t.TempDir(), "project-checkout")
	notes := "旧版任务专属规则"
	router, err := service.writeTaskDocuments(task, manifest, config, &notes, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(service.TaskNotesFile(task)); !os.IsNotExist(err) {
		t.Fatalf("task rules file should be absent: %v", err)
	}
	content, err := os.ReadFile(router)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		manifest.RequirementLink, "## 任务交接（仅 Agent CLI 创建）", ".workspace/HANDOFF.md", "## Worktree 范围",
		"必须先阅读并遵守下列规则文件。", "- 全局规则 `" + paths.GlobalAgents + "`",
		"- 项目资料规则 `" + filepath.Join(config.ProjectInstructionsGitLocalRoot, domain.ProjectInstructionsDirectory, AgentRouterFileName) + "`",
		"项目资料中的规则优先于全局规则。",
	} {
		if !strings.Contains(string(content), value) {
			t.Errorf("missing %q in router: %s", value, content)
		}
	}
	if strings.Contains(string(content), AgentRulesFileName) || strings.Contains(string(content), "任务专属规则") {
		t.Errorf("router still routes to task rules: %s", content)
	}
	for _, removed := range []string{"## 系统元数据", "任务清单是只读元数据", "存在时，是 Agent CLI 任务的交接记录"} {
		if strings.Contains(string(content), removed) {
			t.Errorf("router still contains %q", removed)
		}
	}
	preview, err := service.RenderPreviewChecked(task, manifest, config, notes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(task, AgentWorkspaceDir, AgentDirectoryName, "TASK-CONTEXT.md")); !os.IsNotExist(err) {
		t.Fatalf("standalone context was generated: %v", err)
	}
	wantRouter := AgentDocumentPreviewFile{RelativePath: AgentRouterFileName, Content: string(content)}
	if len(preview.Files) != 1 || preview.Files[0] != wantRouter {
		t.Fatalf("preview should only contain the generated AGENTS.md: %+v", preview.Files)
	}
	config.ProjectInstructionsGitURL, config.ProjectInstructionsGitLocalRoot = "", ""
	manifest.AgentContext = nil
	withoutProject := service.taskInstructionsBlock(manifest, config)
	if strings.Contains(withoutProject, "项目资料") || strings.Contains(withoutProject, ".workspace/HANDOFF.md") || !strings.Contains(withoutProject, paths.GlobalAgents) {
		t.Errorf("unexpected rule sources without project materials: %s", withoutProject)
	}
}

func TestProfileTaskDocumentUpdatesPreserveUserTextAndOldRules(t *testing.T) {
	for _, operation := range []string{"regenerate", "rebind"} {
		t.Run(operation, func(t *testing.T) {
			paths := storage.NewPaths(t.TempDir())
			paths.ProfileID = "profile"
			service := NewAgentDocumentService(paths, storage.NewProfileManifestStore(paths))
			manifest := domain.DefaultTaskManifest()
			manifest.TaskDirectoryName = "task"
			manifest.ProjectInstructionsGitURL = "https://example.invalid/project.git"
			root := t.TempDir()
			config := domain.DefaultAppConfig()
			config.TaskRoot = &root
			config.ProjectInstructionsGitURL = manifest.ProjectInstructionsGitURL
			task := filepath.Join(root, manifest.TaskDirectoryName)
			if err := os.Mkdir(task, 0o755); err != nil {
				t.Fatal(err)
			}
			if _, err := service.EnsureTaskDocuments(task, manifest, config); err != nil {
				t.Fatal(err)
			}
			const header = "\ufeff# 我的任务说明\r\n\r\n"
			const tail = "\r\n\r\n## 手写说明\r\n\r\n保留这里的内容。\r\n"
			previousBlock := profileRouterStart + "\n请阅读 `.workspace/agent/TASK-RULES.md`。\n\n规则优先级为任务专属规则、项目指令、全局规则。\n\n" +
				"## 系统元数据\n\n- 任务清单是只读元数据；不要手动改写。\n- `.workspace/HANDOFF.md` 存在时，是 Agent CLI 任务的交接记录。\n\n" + profileRouterEnd
			router := filepath.Join(task, AgentRouterFileName)
			if err := os.WriteFile(router, []byte(header+previousBlock+tail), 0o600); err != nil {
				t.Fatal(err)
			}
			const notes = "旧规则保留\r\n  \r\n"
			rules := service.TaskNotesFile(task)
			if err := os.MkdirAll(filepath.Dir(rules), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(rules, []byte(notes), 0o600); err != nil {
				t.Fatal(err)
			}
			config.ProjectInstructionsGitLocalRoot = filepath.Join(t.TempDir(), "active-project-checkout")
			var err error
			if operation == "regenerate" {
				_, err = service.regenerateTaskDocuments(task, manifest, config)
			} else {
				err = service.RebindProfileTaskDocuments(task, manifest, config)
			}
			if err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(router)
			want := header + service.taskInstructionsBlock(manifest, config) + tail
			if err != nil || string(content) != want || strings.Contains(string(content), AgentRulesFileName) || strings.Contains(string(content), "## 系统元数据") {
				t.Fatalf("managed router or user text differ: %q %v", content, err)
			}
			if !strings.Contains(string(content), filepath.Join(config.ProjectInstructionsGitLocalRoot, domain.ProjectInstructionsDirectory, AgentRouterFileName)) {
				t.Fatal("router did not use the currently configured project checkout")
			}
			preserved, err := os.ReadFile(rules)
			if err != nil || string(preserved) != notes {
				t.Fatalf("old rules changed: %q %v", preserved, err)
			}
		})
	}
}

func TestRegenerateProfileRouterReplacesKnownLegacyRules(t *testing.T) {
	const tail = "\r\n<!-- silverwing:materials:start -->\r\n资料目录\r\n<!-- silverwing:materials:end -->\r\n\r\n手写说明\r\n"
	const block = profileRouterStart + "\n最新项目资料和全局规则\n" + profileRouterEnd
	legacy := renderLegacyTaskRouter(`D:\旧配置\agents\global.md`, `D:\旧配置\agents\groups\testing.md`)
	legacy = strings.Replace(legacy, taskScopeRule, legacyTaskScopeRule, 1)
	result, err := replaceGeneratedRouter("\ufeff"+strings.ReplaceAll(legacy, "\n", "\r\n")+tail, block, true)
	want := "\ufeff# 任务说明\r\n\r\n" + strings.ReplaceAll(block, "\n", "\r\n") + "\r\n" + tail
	if err != nil || result != want {
		t.Fatalf("legacy generated rules or user text differ: %q %v", result, err)
	}
}

func TestRegenerateTaskAgentsRejectsBrokenManagedBlocks(t *testing.T) {
	block := profileRouterStart + "\nlatest\n" + profileRouterEnd
	for _, invalid := range []string{
		profileRouterStart,
		profileRouterEnd + "\n" + profileRouterStart,
		block + "\n" + block,
		block + "\n" + materialsCatalogEnd,
		profileRouterStart + "\n" + materialsCatalogStart + "\n" + profileRouterEnd + "\n" + materialsCatalogEnd,
	} {
		if updated, err := replaceGeneratedRouter(invalid, block, true); err == nil || updated != "" {
			t.Fatalf("accepted invalid managed blocks: %q %v", invalid, err)
		}
	}
}
