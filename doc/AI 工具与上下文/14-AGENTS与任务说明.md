# AGENTS 与任务说明

[返回知识库目录](../Index.md)

Silverwing 在任务根目录生成 `AGENTS.md`，集中说明需求、资料目录、工作区修改范围和共享规则来源。通过任务目录打开外部 Agent 时，可以从这份文件开始了解任务。

## 全局说明与项目资料

在 **设置 → 资料与说明 → 全局说明** 编辑全局 Markdown。页面自动保存，可从更多操作中重新读取、复制说明或显示文件位置。若外部编辑器同时修改了同一文件，按提示接受磁盘版本或确认覆盖。

- **全局说明**适用于当前配置管理的任务。任务 `AGENTS.md` 列出全局说明文件的实际路径。
- **项目资料**在 **设置 → 资料与说明 → 项目资料** 配置。配置后，任务 `AGENTS.md` 会指向对应 Git 仓库中的 `project-materials/AGENTS.md`。项目资料中的规则优先于全局规则。
- 开始任务前，外部 Agent 必须阅读并遵守任务 `AGENTS.md` 列出的全局规则和已配置项目的规则文件。已打开的会话何时重新读取，由所用 Agent 决定。

可从以下短例子起步，再按团队习惯调整：

```markdown
# 研发协作规则

## 修改范围
- 只修改任务根 `AGENTS.md` 列出的可改 Worktree。
- 其他本地仓库仅用于阅读，不直接修改或提交。
- 不把密钥、令牌或真实用户数据写入代码和说明。

## 验证方式
- 先阅读目标模块现有说明和脚本，再选择相关验证命令。
- 不猜测未记录的启动步骤；无法验证时明确说明原因。

## 交付说明
- 汇报改动文件、验证命令和结果、尚未解决的问题。
- Git 写操作前先检查当前分支和工作区状态。
```

## 任务说明

创建任务时，点击 **查看生成说明** 可只读预览根 `AGENTS.md`。创建后，在 **任务文件** 中打开根 `AGENTS.md`。需要刷新内容时，右键点击左侧的任务行，选择 **重新生成任务说明**。

```text
<任务目录>/
├─ AGENTS.md
└─ .workspace/
   └─ HANDOFF.md    # 仅有 Agent CLI 交接上下文时出现
```

| 文件 | 用途 | 如何维护 |
| --- | --- | --- |
| 根目录 `AGENTS.md` | 列出需求链接、资料目录、可改 Worktree、其他仓库的只读边界、共享规则路径及任务交接入口。 | 系统更新 `silverwing:instructions` 和 `silverwing:materials` 两个区，保留区外的手写内容。应用内只读。 |
| `.workspace/HANDOFF.md` | Agent CLI 创建任务时的交接记录，包含目标、范围、已验证事实、风险和建议下一步。 | 任务清单带交接上下文时生成，根 `AGENTS.md` 会提示读取。 |

根据 `AGENTS.md` 中的 Worktree 表格确认当前任务允许修改的范围。需要调整范围时，在 Silverwing 中增删服务或模块，再读取更新后的说明。

资料管理区列出本人配置的资料子目录和 `temp` 的完整路径。其他同级目录属于团队成员，Agent 可以自行查找和阅读相关资料。这些同级目录在任务上下文中为只读，Git 同步仍遵守资料仓库的跟踪和忽略规则。

## 源码索引

- 文件内容生成及交接模板见 `internal/workspace/agent_documents.go`。
- 创建预览见 `internal/workspace/task_agent_preview.go` 和 `internal/desktop/task_creation_generated_preview.go`。
- 说明的读写路径见 `internal/application/instruction_actions.go`。

相关阅读：[第一个多仓库任务](../入门与基础/05-第一个多仓库任务.md)、[任务与工作区管理](../日常使用/06-任务与工作区管理.md)、[IDE 与 Agent 配置](13-IDE与Agent配置.md)、[外部 CLI 与 Codex 集成](../技术架构/26-外部CLI与Codex集成.md)。
