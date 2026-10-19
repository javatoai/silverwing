# AGENTS 与说明模板

[返回知识库目录](../Index.md)

> 适用：Silverwing Go / MyGO，源码版本 0.0.1；按 2026-10-19 的代码核对。

Silverwing 把共享规则和任务说明写成普通 Markdown 文件，再在任务根目录生成一个 `AGENTS.md` 路由文件。外部 Agent 是否读取这些文件，取决于它如何打开工作区以及是否遵循文件中的说明；Silverwing 本身不会自动运行或调度多个 Agent。

![共享规则、任务说明和 Agent 上下文文件的生成关系示意图](../assets/agent-context.png)

## 共享规则：全局与项目组

进入 **设置 → AGENTS.md → 全局与组说明**，先选 **全局** 或一个项目组，再编辑 Markdown。页面自动保存，可从更多操作中重新读取、复制说明或显示文件位置；若外部编辑器同时修改了同一文件，按提示接受磁盘版本或确认覆盖。

- **全局规则**适用于使用 Silverwing 创建的各类任务，文件位于 `<Silverwing Home>/agents/global/AGENTS.md`。
- **项目组规则**只针对所选项目组，文件位于 `<Silverwing Home>/agents/groups/<GroupID>/AGENTS.md`。例如 `group-payments` 是组 ID，不一定等于屏幕上的显示名称。
- 两份共享规则由任务根的 `AGENTS.md` 引用，不复制进每个任务目录。修改共享文件不会重写任务清单或生成文件；若外部 Agent 能访问这些路径并按路由读取，新会话可读到当前内容。已打开会话是否重新读取，由所用 Agent 决定。

可从以下短例子起步，再按团队习惯调整：

```markdown
# 研发协作规则

## 修改范围
- 只修改任务上下文列出的可改 Worktree。
- 其他本地仓库仅用于阅读，不直接修改或提交。
- 不把密钥、令牌或真实用户数据写入代码和说明。

## 验证方式
- 先阅读目标模块现有说明和脚本，再选择相关验证命令。
- 不猜测未记录的启动步骤；无法验证时明确说明原因。

## 交付说明
- 汇报改动文件、验证命令和结果、尚未解决的问题。
- Git 写操作前先检查当前分支和工作区状态。
```

## 任务说明模板

到 **设置 → AGENTS.md → 任务说明模板**，选择 **新建模板**，填写名称与内容后保存。模板保存在 `<Silverwing Home>/agents/task-templates.json`，用于在任务创建时快速填入任务级说明，不会自动替换占位符，也不会覆盖全局或项目组规则。

一个可复用的模板例子：

```markdown
## 本任务目标
- 实现：填写要完成的行为。

## 验收标准
- 正常输入：填写期望结果。
- 异常情况：填写需要保留的边界行为。

## 验证要求
- 运行：填写与本模块相关的命令。
- 记录未运行的检查及原因。

## 交付边界
- 只修改任务上下文列出的 Worktree。
- 未经确认不执行提交、推送或发布。
```

从需求创建任务时，切到右侧 **编辑任务说明**，点击单个模板即可将内容填入本次任务；这一步不会直接改写模板本身。若当前说明已有不同内容，Silverwing 会先询问是否替换。选 **任务说明预览** 可查看实时生成的 `AGENTS.md`、`TASK-CONTEXT.md` 和 `TASK-RULES.md`，确认后再点 **检查并创建任务**。任务创建完成后，也可在任务详情的说明区域编辑并保存 `TASK-RULES.md`。

## 生成文件各自负责什么

```text
<任务目录>/
├─ AGENTS.md
└─ .workspace/
   ├─ agent/
   │  ├─ TASK-CONTEXT.md
   │  └─ TASK-RULES.md
   └─ HANDOFF.md             # 仅有 Agent CLI 交接上下文时出现
```

| 文件 | 用途 | 如何维护 |
| --- | --- | --- |
| 根目录 `AGENTS.md` | 稳定路由：要求 Agent 先读任务上下文和规则，并列出全局、项目组规则路径。 | 系统生成；不要手工编辑。若任务根已有同名文件，生成逻辑会保留现有文件。 |
| `.workspace/agent/TASK-CONTEXT.md` | 从任务清单生成需求链接、资料目录、可改 Worktree 表格、其他仓库只读边界；有 CLI 交接上下文时也会提示先读交接文件。 | 系统生成并随任务信息更新；只读。 |
| `.workspace/agent/TASK-RULES.md` | 本任务专属要求，通常来自创建任务时的说明或所选模板。 | 通过任务创建界面或任务详情说明区域编辑、保存。 |
| `.workspace/HANDOFF.md` | Agent CLI 创建任务时的交接记录，包含目标、范围、已验证事实、风险和建议下一步。 | 只有任务清单带交接上下文时才生成；不是 `TASK-RULES.md`，也不承载仓库清单或系统元数据。 |

根路由声明的三类说明优先级为 **任务专属规则 > 项目组规则 > 全局规则**。例如，全局规则要求运行全量检查，而任务规则要求先运行聚焦测试时，路由约定优先采用任务规则。这是写入 Markdown 的约定，不是 Silverwing 对外部 Agent 的强制执行机制；实际读取与冲突处理还取决于 Agent 及其上层指令。需要修改共享要求时，更新全局或组说明，不要修改根路由。

`TASK-CONTEXT.md` 中的 Worktree 表格是当前任务允许修改范围的来源。任务清单 `silverwing.json` 和系统生成的 `AGENTS.md`、`TASK-CONTEXT.md` 都不是说明编辑入口；需要调整范围时，在 Silverwing 任务里增删服务或模块，再重新读取生成结果。不要手工改写任务元数据或把产品源代码放进资料目录。

## 源码索引

- 文件路由、内容生成及交接模板：`internal/workspace/agent_documents.go`
- 创建表单预览：`internal/workspace/task_agent_preview.go`、`internal/desktop/task_creation_ui.go`
- 全局、组和任务说明的读写边界：`internal/application/instruction_actions.go`、`internal/desktop/task_notes_ui.go`
- 任务说明模板持久化：`internal/documents/templates.go`、`internal/storage/paths.go`

相关阅读：[第一个多仓库任务](../入门与基础/05-第一个多仓库任务.md)、[任务与工作区管理](../日常使用/06-任务与工作区管理.md)、[IDE 与 Agent 配置](13-IDE与Agent配置.md)、[外部 CLI 与 Codex 集成](../技术架构/26-外部CLI与Codex集成.md)。
